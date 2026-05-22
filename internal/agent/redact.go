package agent

import "regexp"

var (
	reAuthHeader = regexp.MustCompile(`(?i)(authorization\s*:\s*bearer\s+)[A-Za-z0-9._\-]+`)
	reAPIKeyHdr  = regexp.MustCompile(`(?i)(x-api-key\s*:\s*)[A-Za-z0-9._\-]+`)
	reBareToken  = regexp.MustCompile(`sk-(ant-)?[A-Za-z0-9_\-]{20,}`)
)

// RedactSecrets removes obvious credential substrings from s. Best-effort —
// preserves enough structure for diagnosis (header names, surrounding error
// context) but strips the secret payload. Applied to provider error
// messages before they reach the TUI or the request log: most providers
// don't echo Authorization / x-api-key in error bodies, but local /
// self-hosted endpoints (LiteLLM proxy, Ollama-fronted setups) sometimes
// do, and the result lands in a chat scrollback the user might paste
// into a bug report.
func RedactSecrets(s string) string {
	s = reAuthHeader.ReplaceAllString(s, "${1}[REDACTED]")
	s = reAPIKeyHdr.ReplaceAllString(s, "${1}[REDACTED]")
	s = reBareToken.ReplaceAllString(s, "[REDACTED]")
	return s
}
