package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
)

const (
	webfetchTimeout = 5 * time.Second
	webfetchMaxBody = 5 * 1024 * 1024 // 5MB raw body limit
)

// chromeUserAgent is the canonical Chrome 146 / Linux UA every plain
// fetch claims. MUST be kept in sync with the tls_client profile
// pinned below: drift between the wire fingerprint and the UA string
// is a fingerprint inconsistency that defeats the point of the
// migration. When updating the profile to a newer Chrome version,
// update this constant in the same edit.
const chromeUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"

// chromeProfile is the bogdanfinn/tls-client profile that drives both
// the TLS ClientHello AND the HTTP/2 SETTINGS / pseudoHeaderOrder /
// connectionFlow we emit. Bump alongside chromeUserAgent.
var chromeProfile = profiles.Chrome_146

// Chrome 146 navigation headers. tls_client handles the wire-level
// fingerprint (TLS ClientHello, HTTP/2 SETTINGS, pseudoHeaderOrder)
// but does NOT add request headers — those are still ours to set.
// All values must remain coherent with chromeUserAgent above.
const (
	chromeAccept          = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"
	chromeAcceptLanguage  = "en-US,en;q=0.9"
	chromeSecChUA         = `"Chromium";v="146", "Not;A=Brand";v="24", "Google Chrome";v="146"`
	chromeSecChUAMobile   = "?0"
	chromeSecChUAPlatform = `"Linux"`
)

// WebFetchInput defines the JSON parameters for the webfetch tool.
type WebFetchInput struct {
	URL   string `json:"url"   jsonschema:"required,description=The URL to fetch"`
	Query string `json:"query" jsonschema:"required,description=A clear instruction for what to extract from this page. Write it as a task e.g. 'Summarize all bitcoin projects written in Golang listed on this page' or 'Extract the project description and key features and usage examples'."`
}

// WebFetchTool fetches web pages and returns their content as markdown.
type WebFetchTool struct {
	definition api.ToolDefinition
	client     tls_client.HttpClient
	summarize  Summarizer
	// allowedHosts is the SSRF-guard escape hatch used by tests. nil in
	// production so loopback / private IPs are always rejected.
	allowedHosts fetchAllowedHosts
	// robots is consulted before each fetch when respectRobots is true.
	// nil cache + flag-off both disable the check.
	robots        *RobotsCache
	respectRobots bool
}

// WebFetchOptions tunes the WebFetchTool at construction time.
// All fields are optional.
type WebFetchOptions struct {
	// RespectRobotsTxt enables a per-host /robots.txt check before
	// every fetch. Off by default. Fail-open on /robots.txt errors.
	RespectRobotsTxt bool
}

// NewWebFetchTool creates a WebFetchTool whose HTTP client emits a
// Chrome 146-shaped TLS ClientHello AND HTTP/2 SETTINGS frame via
// bogdanfinn/tls-client. Cookies persist for the lifetime of the
// returned tool (one TUI session). The summarize closure runs the
// rendered markdown through an LLM when non-nil.
func NewWebFetchTool(summarize Summarizer, opts ...WebFetchOptions) *WebFetchTool {
	var o WebFetchOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	t := &WebFetchTool{
		definition: api.ToolDefinition{
			Name:        "webfetch",
			Description: "Fetch a URL and return a summary of its content. Tries plain HTTP first; transparently re-fetches via a headless browser when the page is JavaScript-rendered. Sends Chrome 146-realistic headers, emulates Chrome's TLS ClientHello and HTTP/2 SETTINGS, and persists cookies across calls within a session.",
			InputSchema: GenerateSchema(WebFetchInput{}),
		},
		summarize:     summarize,
		respectRobots: o.RespectRobotsTxt,
	}
	if o.RespectRobotsTxt {
		t.robots = NewRobotsCache()
	}

	jar := newSessionJar()
	client, err := tls_client.NewHttpClient(
		tls_client.NewNoopLogger(),
		tls_client.WithClientProfile(chromeProfile),
		tls_client.WithDisableHttp3(),
		tls_client.WithCookieJar(jar),
		tls_client.WithDialer(t.guardedDialer()),
		tls_client.WithCustomRedirectFunc(t.checkRedirect),
		tls_client.WithTimeoutSeconds(int(webfetchTimeout/time.Second)),
	)
	if err != nil {
		// NewHttpClient only returns an error if you pass nonsensical
		// options. Construction-time programming errors should panic
		// because there's no recovery path that produces a working tool.
		panic(fmt.Sprintf("webfetch: tls_client.NewHttpClient: %v", err))
	}
	t.client = client
	return t
}

func (t *WebFetchTool) Name() string                   { return "webfetch" }
func (t *WebFetchTool) Definition() api.ToolDefinition { return t.definition }
func (t *WebFetchTool) MaxResultBytes() int            { return 0 }
func (t *WebFetchTool) IsReadOnly() bool               { return true }
func (t *WebFetchTool) IsConcurrencySafe() bool        { return true }

