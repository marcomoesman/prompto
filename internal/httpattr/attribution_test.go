package httpattr

import (
	"net/http"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/version"
)

func TestApply_SetsAllAttributionHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	Apply(req)

	if got := req.Header.Get("HTTP-Referer"); got != RefererURL {
		t.Errorf("HTTP-Referer = %q, want %q", got, RefererURL)
	}
	wantTitle := "prompto/v" + version.Version
	if got := req.Header.Get("X-Title"); got != wantTitle {
		t.Errorf("X-Title = %q, want %q", got, wantTitle)
	}
	wantUA := "prompto/" + version.Version
	if got := req.Header.Get("User-Agent"); got != wantUA {
		t.Errorf("User-Agent = %q, want %q", got, wantUA)
	}
}

func TestApply_OverwritesExistingHeaders(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	req.Header.Set("HTTP-Referer", "https://attacker.example/spoof")
	req.Header.Set("X-Title", "stale")
	req.Header.Set("User-Agent", "Go-http-client/1.1")

	Apply(req)

	if got := req.Header.Get("HTTP-Referer"); got != RefererURL {
		t.Errorf("HTTP-Referer was not overwritten: got %q", got)
	}
	if got := req.Header.Get("X-Title"); !strings.HasPrefix(got, "prompto/v") {
		t.Errorf("X-Title was not overwritten: got %q", got)
	}
	if got := req.Header.Get("User-Agent"); got == "Go-http-client/1.1" {
		t.Errorf("User-Agent left at Go default: %q", got)
	}
}

func TestXTitle_IncludesVersion(t *testing.T) {
	got := XTitle()
	if !strings.HasPrefix(got, "prompto/v") {
		t.Errorf("XTitle = %q, want prompto/v… prefix", got)
	}
	if !strings.Contains(got, version.Version) {
		t.Errorf("XTitle = %q, want it to contain version %q", got, version.Version)
	}
}

func TestUserAgent_FollowsConventionalFormat(t *testing.T) {
	got := UserAgent()
	want := "prompto/" + version.Version
	if got != want {
		t.Errorf("UserAgent = %q, want %q", got, want)
	}
	// Conventional User-Agent format is "<name>/<version>" — no
	// leading "v" on the version (that's reserved for X-Title).
	if strings.Contains(got, "/v") {
		t.Errorf("UserAgent = %q should not include leading 'v' on the version", got)
	}
}
