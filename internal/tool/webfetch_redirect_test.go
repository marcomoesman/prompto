package tool

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

// TestCheckRedirect_BlocksRobotsDisallowedTarget regresses a gap
// where checkRedirect re-validated the SSRF guard on every redirect
// hop but did NOT re-check robots.txt. With respect_robots_txt
// enabled, a 302 from an allowed URL into a Disallow:-listed path
// (same host or a different one) would slip through unfiltered.
func TestCheckRedirect_BlocksRobotsDisallowedTarget(t *testing.T) {
	robots := &RobotsCache{
		cache: map[string]robotsEntry{
			"example.com": {
				rules: []robotsRule{
					{path: "/blocked", allow: false},
				},
				expiresAt: time.Now().Add(time.Hour),
			},
		},
		fetchFn: func(_ context.Context, _ string) (string, error) {
			t.Fatal("fetchFn should not be called when cache is hot")
			return "", nil
		},
	}
	tool := &WebFetchTool{
		respectRobots: true,
		robots:        robots,
	}

	target, _ := url.Parse("https://example.com/blocked/page")
	req := &http.Request{URL: target}
	req = req.WithContext(context.Background())

	err := tool.checkRedirect(req, nil)
	if err == nil {
		t.Fatal("checkRedirect should refuse a robots-disallowed redirect target")
	}
	if !errors.Is(err, ErrRobotsBlocked) {
		t.Errorf("expected ErrRobotsBlocked, got %v", err)
	}
}

// TestCheckRedirect_AllowsRobotsAllowedTarget covers the happy path —
// when robots.txt permits the redirect target, checkRedirect returns
// nil and the request proceeds.
func TestCheckRedirect_AllowsRobotsAllowedTarget(t *testing.T) {
	robots := &RobotsCache{
		cache: map[string]robotsEntry{
			"example.com": {
				rules:     nil, // empty rules = allow everything
				expiresAt: time.Now().Add(time.Hour),
			},
		},
		fetchFn: func(_ context.Context, _ string) (string, error) {
			t.Fatal("fetchFn should not be called when cache is hot")
			return "", nil
		},
	}
	tool := &WebFetchTool{
		respectRobots: true,
		robots:        robots,
	}

	target, _ := url.Parse("https://example.com/allowed")
	req := &http.Request{URL: target, Header: http.Header{}}
	req = req.WithContext(context.Background())

	if err := tool.checkRedirect(req, nil); err != nil {
		t.Errorf("allowed target should not error: %v", err)
	}
}

// TestCheckRedirect_NoRobotsCheckWhenDisabled confirms that the
// redirect check is gated on respect_robots_txt — leaving the
// feature off should not introduce a robots.txt fetch into every
// redirect.
func TestCheckRedirect_NoRobotsCheckWhenDisabled(t *testing.T) {
	tool := &WebFetchTool{
		respectRobots: false,
		// robots is nil intentionally — must never be dereferenced.
	}

	target, _ := url.Parse("https://example.com/anything")
	req := &http.Request{URL: target, Header: http.Header{}}
	req = req.WithContext(context.Background())

	if err := tool.checkRedirect(req, nil); err != nil {
		t.Errorf("disabled robots: redirect should pass cleanly, got %v", err)
	}
}
