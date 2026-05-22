package tool

import (
	"context"
	"errors"
	"testing"
)

func TestRobotsCache_HonoursDisallow(t *testing.T) {
	c := NewRobotsCache()
	c.fetchFn = func(_ context.Context, _ string) (string, error) {
		return "User-agent: *\nDisallow: /private\nAllow: /public\n", nil
	}
	if c.IsAllowed(t.Context(), "https", "example.com", "/private/file") {
		t.Error("/private/file should be disallowed")
	}
	if !c.IsAllowed(t.Context(), "https", "example.com", "/public/page") {
		t.Error("/public/page should be allowed")
	}
	if !c.IsAllowed(t.Context(), "https", "example.com", "/index.html") {
		t.Error("paths outside Disallow should be allowed")
	}
}

func TestRobotsCache_FailOpenOnFetchError(t *testing.T) {
	c := NewRobotsCache()
	c.fetchFn = func(_ context.Context, _ string) (string, error) {
		return "", errors.New("network down")
	}
	if !c.IsAllowed(t.Context(), "https", "example.com", "/anything") {
		t.Error("expected fail-open on fetch error")
	}
}

func TestRobotsCache_LongestMatchWins(t *testing.T) {
	c := NewRobotsCache()
	c.fetchFn = func(_ context.Context, _ string) (string, error) {
		return "User-agent: *\nDisallow: /docs\nAllow: /docs/public\n", nil
	}
	if !c.IsAllowed(t.Context(), "https", "example.com", "/docs/public/page") {
		t.Error("/docs/public/page should be allowed (longer Allow wins)")
	}
	if c.IsAllowed(t.Context(), "https", "example.com", "/docs/private") {
		t.Error("/docs/private should be disallowed")
	}
}

func TestRobotsCache_IgnoresOtherUserAgentGroups(t *testing.T) {
	c := NewRobotsCache()
	c.fetchFn = func(_ context.Context, _ string) (string, error) {
		// Disallow scoped to a specific bot, not *.
		return "User-agent: Googlebot\nDisallow: /\n\nUser-agent: *\nAllow: /\n", nil
	}
	if !c.IsAllowed(t.Context(), "https", "example.com", "/anything") {
		t.Error("rule scoped to other UA must not affect us")
	}
}

func TestRobotsCache_CachesAcrossCalls(t *testing.T) {
	c := NewRobotsCache()
	hits := 0
	c.fetchFn = func(_ context.Context, _ string) (string, error) {
		hits++
		return "User-agent: *\nAllow: /\n", nil
	}
	for i := 0; i < 5; i++ {
		_ = c.IsAllowed(t.Context(), "https", "example.com", "/x")
	}
	if hits != 1 {
		t.Errorf("fetch called %d times, want 1", hits)
	}
}