// Close releases idle TCP connections held by the underlying tls_client
// transport. Safe to call multiple times and on a nil receiver. Wired
// into the process shutdown path so a long-lived TUI session doesn't
// leave keep-alive sockets dangling against the OS's per-process FD
// limit.
func (t *WebFetchTool) Close() error {
	if t == nil || t.client == nil {
		return nil
	}
	t.client.CloseIdleConnections()
	return nil
}

// PermissionKey returns "domain:<host>" so rules can allow-list hosts
// (e.g. {tool:webfetch, pattern:"domain:github.com", action:allow}).
func (t *WebFetchTool) PermissionKey(input []byte) string {
	return webfetchPermissionKey(input)
}

func webfetchPermissionKey(input []byte) string {
	var params WebFetchInput
	if err := json.Unmarshal(input, &params); err != nil {
		return ""
	}
	host := hostFromURL(params.URL)
	if host == "" {
		return ""
	}
	return "domain:" + host
}

func hostFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func (t *WebFetchTool) FormatForDisplay(input []byte) string {
	var params WebFetchInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "WebFetch(?)"
	}
	kvs := []string{"url", params.URL}
	if params.Query != "" {
		kvs = append(kvs, "prompt", params.Query)
	}
	return FormatCall("WebFetch", kvs...)
}

// fetchOutcome captures everything the display summary needs: the
// response status (or 0 on hard failure), the raw body byte count,
// the post-conversion markdown byte count (when the body was HTML
// and got rendered), and whether the chromedp escalation path
// engaged.
//
// rawBytes is what came off the wire (HTML, JSON, etc.).
// markdownBytes is what the model actually sees after htmlToMarkdown
// — populated only on the HTML-rendered path; left zero on
// passthrough (non-HTML content, JSON APIs).
type fetchOutcome struct {
	status         int
	rawBytes       int
	markdownBytes  int
	escalated      bool
	chromeFallback string // non-empty when escalation was tried but failed
}

func (o fetchOutcome) summary() string {
	statusText := "unknown"
	if o.status > 0 {
		statusText = fmt.Sprintf("%d %s", o.status, http.StatusText(o.status))
	}
	// Show both raw and rendered sizes when we converted HTML to
	// markdown — gives a quick view of "how much did the page weigh
	// vs how much did the model actually read". Non-HTML passthroughs
	// have markdownBytes == 0; show only the raw size in that case.
	var sizeStr string
	if o.markdownBytes > 0 && o.markdownBytes != o.rawBytes {
		sizeStr = fmt.Sprintf("%s → %s markdown", HumanizeBytes(o.rawBytes), HumanizeBytes(o.markdownBytes))
	} else {
		sizeStr = HumanizeBytes(o.rawBytes)
	}
	s := fmt.Sprintf("Received %s (%s)", sizeStr, statusText)
	switch {
	case o.escalated:
		s += " · escalated to headless"
	case o.chromeFallback != "":
		s += " · headless " + o.chromeFallback
	}
	return s
}

func (t *WebFetchTool) Execute(ctx context.Context, tc agent.ToolContext, input []byte) (agent.Result, error) {
	var params WebFetchInput
	if err := json.Unmarshal(input, &params); err != nil {
		return agent.Result{}, fmt.Errorf("invalid input: %w", err)
	}

	if err := validateURLForFetch(ctx, params.URL, t.allowedHosts); err != nil {
		return agent.Result{}, err
	}

	if t.respectRobots && t.robots != nil {
		if u, _ := url.Parse(params.URL); u != nil {
			if !t.robots.IsAllowed(ctx, u.Scheme, u.Host, u.Path) {
				return agent.Result{}, fmt.Errorf("%w: %s", ErrRobotsBlocked, params.URL)
			}
		}
	}

	tc.Status("Fetching")
	body, contentType, status, err := t.fetch(ctx, params.URL)
	if err != nil {
		return agent.Result{}, fmt.Errorf("fetching %s: %w", params.URL, err)
	}

	outcome := fetchOutcome{status: status, rawBytes: len(body)}

	// Non-HTML content: return raw body truncated. markdownBytes
	// stays zero so the display summary shows only the raw size.
	if !strings.Contains(contentType, "text/html") {
		full := len(body)
		if len(body) > htmlMaxOutput {
			body = truncateOnRune(body, htmlMaxOutput) + "\n[Content truncated at 50KB]"
		}
		return agent.Result{
			Content:        body,
			Bytes:          full,
			DisplaySummary: outcome.summary(),
		}, nil
	}

	md, err := htmlToMarkdown(body, params.URL)
	if err != nil {
		return agent.Result{}, err
	}
	outcome.markdownBytes = len(md)

	// Auto-escalate when the plain-HTTP body looks JS-rendered. On
	// chromedp failure (binary missing, navigation timeout) fall back
	// to the plain-HTTP markdown rather than aborting.
	if looksJSRendered(body, md) {
		tc.Status("Rendering with headless browser")
		rendered, cerr := fetchWithChromeFn(ctx, params.URL)
		switch {
		case cerr == nil:
			if escMD, herr := htmlToMarkdown(rendered, params.URL); herr == nil {
				md = escMD
				outcome.escalated = true
				outcome.rawBytes = len(rendered)
				outcome.markdownBytes = len(md)
			}
		case errors.Is(cerr, ErrChromeMissing):
			md = md + "\n\n[Page appears JS-rendered but headless browser is unavailable: " + cerr.Error() + ". Returning plain-HTTP markdown.]"
			outcome.chromeFallback = "unavailable"
		default:
			md = md + "\n\n[Page appears JS-rendered; headless browser fetch failed: " + cerr.Error() + ". Returning plain-HTTP markdown.]"
			outcome.chromeFallback = "failed"
		}
	}

	if t.summarize != nil {
		tc.Status("Summarizing")
		summary, err := t.summarize(ctx, md, params.Query)
		if err != nil {
			s := fmt.Sprintf("[webfetch summarization error: %v]", err)
			return agent.Result{
				Content:        s,
				Bytes:          len(s),
				DisplaySummary: outcome.summary(),
			}, nil
		}
		return agent.Result{
			Content:        summary,
			Bytes:          len(summary),
			DisplaySummary: outcome.summary(),
		}, nil
	}

	return agent.Result{
		Content:        md,
		Bytes:          len(md),
		DisplaySummary: outcome.summary(),
	}, nil
}

