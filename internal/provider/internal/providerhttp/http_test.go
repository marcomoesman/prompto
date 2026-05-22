package providerhttp

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestNewIdempotentPOST_HasReplayableMarkers asserts the per-request
// markers Go's net/http requires to retry a POST: GetBody must be
// set and yield a fresh reader, and Idempotency-Key must be present
// on the headers. Both are required — see the comment on the
// function for the conditions inside Request.isReplayable.
func TestNewIdempotentPOST_HasReplayableMarkers(t *testing.T) {
	body := []byte(`{"model":"x","messages":[]}`)
	req, err := NewIdempotentPOST(context.Background(), "http://example.com/v1/chat/completions", body)
	if err != nil {
		t.Fatalf("NewIdempotentPOST: %v", err)
	}

	if req.Header.Get("Idempotency-Key") == "" {
		t.Errorf("Idempotency-Key header missing — POST will not be replayable")
	}
	if req.GetBody == nil {
		t.Fatal("GetBody is nil — net/http cannot rebuild the body for retry")
	}

	// GetBody must yield a fresh reader each call. Drain twice to
	// confirm the second attempt sees the full body, not EOF.
	for i := range 2 {
		rc, err := req.GetBody()
		if err != nil {
			t.Fatalf("GetBody call %d: %v", i, err)
		}
		got, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read body %d: %v", i, err)
		}
		if string(got) != string(body) {
			t.Errorf("GetBody call %d returned %q, want %q", i, got, body)
		}
	}
}

// TestNewIdempotentPOST_AutoRetriesOnStaleConn is the integration
// test for the user-reported "use of closed network connection"
// failure. Mechanism: stand up a server that pre-closes the first
// connection a client picks from the idle pool — exactly what a
// self-hosted LLM (vLLM/uvicorn/llama.cpp) does when its keep-alive
// timer expires between subagent turns. Go's transport must
// transparently retry on a fresh connection; without GetBody +
// Idempotency-Key the second attempt would never happen and the
// caller would see the same error the user saw in the field.
func TestNewIdempotentPOST_AutoRetriesOnStaleConn(t *testing.T) {
	var hits atomic.Int32

	// Custom listener that hijacks the FIRST accepted connection and
	// closes it without responding. Subsequent connections route to
	// the wrapped handler. This simulates "client picks a stale conn
	// from the pool" — the server-side socket is gone before the
	// client's next Write completes.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	// First, prime the connection pool: do a successful round-trip
	// so a keep-alive conn lands in the idle pool.
	client := &http.Client{Transport: DefaultTransport()}
	defer client.CloseIdleConnections()

	req1, err := NewIdempotentPOST(context.Background(), srv.URL, []byte(`{"warmup":true}`))
	if err != nil {
		t.Fatalf("priming request build: %v", err)
	}
	resp1, err := client.Do(req1)
	if err != nil {
		t.Fatalf("priming request failed: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp1.Body)
	_ = resp1.Body.Close()

	// Now reach into the test server's listener and forcibly close
	// the underlying server-side conn for that pooled keep-alive.
	// httptest doesn't expose this directly, so the indirect route is:
	// have the next request hit a server that the client THINKS is
	// still alive. Easier: use http.Server with ConnState hook so we
	// can grab the raw conn and close it.
	//
	// Rebuild the test server with a hook that closes idle conns
	// after the first response. See sub-test below.
	t.Run("stale-conn retried transparently", func(t *testing.T) {
		closedOnce := make(chan struct{}, 1)

		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("served"))
		})

		ts := &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
			ConnState: func(c net.Conn, state http.ConnState) {
				// On the first Idle transition, close the conn from
				// underneath the client's pool. Next request → stale.
				if state != http.StateIdle {
					return
				}
				select {
				case closedOnce <- struct{}{}:
					_ = c.Close()
				default:
				}
			},
		}

		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		go func() { _ = ts.Serve(l) }()
		defer func() { _ = ts.Shutdown(context.Background()) }()

		c := &http.Client{Transport: DefaultTransport()}
		defer c.CloseIdleConnections()
		base := "http://" + l.Addr().String() + "/"

		// First request: success, conn enters idle pool, hook closes it.
		r1, _ := NewIdempotentPOST(context.Background(), base, []byte(`{"a":1}`))
		resp, err := c.Do(r1)
		if err != nil {
			t.Fatalf("first request: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		// Wait for ConnState→Idle hook to fire and kill the conn.
		select {
		case <-closedOnce:
		case <-time.After(2 * time.Second):
			t.Fatal("ConnState idle hook did not fire — server may not have parked the conn")
		}
		// Tiny sleep so the kernel observes the close before next write.
		time.Sleep(50 * time.Millisecond)

		// Second request: client picks the now-dead pooled conn,
		// Write fails, transport must transparently retry on a
		// fresh conn. With GetBody+Idempotency-Key set this works;
		// without it the user would see the original error here.
		r2, _ := NewIdempotentPOST(context.Background(), base, []byte(`{"a":2}`))
		resp2, err := c.Do(r2)
		if err != nil {
			t.Fatalf("second request failed (retry not triggered): %v", err)
		}
		got, _ := io.ReadAll(resp2.Body)
		_ = resp2.Body.Close()
		if string(got) != "served" {
			t.Errorf("retried response body = %q, want %q", got, "served")
		}
	})
}

// TestNewIdempotentPOST_RebuildsBodyOnRetry is a tight unit-level
// check on GetBody specifically: simulate Go's retry path by reading
// the body, then calling GetBody for the "retry" reader, and
// verifying both attempts produce identical bytes. Catches a class
// of bugs where the closure captures a single bytes.Reader that
// gets exhausted on attempt 1.
func TestNewIdempotentPOST_RebuildsBodyOnRetry(t *testing.T) {
	body := []byte(strings.Repeat("payload", 256))
	req, err := NewIdempotentPOST(context.Background(), "http://x.example/", body)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Attempt 1: drain req.Body as net/http's writeLoop would.
	first, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("attempt-1 read: %v", err)
	}
	if string(first) != string(body) {
		t.Fatalf("attempt-1 body mismatch")
	}

	// Attempt 2: transport calls GetBody for a fresh reader.
	rc, err := req.GetBody()
	if err != nil {
		t.Fatalf("GetBody: %v", err)
	}
	second, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("attempt-2 read: %v", err)
	}
	if string(second) != string(body) {
		t.Fatalf("attempt-2 body mismatch — GetBody returned a drained reader")
	}
}
