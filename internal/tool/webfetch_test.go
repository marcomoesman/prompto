package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestWebFetchTool creates a WebFetchTool without summarization,
// permitting loopback so httptest fixtures bypass the SSRF guard.
// Production callers never set allowedHosts.
func newTestWebFetchTool() *WebFetchTool {
	wf := NewWebFetchTool(nil)
	wf.allowedHosts = fetchAllowedHosts{"127.0.0.1", "[::1]"}
	return wf
}

func TestWebFetchToolBasic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><h1>Hello World</h1><p>Some content here.</p></body></html>`))
	}))
	defer srv.Close()

	wf := newTestWebFetchTool()
	input, _ := json.Marshal(WebFetchInput{URL: srv.URL, Query: "page content"})
	result, err := wf.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(result.Content, "Hello World") {
		t.Errorf("expected Hello World in result, got:\n%s", result.Content)
	}
}

func TestWebFetchToolWithSummarizer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><h1>Big Page</h1><p>Lots of content here.</p></body></html>`))
	}))
	defer srv.Close()

	// Mock summarizer that returns a fixed summary.
	mockSummarizer := func(_ context.Context, content, query string) (string, error) {
		return "Summary: " + query, nil
	}

	wf := NewWebFetchTool(mockSummarizer)
	wf.allowedHosts = fetchAllowedHosts{"127.0.0.1", "[::1]"}
	input, _ := json.Marshal(WebFetchInput{URL: srv.URL, Query: "what is this page about"})
	result, err := wf.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Content != "Summary: what is this page about" {
		t.Errorf("expected summarized result, got:\n%s", result.Content)
	}
}

func TestWebFetchToolNonHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key": "value"}`))
	}))
	defer srv.Close()

	wf := newTestWebFetchTool()
	input, _ := json.Marshal(WebFetchInput{URL: srv.URL, Query: "json data"})
	result, err := wf.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !strings.Contains(result.Content, `"key"`) {
		t.Errorf("expected raw JSON, got:\n%s", result.Content)
	}
}

func TestWebFetchToolBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	wf := newTestWebFetchTool()
	input, _ := json.Marshal(WebFetchInput{URL: srv.URL, Query: "test"})
	_, err := wf.Execute(t.Context(), newTestCtx(t), input)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %q, expected 404 mention", err.Error())
	}
}

// TestWebFetchToolSendsChromeClientHints asserts the Phase-13 header
// set: every sec-ch-ua* + sec-fetch-* + upgrade-insecure-requests
// header lands on the request, plus Accept-Language and the Chrome 146
// User-Agent. Fixture content must be varied — the Phase-17 markdown
// dup-collapse would otherwise compress repeated paragraphs to 1,
// pushing the rendered markdown below the JS-shell heuristic and
// triggering chromedp escalation (whose headers would then mask ours).
func TestWebFetchToolSendsChromeClientHints(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`<html><body><h1>OK</h1>`)
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&sb, "<p>Filler paragraph number %d with enough text to keep the rendered markdown body comfortably above the JS-shell threshold.</p>", i)
	}
	sb.WriteString(`</body></html>`)
	largeBody := sb.String()

	captured := http.Header{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range r.Header {
			captured[k] = v
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(largeBody))
	}))
	defer srv.Close()

	wf := newTestWebFetchTool()
	input, _ := json.Marshal(WebFetchInput{URL: srv.URL, Query: "test"})
	if _, err := wf.Execute(t.Context(), newTestCtx(t), input); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	required := map[string]string{
		"Sec-Ch-Ua":                 chromeSecChUA,
		"Sec-Ch-Ua-Mobile":          chromeSecChUAMobile,
		"Sec-Ch-Ua-Platform":        chromeSecChUAPlatform,
		"Sec-Fetch-Dest":            "document",
		"Sec-Fetch-Mode":            "navigate",
		"Sec-Fetch-Site":            "none",
		"Sec-Fetch-User":            "?1",
		"Upgrade-Insecure-Requests": "1",
		"Accept-Language":           chromeAcceptLanguage,
	}
	for k, want := range required {
		got := captured.Get(k)
		if got != want {
			t.Errorf("header %q = %q, want %q", k, got, want)
		}
	}
	if got := captured.Get("User-Agent"); got != chromeUserAgent {
		t.Errorf("User-Agent = %q, want chrome 146", got)
	}
}

// TestWebFetchToolCookieRoundTrip asserts the per-instance jar carries
// a Set-Cookie from one call into the Cookie header of a subsequent
// call to the same host.
func TestWebFetchToolCookieRoundTrip(t *testing.T) {
	largeBody := `<html><body><p>` + strings.Repeat("filler ", 60) + `</p></body></html>`

	hits := 0
	gotCookie := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc123", Path: "/"})
		} else {
			if c, err := r.Cookie("session"); err == nil {
				gotCookie = c.Value
			}
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(largeBody))
	}))
	defer srv.Close()

	wf := newTestWebFetchTool()
	input, _ := json.Marshal(WebFetchInput{URL: srv.URL, Query: "first"})
	if _, err := wf.Execute(t.Context(), newTestCtx(t), input); err != nil {
		t.Fatalf("Execute call 1: %v", err)
	}
	if _, err := wf.Execute(t.Context(), newTestCtx(t), input); err != nil {
		t.Fatalf("Execute call 2: %v", err)
	}
	if gotCookie != "abc123" {
		t.Errorf("second call did not carry session cookie; got %q", gotCookie)
	}
}

