package permission

// protectedGlobs is the hardcoded list of paths that the Evaluator denies
// writes to unconditionally, regardless of any user rule. Reads fall
// through to the ruleset — the user may explicitly grant a read rule for
// a .env file if they want the model to summarize it.
//
// This list is not user-configurable by design. If a workflow genuinely
// needs to write to a file on this list, the user does it with a plain
// text editor outside prompto.
var protectedGlobs = []string{
	"**/.env",
	"**/.env.*",
	"**/.git/config",
	"**/.git/hooks/**",
	"**/.ssh/**",
	"**/.aws/credentials",
	"**/.mcp.json",
	"**/id_rsa",
	"**/id_dsa",
	"**/id_ecdsa",
	"**/id_ed25519",
	"**/*.pem",
	"**/*.p12",
}

// IsProtected reports whether the given path matches any hardcoded
// protected glob. The input must already be normalized via NormalizePath so
// the match runs against a canonical absolute path.
func IsProtected(path string) bool {
	for _, pattern := range protectedGlobs {
		if MatchGlob(pattern, path) {
			return true
		}
	}
	return false
}

// readProtectedGlobs is a stricter subset that also blocks read-only
// tools (read, grep, glob). protectedGlobs above only fires on writes,
// which leaves a permissive `allow read **` rule free to expose
// unambiguous credentials to the model. There is no legitimate "model
// summarises my private key" workflow, so we force-deny reads of the
// hard-credential set.
//
// .env and .git/* are deliberately omitted: those have legitimate read
// use cases (model inspecting .env.example for setup, listing MCP
// configs, checking hook contents), and the user can already deny them
// via ruleset if they want.
var readProtectedGlobs = []string{
	"**/.ssh/**",
	"**/.aws/credentials",
	"**/id_rsa",
	"**/id_dsa",
	"**/id_ecdsa",
	"**/id_ed25519",
	"**/*.pem",
	"**/*.p12",
}

// IsReadProtected reports whether the given path matches any read-side
// hard-credential glob. Used by the Evaluator to deny read-only tools
// against these paths regardless of any allow rule.
func IsReadProtected(path string) bool {
	for _, pattern := range readProtectedGlobs {
		if MatchGlob(pattern, path) {
			return true
		}
	}
	return false
}
