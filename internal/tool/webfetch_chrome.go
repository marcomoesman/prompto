package tool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

const webfetchChromeTimeout = 30 * time.Second

// ErrChromeMissing is returned when the chromedp executor cannot find a
// Chrome / Chromium binary. Surfaced as a sentinel so the caller can
// gracefully fall back rather than aborting the tool call.
var ErrChromeMissing = errors.New("webfetch: chrome/chromium not found — install Chrome or set CHROME_PATH")

// fetchWithChromeFn is the package-level seam the escalation path uses.
// Tests swap this for a stub; production keeps the default chromedp
// implementation.
var fetchWithChromeFn = realFetchWithChrome

// realFetchWithChrome runs a headless-Chrome navigation and returns
// the rendered outerHTML. Honours CHROME_PATH for environments with a
// non-default Chrome location.
//
// Stealth measures applied here:
//   - Init script that masks the headless fingerprint (navigator.webdriver,
//     userAgentData, etc.)
//   - Chrome-shaped extra headers via Network.setExtraHTTPHeaders
//   - 1920×1080 viewport, en-US locale, system-tz timezone
//   - NetworkIdle2 wait so SPAs and lazy hydrators get a chance to settle
func realFetchWithChrome(ctx context.Context, rawURL string) (string, error) {
	// Fast-fail when the parent ctx is already cancelled. Without
	// this gate, chromedp.NewExecAllocator + NewContext still spin
	// up the three-level ctx tree (and the allocator briefly looks
	// for a Chrome binary on disk) before chromedp.Run notices the
	// cancellation and returns. Cheap to skip, free when ctx is alive.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, webfetchChromeTimeout)
	defer timeoutCancel()

	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts, chromedp.DisableGPU)
	if chromePath := os.Getenv("CHROME_PATH"); chromePath != "" {
		opts = append(opts, chromedp.ExecPath(chromePath))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(timeoutCtx, opts...)
	defer allocCancel()

	chromeCtx, chromeCancel := chromedp.NewContext(allocCtx)
	defer chromeCancel()

	var htmlContent string
	chromedp.ListenTarget(chromeCtx, func(ev any) {
		paused, ok := ev.(*fetch.EventRequestPaused)
		if !ok || paused.Request == nil {
			return
		}
		go func() {
			if err := validateURLForFetch(chromeCtx, paused.Request.URL, nil); err != nil {
				_ = fetch.FailRequest(paused.RequestID, network.ErrorReasonBlockedByClient).Do(chromeCtx)
				return
			}
			_ = fetch.ContinueRequest(paused.RequestID).Do(chromeCtx)
		}()
	})

	// Pre-navigation: install stealth, set headers/viewport/locale/tz.
	preActions := []chromedp.Action{
		fetch.Enable().WithPatterns([]*fetch.RequestPattern{{
			URLPattern:   "*",
			RequestStage: fetch.RequestStageRequest,
		}}),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(stealthScript).Do(ctx)
			return err
		}),
		network.Enable(),
		network.SetExtraHTTPHeaders(headlessExtraHeaders()),
		emulation.SetDeviceMetricsOverride(1920, 1080, 1.0, false),
		emulation.SetLocaleOverride().WithLocale("en-US"),
		emulation.SetTimezoneOverride(systemTimezone()),
	}

	// Navigate, then wait for network idle (≤2 in-flight for 500ms),
	// then read outerHTML.
	mainActions := []chromedp.Action{
		chromedp.Navigate(rawURL),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return waitNetworkIdle(ctx, 2, 500*time.Millisecond, 5*time.Second)
		}),
		chromedp.OuterHTML("html", &htmlContent),
	}

	all := append(preActions, mainActions...)
	if err := chromedp.Run(chromeCtx, all...); err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "not found") ||
			strings.Contains(errMsg, "no such file") ||
			strings.Contains(errMsg, "executable file not found") ||
			strings.Contains(errMsg, "exec:") {
			return "", ErrChromeMissing
		}
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("fetching %s: timed out after %s", rawURL, webfetchChromeTimeout)
		}
		return "", fmt.Errorf("fetching %s with headless browser: %w", rawURL, err)
	}
	return htmlContent, nil
}

// headlessExtraHeaders returns the subset of Chrome 146 headers that
// headless Chrome doesn't already send by default — primarily the
// cache-busting pragma + accept-language. sec-ch-ua* and sec-fetch-*
// are emitted by Chrome itself; setting them via CDP would risk
// duplication.
func headlessExtraHeaders() network.Headers {
	return network.Headers{
		"Accept-Language": chromeAcceptLanguage,
		"Cache-Control":   "no-cache",
		"Pragma":          "no-cache",
	}
}

// systemTimezone returns the operating system's timezone identifier,
// falling back to "America/New_York" when it's empty, "Local",
// "UTC", or otherwise unparseable. The fallback matches the most
// common public-internet fingerprint and avoids the headless-tell
// of UTC-anywhere.
func systemTimezone() string {
	loc := time.Local.String()
	if loc == "" || loc == "Local" || loc == "UTC" {
		return "America/New_York"
	}
	return loc
}

// jsShellMarkers are HTML substrings strongly indicating client-side
// rendering. The list is short on purpose — false-positives waste a
// chromedp invocation; false-negatives are recoverable by the user
// re-issuing the call (which doesn't happen any more once we escalate
// transparently, but the fallback model is more permissive than restrictive).
var jsShellMarkers = []string{
	"__NEXT_DATA__",
	"window.__INITIAL_STATE__",
	`id="__nuxt"`,
	`id="root"></div>`,
	`id="app"></div>`,
}

// looksJSRendered returns true when the plain-HTTP fetch likely missed
// the page's actual content. Combines a markdown-body-size floor with a
// list of common SPA shell markers.
func looksJSRendered(rawHTML, markdown string) bool {
	if len(strings.TrimSpace(markdown)) < 200 {
		return true
	}
	for _, marker := range jsShellMarkers {
		if strings.Contains(rawHTML, marker) {
			return true
		}
	}
	return false
}