// fetch performs one HTTP GET via tls_client. The library handles TLS
// fingerprinting, HTTP/2 SETTINGS, ALPN-driven dispatch, cookie
// persistence (via the jar configured at construction), and redirect
// callbacks. Per-call cancellation is honoured via req.Context().
func (t *WebFetchTool) fetch(ctx context.Context, rawURL string) (body string, contentType string, status int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", 0, err
	}
	addChromeHeaders(req)

	resp, err := t.client.Do(req)
	if err != nil {
		return "", "", 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	status = resp.StatusCode
	contentType = resp.Header.Get("Content-Type")

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain a bounded prefix so connection re-use stays clean,
		// then surface the status as an error.
		_, _ = io.CopyN(io.Discard, resp.Body, 4096)
		return "", contentType, status, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, webfetchMaxBody+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", contentType, status, err
	}
	if len(data) > webfetchMaxBody {
		return "", contentType, status, fmt.Errorf("response too large (>5MB)")
	}
	return string(data), contentType, status, nil
}

// checkRedirect is wired into tls_client via WithCustomRedirectFunc.
// Same shape as stdlib net/http.Client.CheckRedirect — re-validates
// every hop's target through the SSRF guard so a 302 to
// 169.254.169.254 (cloud metadata) or to 10.0.0.1 (LAN admin page)
// is refused before the request goes out.
func (t *WebFetchTool) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	if err := validateURLForFetch(req.Context(), req.URL.String(), t.allowedHosts); err != nil {
		return err
	}
	// robots.txt is per-URL, not per-domain — a redirect into a
	// disallowed path on the same host (or a different host the
	// initial check never saw) must not slip through just because
	// the first URL was allowed. checkRedirect uses the same fail-
	// open semantics as the entry-point check: a robots.txt fetch
	// failure does not block the request.
	if t.respectRobots && t.robots != nil {
		if !t.robots.IsAllowed(req.Context(), req.URL.Scheme, req.URL.Host, req.URL.Path) {
			return fmt.Errorf("%w: %s", ErrRobotsBlocked, req.URL.String())
		}
	}
	// Re-stamp the deterministic header set on every hop. tls_client's
	// HTTP/2 layer manages pseudo-headers and sec-ch-ua* itself; this
	// resets the Accept-* / User-Agent we control.
	addChromeHeaders(req)
	return nil
}

// addChromeHeaders stamps the full Chrome 146 navigation header set
// onto req. tls_client owns the wire-level fingerprint (TLS
// ClientHello + HTTP/2 SETTINGS + pseudoHeaderOrder); request headers
// remain our responsibility. All values must stay coherent with
// chromeUserAgent / chromeProfile above.
func addChromeHeaders(req *http.Request) {
	req.Header.Set("User-Agent", chromeUserAgent)
	req.Header.Set("Accept", chromeAccept)
	req.Header.Set("Accept-Language", chromeAcceptLanguage)
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Sec-Ch-Ua", chromeSecChUA)
	req.Header.Set("Sec-Ch-Ua-Mobile", chromeSecChUAMobile)
	req.Header.Set("Sec-Ch-Ua-Platform", chromeSecChUAPlatform)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
}
