package agent

import (
	"strings"
	"testing"
)

func TestRedactSecrets_AuthHeader(t *testing.T) {
	in := "401 Unauthorized\nAuthorization: Bearer sk-abc123def456ghi789jkl\n"
	got := RedactSecrets(in)
	if strings.Contains(got, "sk-abc123def456ghi789jkl") {
		t.Errorf("token leaked: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("expected [REDACTED] marker: %q", got)
	}
	if !strings.Contains(strings.ToLower(got), "authorization") {
		t.Errorf("header name should be preserved for diagnosis: %q", got)
	}
}

func TestRedactSecrets_APIKeyHeader(t *testing.T) {
	in := "x-api-key: sk-ant-api03-realkeyvalue123"
	got := RedactSecrets(in)
	if strings.Contains(got, "sk-ant-api03-realkeyvalue123") {
		t.Errorf("token leaked: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("expected [REDACTED] marker: %q", got)
	}
}

func TestRedactSecrets_BareToken(t *testing.T) {
	in := "request failed with sk-ant-api03-xyzABC0123456789longenough in body"
	got := RedactSecrets(in)
	if strings.Contains(got, "sk-ant-api03-xyzABC0123456789longenough") {
		t.Errorf("bare token leaked: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("expected [REDACTED] marker: %q", got)
	}
}

func TestRedactSecrets_NoMatch(t *testing.T) {
	in := "500 internal server error\nupstream timeout after 30s"
	got := RedactSecrets(in)
	if got != in {
		t.Errorf("unchanged input mutated: %q -> %q", in, got)
	}
}

func TestRedactSecrets_CaseInsensitive(t *testing.T) {
	in := "AUTHORIZATION: BEARER sk-ABCdef0123456789xyzqr\n"
	got := RedactSecrets(in)
	if strings.Contains(got, "sk-ABCdef0123456789xyzqr") {
		t.Errorf("token leaked under uppercase header: %q", got)
	}
}
