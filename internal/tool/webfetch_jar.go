package tool

import (
	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
)

// newSessionJar builds the cookie jar bogdanfinn/tls-client expects.
// Lives for the lifetime of the WebFetchTool — i.e., one TUI session.
// The library's jar already understands the public-suffix list, so
// we no longer need golang.org/x/net/publicsuffix here.
func newSessionJar() http.CookieJar {
	return tls_client.NewCookieJar()
}
