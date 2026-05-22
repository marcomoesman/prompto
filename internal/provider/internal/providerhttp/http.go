package providerhttp

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"
)

const ErrorBodyLimit = 64 * 1024

func DefaultTransport() *http.Transport {
	// No ResponseHeaderTimeout: prefill on a long-context turn against a
	// self-hosted LLM (vLLM / sglang / llama.cpp) can take several
	// minutes before the first byte. The per-turn ctx from the agent
	// loop (cancellable via Esc/Ctrl+C) is the right primitive for
	// bounding wait time; a fixed transport-level cap killed long
	// follow-up requests mid-turn even when the model was actively
	// streaming on prior round-trips.
	//
	// IdleConnTimeout sets how long a pooled connection sits unused
	// before we close it. Note "idle" means literally no active
	// request — a slow prefill, a long streaming generation, and a
	// SSE response with multi-second gaps between events all count
	// as ACTIVE, not idle, so this timeout never affects an in-flight
	// inference. It only governs the gap BETWEEN turns.
	//
	// 60s is a deliberate compromise. The race we're guarding
	// against — "use of closed network connection" on a POST after a
	// long subagent gap — has multiple plausible upstream causes:
	//
	//   1. Server-side keep-alive expiry. uvicorn (vLLM, sglang)
	//      defaults to 5s; llama.cpp server is typically ~60s;
	//      nginx fronts at 75s; commercial APIs hold longer.
	//   2. NAT / stateful-firewall idle eviction. Cross-subnet
	//      traffic (192.168.10.x → 192.168.4.x in the user's
	//      report) crosses a router whose conntrack table can
	//      drop silent established TCP at 30-90s on consumer
	//      hardware, and at hours on enterprise gear.
	//   3. TCP keep-alive probes failing. We set 30s probes via
	//      the dialer, but a silently-dropped conn takes ~4 min
	//      to surface that way (default 9 retries × 30s).
	//
	// 60s closes early enough that the most aggressive of those
	// causes (uvicorn at 5s, cheap-router NAT at 30-60s) rarely
	// catches us holding a dead conn, while still letting back-to-
	// back subagent turns reuse the pool. The actual safety net
	// for any race we DO lose is NewIdempotentPOST: GetBody +
	// Idempotency-Key flips POST into Go's auto-retry path, so a
	// stale conn surfaces as one wasted Write, not a user-visible
	// error. Tightening further would gain little; loosening would
	// just shift more requests from "happy path" to "wasted write
	// + transparent retry," which is a latency hit, not a failure.
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		IdleConnTimeout:       60 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
	}
}

// NewIdempotentPOST constructs a POST request that net/http will
// transparently retry if the underlying TCP connection turns out to
// be stale (server-side keep-alive expired between requests). LLM
// completion POSTs are naturally idempotent: at the moment of a
// stale-connection error no bytes have reached the server, and even
// for partial flushes the worst case is a re-run of an inference —
// no real-world side effect.
//
// Three things are required for Go's transport to enable the retry:
//
//   - The request has GetBody set, so the transport can re-issue the
//     body on a fresh connection.
//   - The request carries an Idempotency-Key (or X-Idempotency-Key)
//     header. POST is non-replayable by default in net/http; either
//     header flips Request.isReplayable() to true. The header is
//     ignored by every provider we talk to, so it's purely a
//     client-side signal — see https://golang.org/issue/19943.
//   - The error class must be one of the retryable patterns (most
//     importantly nothingWrittenError, which is what a closed-pool
//     conn produces when Write fails before any bytes flush).
//
// Without this helper, prompto's POST → /chat/completions and
// /v1/messages calls would fail terminally on the first stale-conn
// error of a long session — exactly the symptom the user hit during
// a multi-minute subagent run.
func NewIdempotentPOST(ctx context.Context, url string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// GetBody lets net/http rebuild the body reader on retry. Without
	// this, even a "replayable" POST cannot actually be replayed.
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	// Idempotency-Key flips POST into the replayable bucket. The value
	// is per-request-unique so duplicate detection on any future
	// idempotency-aware backend works correctly; current providers
	// just ignore the header.
	req.Header.Set("Idempotency-Key", uuid.NewString())
	return req, nil
}

func ReadErrorBody(r io.Reader) (string, bool) {
	data, err := io.ReadAll(io.LimitReader(r, ErrorBodyLimit+1))
	if err != nil {
		return "", false
	}
	truncated := len(data) > ErrorBodyLimit
	if truncated {
		data = data[:ErrorBodyLimit]
	}
	return string(data), truncated
}
