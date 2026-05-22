package tool

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrSSRFBlocked is returned when validateURLForFetch refuses a URL
// because its host resolves to a private/loopback/CGNAT/multicast
// address or matches a blocked hostname (localhost / cloud-metadata
// fronts). Sentinel so callers can branch.
var ErrSSRFBlocked = errors.New("webfetch: target rejected by SSRF guard")

// blockedHostnames is the literal-name reject list — these never get
// past the guard regardless of what they resolve to.
var blockedHostnames = []string{
	"localhost",
	"metadata.google.internal",
	"metadata.azure.com",
	"169.254.169.254",
}

// fetchAllowedHosts is the test-only escape hatch. Production callers
// pass nil; httptest-driven tests pass `[]string{"127.0.0.1"}` so the
// loopback fixtures keep working without weakening the guard.
type fetchAllowedHosts []string

// ssrfDNSLookupTimeout caps the DNS resolution performed inside
// validateURLForFetch. net.LookupIP doesn't accept a context, so a
// slow or hostile resolver could block the webfetch hot path
// indefinitely. Match the dialer's connect timeout (5s).
const ssrfDNSLookupTimeout = 5 * time.Second

// validateURLForFetch performs the cheap surface validation
// (scheme/host/length) and then enforces the SSRF guard:
//   - rejects loopback / RFC1918 / link-local / CGNAT / unique-local /
//     documentation / multicast addresses on every IP the host
//     resolves to;
//   - rejects literal localhost / *.localhost / cloud-metadata
//     hostnames;
//   - allows hosts in `allowed` to bypass the IP-class check (for
//     httptest fixtures only — production passes nil).
//
// This is half of a two-stage check. The DNS lookup performed here is
// inherently TOCTOU-prone — a hostile resolver can rebind to a private
// IP between this validation and the kernel's connect(). The second
// stage that closes that window is guardedDialer's ControlContext (see
// webfetch_ssrf.go ~L112): the Go dialer hands ControlContext the
// already-resolved IP:port immediately before connect(), and we run
// isUnsafeIP one more time. Both checks must remain in lockstep — this
// one rejects bad URLs early with clear errors before any network I/O,
// while ControlContext is the actual security boundary for DNS
// rebinding. Removing either one re-opens the gap.
func validateURLForFetch(ctx context.Context, rawURL string, allowed fetchAllowedHosts) error {
	if err := validateURL(rawURL); err != nil {
		return err
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return fmt.Errorf("url must include a host")
	}

	// Hostname-based rejects come before allowed-hosts: even if the
	// caller permits 127.0.0.1, "localhost" is still off-limits to
	// keep cookie jars + permission rules host-keyed.
	for _, blocked := range blockedHostnames {
		if host == blocked {
			return fmt.Errorf("%w: hostname %q is blocked", ErrSSRFBlocked, host)
		}
	}
	if strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("%w: hostname %q is blocked", ErrSSRFBlocked, host)
	}

	for _, h := range allowed {
		if strings.EqualFold(h, host) {
			return nil
		}
	}

	// If the host is already a literal IP, skip DNS — net.LookupIP
	// returns it back unchanged, but skipping avoids spurious DNS
	// against a real resolver.
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		lookupCtx, cancel := context.WithTimeout(ctx, ssrfDNSLookupTimeout)
		defer cancel()
		addrs, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
		if err != nil {
			return fmt.Errorf("%w: dns lookup of %q failed: %v", ErrSSRFBlocked, host, err)
		}
		ips = make([]net.IP, len(addrs))
		for i, a := range addrs {
			ips[i] = a.IP
		}
	}

	for _, ip := range ips {
		if isUnsafeIP(ip) {
			return fmt.Errorf("%w: %q resolves to %s which is in a blocked range", ErrSSRFBlocked, host, ip)
		}
	}
	return nil
}

func (t *WebFetchTool) guardedDialer() net.Dialer {
	return net.Dialer{
		Timeout: 5 * time.Second,
		ControlContext: func(_ context.Context, _, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				host = address
			}
			for _, allowed := range t.allowedHosts {
				if strings.EqualFold(allowed, host) {
					return nil
				}
			}
			ip := net.ParseIP(host)
			if ip == nil {
				// Go's default Dialer hands ControlContext the post-DNS
				// IP:port, so a non-IP host here means something
				// upstream skipped resolution (custom Resolver, future
				// refactor). Refuse rather than allow — the pre-connect
				// SSRF check assumes IP-form addresses.
				return fmt.Errorf("%w: dial target %q is not an IP after resolution", ErrSSRFBlocked, host)
			}
			if isUnsafeIP(ip) {
				return fmt.Errorf("%w: connection target %s is in a blocked range", ErrSSRFBlocked, ip)
			}
			return nil
		},
	}
}

// isUnsafeIP reports whether ip falls into one of the SSRF-unsafe
// classes: loopback, RFC1918 private, link-local (incl. AWS metadata
// 169.254.169.254), CGNAT (100.64.0.0/10), unique-local IPv6
// (fc00::/7), broadcast, multicast, documentation, or unspecified.
func isUnsafeIP(ip net.IP) bool {
	if ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() {
		return true
	}
	// CGNAT 100.64.0.0/10 — net.IP.IsPrivate covers RFC1918 but not
	// the carrier-grade NAT block, which can still front home routers.
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return true
		}
		// 0.0.0.0/8 (current network)
		if v4[0] == 0 {
			return true
		}
		// 192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24 (TEST-NETs)
		if (v4[0] == 192 && v4[1] == 0 && v4[2] == 2) ||
			(v4[0] == 198 && v4[1] == 51 && v4[2] == 100) ||
			(v4[0] == 203 && v4[1] == 0 && v4[2] == 113) {
			return true
		}
	}
	return false
}