// TestWebFetchToolRejectsPrivateIP asserts the SSRF guard refuses a
// direct private-IP URL without making any HTTP request.
func TestWebFetchToolRejectsPrivateIP(t *testing.T) {
	wf := NewWebFetchTool(nil) // no allowedHosts — guard is at full strength
	input, _ := json.Marshal(WebFetchInput{URL: "http://10.0.0.1/secret", Query: "x"})
	_, err := wf.Execute(t.Context(), newTestCtx(t), input)
	if err == nil {
		t.Fatal("expected SSRF rejection")
	}
	if !strings.Contains(err.Error(), "SSRF") && !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error = %q, want SSRF / blocked mention", err)
	}
}

// TestWebFetchToolRejectsLocalhost asserts the SSRF guard rejects the
// literal hostname "localhost" even when the test harness allows
// 127.0.0.1 — name-keyed rejects come before the IP-class check.
func TestWebFetchToolRejectsLocalhost(t *testing.T) {
	wf := newTestWebFetchTool() // 127.0.0.1 allowed, but "localhost" still blocked
	input, _ := json.Marshal(WebFetchInput{URL: "http://localhost:9999/", Query: "x"})
	_, err := wf.Execute(t.Context(), newTestCtx(t), input)
	if err == nil {
		t.Fatal("expected SSRF rejection of localhost hostname")
	}
}

// TestWebFetchToolBlocksRedirectToInternal asserts that even when the
// initial host passes the guard, a redirect target inside a blocked
// range is refused.
func TestWebFetchToolBlocksRedirectToInternal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()

	wf := newTestWebFetchTool()
	input, _ := json.Marshal(WebFetchInput{URL: srv.URL, Query: "x"})
	_, err := wf.Execute(t.Context(), newTestCtx(t), input)
	if err == nil {
		t.Fatal("expected redirect to AWS metadata to be blocked")
	}
}

// TestWebFetchToolAutoEscalatesOnJSShell exercises the Phase-10 merge:
// when the plain-HTTP fetch returns a JS-shell HTML, the tool should
// transparently call the chromedp helper and use its rendered output.
// fetchWithChromeFn is swapped for a stub so the test stays hermetic.
func TestWebFetchToolAutoEscalatesOnJSShell(t *testing.T) {
	jsShell := `<!doctype html><html><head><title>App</title></head><body><div id="root"></div><script>__NEXT_DATA__={};</script></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(jsShell))
	}))
	defer srv.Close()

	rendered := `<html><body><h1>Rendered Heading</h1><p>` + strings.Repeat("Real client-rendered content. ", 20) + `</p></body></html>`
	called := 0
	original := fetchWithChromeFn
	fetchWithChromeFn = func(_ context.Context, gotURL string) (string, error) {
		called++
		if gotURL != srv.URL {
			t.Errorf("chromedp URL = %q, want %q", gotURL, srv.URL)
		}
		return rendered, nil
	}
	t.Cleanup(func() { fetchWithChromeFn = original })

	wf := newTestWebFetchTool()
	input, _ := json.Marshal(WebFetchInput{URL: srv.URL, Query: "test"})
	result, err := wf.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if called != 1 {
		t.Errorf("chromedp helper called %d times, want 1", called)
	}
	if !strings.Contains(result.Content, "Rendered Heading") {
		t.Errorf("expected rendered content, got:\n%s", result.Content)
	}
}

// TestWebFetchToolEscalationFallsBackOnChromeMissing covers the
// graceful-degradation path: if the headless browser is unavailable, the
// model still gets the plain-HTTP markdown plus a one-line note rather
// than a hard error.
func TestWebFetchToolEscalationFallsBackOnChromeMissing(t *testing.T) {
	jsShell := `<!doctype html><html><body><div id="__nuxt"></div></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(jsShell))
	}))
	defer srv.Close()

	original := fetchWithChromeFn
	fetchWithChromeFn = func(_ context.Context, _ string) (string, error) {
		return "", ErrChromeMissing
	}
	t.Cleanup(func() { fetchWithChromeFn = original })

	wf := newTestWebFetchTool()
	input, _ := json.Marshal(WebFetchInput{URL: srv.URL, Query: "test"})
	result, err := wf.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute should not error on chrome-missing: %v", err)
	}
	if !strings.Contains(result.Content, "headless browser is unavailable") {
		t.Errorf("expected fallback note, got:\n%s", result.Content)
	}
}

func TestWebFetchToolEmptyURL(t *testing.T) {
	wf := newTestWebFetchTool()
	input, _ := json.Marshal(WebFetchInput{URL: "", Query: "test"})
	_, err := wf.Execute(t.Context(), newTestCtx(t), input)
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestWebFetchToolInvalidScheme(t *testing.T) {
	wf := newTestWebFetchTool()
	input, _ := json.Marshal(WebFetchInput{URL: "file:///etc/passwd", Query: "test"})
	_, err := wf.Execute(t.Context(), newTestCtx(t), input)
	if err == nil {
		t.Fatal("expected error for file:// URL")
	}
}

func TestWebFetchToolDefinitionSchema(t *testing.T) {
	wf := newTestWebFetchTool()
	def := wf.Definition()
	if def.Name != "webfetch" {
		t.Errorf("Name = %q", def.Name)
	}
	var schema map[string]any
	if err := json.Unmarshal(def.InputSchema, &schema); err != nil {
		t.Fatalf("schema parse: %v", err)
	}
	props := schema["properties"].(map[string]any)
	if _, ok := props["url"]; !ok {
		t.Error("schema missing url property")
	}
	if _, ok := props["query"]; !ok {
		t.Error("schema missing query property")
	}
}
