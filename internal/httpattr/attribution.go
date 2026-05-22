// Package httpattr supplies the prompto-identifying headers attached
// to every outbound LLM API request: HTTP-Referer + X-Title (for
// OpenRouter-compatible attribution and rate-limit benefits) and a
// User-Agent that names the tool and version.
//
// Webfetch deliberately does NOT use this package — it impersonates a
// real Chrome browser via tls_client to defeat anti-bot fingerprinting,
// and prompto-branded headers would defeat that intent.
package httpattr

import (
	"net/http"

	"github.com/marcomoesman/prompto/internal/version"
)

// RefererURL is the canonical project URL sent as HTTP-Referer.
// OpenRouter uses this for app attribution and unlocks higher rate
// limits when it points at a real, reachable URL. Other providers
// either log it harmlessly or ignore it entirely.
const RefererURL = "https://github.com/marcomoesman/prompto"

// XTitle returns the X-Title header value — a human-readable app
// identifier including the running version. Computed at call time so
// a Version bump propagates without const recomputation.
func XTitle() string { return "prompto/v" + version.Version }

// UserAgent returns the User-Agent header value in the conventional
// "<name>/<version>" form ("prompto/0.1.0"). Overrides Go's default
// "Go-http-client/1.1", which is uninformative for telemetry.
func UserAgent() string { return "prompto/" + version.Version }

// Apply sets the prompto attribution headers on req:
//
//   - HTTP-Referer: project URL (RefererURL)
//   - X-Title:      "prompto/v<version>" (XTitle)
//   - User-Agent:   "prompto/<version>" (UserAgent)
//
// Idempotent: existing values are overwritten. Call after any
// authentication / content-type setup so an upstream caller's User-
// Agent override doesn't slip through.
func Apply(req *http.Request) {
	req.Header.Set("HTTP-Referer", RefererURL)
	req.Header.Set("X-Title", XTitle())
	req.Header.Set("User-Agent", UserAgent())
}
