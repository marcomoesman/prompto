package tool

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// ErrRobotsBlocked is returned when the per-host robots.txt forbids
// the agent from fetching the requested path. Sentinel so the tool
// surface can render a friendly message without inspecting strings.
var ErrRobotsBlocked = errors.New("webfetch: blocked by robots.txt")

// robotsTTL is how long a parsed /robots.txt stays cached before
// the next fetch attempts a refresh. One hour matches Google's
// default crawl-delay assumption.
const robotsTTL = 1 * time.Hour

// robotsRule is one allow/disallow directive scoped to a user-agent
// group. We match against `User-agent: *` only — narrower agent
// groups (e.g. `User-agent: prompto`) take precedence when present
// but are uncommon enough to defer.
type robotsRule struct {
	allow bool
	path  string
}

type robotsEntry struct {
	rules     []robotsRule
	expiresAt time.Time
}

// RobotsCache holds parsed `/robots.txt` per host with a 1-hour TTL.
// Fail-open: any error fetching or parsing the file leaves the cache
// empty and IsAllowed returns true.
//
// Concurrent IsAllowed calls for the same host coalesce through a
// singleflight group so a cold cache + N parallel webfetches issues
// exactly one /robots.txt GET — without coalescing, every concurrent
// caller would fire its own request before any of them got a chance
// to populate the cache.
type RobotsCache struct {
	mu     sync.Mutex
	cache  map[string]robotsEntry
	flight singleflight.Group
	// fetchFn lets tests stub out the HTTP fetch.
	fetchFn func(ctx context.Context, robotsURL string) (string, error)
}

// NewRobotsCache builds an empty cache. The HTTP fetcher uses a
// throwaway client with a short timeout — robots.txt fetches must
// not block real work.
func NewRobotsCache() *RobotsCache {
	return &RobotsCache{
		cache:   map[string]robotsEntry{},
		fetchFn: defaultRobotsFetch,
	}
}

// IsAllowed reports whether the given path may be fetched on host
// per the cached robots.txt. Fetches and parses on cache miss. Any
// error is treated as "allow" (fail-open). Concurrent callers for
// the same stale host share a single underlying HTTP request via
// singleflight.
func (c *RobotsCache) IsAllowed(ctx context.Context, scheme, host, path string) bool {
	if path == "" {
		path = "/"
	}
	c.mu.Lock()
	entry, ok := c.cache[host]
	stale := !ok || time.Now().After(entry.expiresAt)
	c.mu.Unlock()

	if stale {
		// Do.Do guarantees only one in-flight fetch per host. Followers
		// receive the leader's resolved entry directly, skipping the
		// HTTP round-trip. The returned `entry` value flows through
		// the closure so we don't need a second cache read after.
		v, _, _ := c.flight.Do(host, func() (any, error) {
			// Re-check the cache: a previous flight may have populated
			// it while we were queued. This collapses the followers'
			// work to a map read.
			c.mu.Lock()
			if e, ok := c.cache[host]; ok && time.Now().Before(e.expiresAt) {
				c.mu.Unlock()
				return e, nil
			}
			c.mu.Unlock()

			body, err := c.fetchFn(ctx, scheme+"://"+host+"/robots.txt")
			var fresh robotsEntry
			if err != nil {
				fresh = robotsEntry{rules: nil, expiresAt: time.Now().Add(robotsTTL)}
			} else {
				fresh = robotsEntry{rules: parseRobots(body), expiresAt: time.Now().Add(robotsTTL)}
			}
			c.mu.Lock()
			c.cache[host] = fresh
			c.mu.Unlock()
			return fresh, nil
		})
		entry = v.(robotsEntry)
	}

	return rulesAllow(entry.rules, path)
}

// rulesAllow walks the directives in declaration order, picking the
// most-specific match (longest path prefix). Disallow wins ties for
// safety. Empty rules → allow.
func rulesAllow(rules []robotsRule, target string) bool {
	var (
		bestLen    = -1
		bestAllow  = true
		bestExists = false
	)
	for _, r := range rules {
		if !strings.HasPrefix(target, r.path) {
			continue
		}
		if len(r.path) > bestLen || (len(r.path) == bestLen && !r.allow) {
			bestLen = len(r.path)
			bestAllow = r.allow
			bestExists = true
		}
	}
	if !bestExists {
		return true
	}
	return bestAllow
}

// parseRobots extracts the directives in the `User-agent: *` group.
// Other user-agent groups are ignored. Recognised keys: User-agent,
// Disallow, Allow. Comments (`#`) and blank lines are skipped.
func parseRobots(body string) []robotsRule {
	var (
		out     []robotsRule
		inGroup = false
		seenUA  = false
	)
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip inline comments.
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)

		switch key {
		case "user-agent":
			// A new User-agent line starts a new group. We follow `*`
			// only; subsequent groups for other agents reset inGroup
			// to false.
			if seenUA && !inGroup && val == "*" {
				// Adjacent UA lines stacking onto the same group.
				inGroup = true
			} else {
				inGroup = (val == "*")
			}
			seenUA = true
		case "disallow":
			if inGroup && val != "" {
				out = append(out, robotsRule{allow: false, path: val})
			}
		case "allow":
			if inGroup && val != "" {
				out = append(out, robotsRule{allow: true, path: val})
			}
		}
	}
	return out
}

// defaultRobotsFetch issues a one-shot GET with a 5-second timeout
// and returns the body text. Used by NewRobotsCache; tests inject a
// stub via fetchFn.
func defaultRobotsFetch(ctx context.Context, robotsURL string) (string, error) {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(c, "GET", robotsURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", chromeUserAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", &robotsHTTPError{status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

type robotsHTTPError struct{ status int }

func (e *robotsHTTPError) Error() string {
	return "robots.txt HTTP status " + http.StatusText(e.status)
}
