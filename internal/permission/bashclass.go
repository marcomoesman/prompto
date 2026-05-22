package permission

import "strings"

// BashClass categorises a bash command for plan-mode permission gating.
//
// The classifier is intentionally conservative: anything not on the
// explicit read-only or mutating allowlists falls through to
// BashClassUnknown so the existing `bash *: Ask` rule fires and the
// user makes the call. Plan-mode auto-allows ReadOnly and auto-denies
// Mutating; build-mode never installs a classifier so its bash
// behaviour is unchanged.
type BashClass int

const (
	BashClassUnknown BashClass = iota
	BashClassReadOnly
	BashClassMutating
)

// String returns a short human-readable form. Used in evaluator
// reasons surfaced to the TUI / audit log.
func (c BashClass) String() string {
	switch c {
	case BashClassReadOnly:
		return "read-only"
	case BashClassMutating:
		return "mutating"
	default:
		return "unknown"
	}
}

// ClassifyBash reports the class of a bash command string.
//
// Pipeline rule: any Mutating segment makes the whole command
// Mutating; any Unknown segment makes the whole command at most
// Unknown; only an all-ReadOnly pipeline is ReadOnly. An empty or
// whitespace-only command is Unknown.
//
// Subshells (`$(...)`, `` `...` ``) outside single quotes downgrade a
// would-be ReadOnly result to Unknown — the inner command isn't
// analysed, so we'd rather ask than risk silently allowing a hidden
// `rm`. Mutating remains Mutating regardless.
func ClassifyBash(command string) BashClass {
	tokens := tokenizeShellCommand(command)
	segments := splitPipelineSegments(tokens)
	result := combineSegmentClasses(segments)
	if result == BashClassReadOnly && containsSubshell(command) {
		return BashClassUnknown
	}
	return result
}

// containsSubshell reports whether the command contains an unquoted
// `$(` or backtick. Single quotes suppress the detection (literal);
// double quotes do not (subshells execute inside them). Backslash
// escapes outside quotes are honoured so `\$(...)` doesn't trip it.
func containsSubshell(s string) bool {
	inSingle, inDouble := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			continue
		}
		switch {
		case c == '\'' && !inDouble:
			inSingle = true
		case c == '"':
			inDouble = !inDouble
		case c == '\\' && !inDouble && i+1 < len(s):
			i++
		case c == '`':
			return true
		case c == '$' && i+1 < len(s) && s[i+1] == '(':
			return true
		}
	}
	return false
}

// tokenKind discriminates the three categories of token the bash
// classifier cares about.
type tokenKind int

const (
	tokenWord tokenKind = iota
	// tokenSeparator is one of `|`, `||`, `;`, `&&` — splits the
	// command into pipeline / sequential segments.
	tokenSeparator
	// tokenRedirection is one of `>`, `>>`, `<>`, `&>`, `>|` — only
	// recognised outside quotes; presence in a segment forces it
	// Mutating.
	tokenRedirection
)

type bashToken struct {
	kind tokenKind
	// op holds the operator literal for separator / redirection tokens;
	// word holds the unquoted concatenated text for tokenWord.
	op   string
	word string
}

// tokenizeShellCommand parses the command into a token stream
// respecting single quotes (literal), double quotes (with backslash
// escape on a small set of chars), and bare backslash escapes outside
// quotes. Operators are matched longest-first so `&&` doesn't read as
// two `&` tokens and `>>` doesn't read as two `>` tokens.
func tokenizeShellCommand(s string) []bashToken {
	var tokens []bashToken
	var cur strings.Builder
	inWord := false

	flush := func() {
		if inWord {
			tokens = append(tokens, bashToken{kind: tokenWord, word: cur.String()})
			cur.Reset()
			inWord = false
		}
	}

	i := 0
	for i < len(s) {
		c := s[i]

		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
			i++
			continue
		case c == '\'':
			// Single quotes: literal content, no escapes.
			inWord = true
			i++
			for i < len(s) && s[i] != '\'' {
				cur.WriteByte(s[i])
				i++
			}
			if i < len(s) {
				i++ // consume closing quote
			}
			continue
		case c == '"':
			// Double quotes: backslash escapes the next char.
			inWord = true
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					cur.WriteByte(s[i+1])
					i += 2
					continue
				}
				cur.WriteByte(s[i])
				i++
			}
			if i < len(s) {
				i++ // consume closing quote
			}
			continue
		case c == '\\' && i+1 < len(s):
			// Bare backslash escape outside quotes.
			inWord = true
			cur.WriteByte(s[i+1])
			i += 2
			continue
		}

		// Multi-char operators, longest match first.
		if op := matchShellOperator(s[i:]); op != "" {
			flush()
			kind := tokenSeparator
			if isShellRedirection(op) {
				kind = tokenRedirection
			}
			tokens = append(tokens, bashToken{kind: kind, op: op})
			i += len(op)
			continue
		}

		// Default: regular word character.
		inWord = true
		cur.WriteByte(c)
		i++
	}
	flush()
	return tokens
}

// shellOperators is ordered longest-first so that `&&` matches before
// `&`, `>>` before `>`, etc. `<` is intentionally absent: input
// redirection is read-only, and adding it as an operator would split
// `<<EOF` heredocs in confusing ways.
var shellOperators = []string{"&&", "||", ">>", "<>", "&>", ">|", "|", ";", ">"}

func matchShellOperator(s string) string {
	for _, op := range shellOperators {
		if strings.HasPrefix(s, op) {
			return op
		}
	}
	return ""
}

func isShellRedirection(op string) bool {
	switch op {
	case ">", ">>", "<>", "&>", ">|":
		return true
	}
	return false
}

// bashSegment is one stage of a pipeline / sequential chain.
type bashSegment struct {
	words          []string
	hasRedirection bool
}

// splitPipelineSegments groups word tokens between separator tokens,
// recording redirection presence per segment.
func splitPipelineSegments(tokens []bashToken) []bashSegment {
	var segments []bashSegment
	var cur bashSegment
	for _, t := range tokens {
		switch t.kind {
		case tokenWord:
			cur.words = append(cur.words, t.word)
		case tokenSeparator:
			segments = append(segments, cur)
			cur = bashSegment{}
		case tokenRedirection:
			cur.hasRedirection = true
		}
	}
	segments = append(segments, cur)
	// Drop fully-empty segments (leading/trailing/duplicate
	// separators) so they don't drag the whole command toward
	// Unknown when only padding is involved.
	filtered := segments[:0]
	for _, seg := range segments {
		if len(seg.words) == 0 && !seg.hasRedirection {
			continue
		}
		filtered = append(filtered, seg)
	}
	return filtered
}

// combineSegmentClasses applies the weakest-link pipeline rule.
func combineSegmentClasses(segments []bashSegment) BashClass {
	if len(segments) == 0 {
		return BashClassUnknown
	}
	worst := BashClassReadOnly
	for _, seg := range segments {
		c := classifySegment(seg)
		if c == BashClassMutating {
			return BashClassMutating
		}
		if c == BashClassUnknown {
			worst = BashClassUnknown
		}
	}
	return worst
}

// classifySegment classifies one pipeline stage.
//
// Order of checks matters:
//  1. Output redirection in this segment → Mutating.
//  2. `git` gets its own decision tree (some subcommands are read-only,
//     some are mutating, anything else is Unknown).
//  3. First-token mutating verbs (rm, mv, ...).
//  4. Package managers with mutating subcommands (npm install, ...).
//  5. `sed -i` / `gawk -i inplace` → Mutating.
//  6. Read-only allowlist.
//  7. Default → Unknown.
func classifySegment(seg bashSegment) BashClass {
	if seg.hasRedirection {
		return BashClassMutating
	}
	if len(seg.words) == 0 {
		return BashClassUnknown
	}
	head := seg.words[0]

	if head == "git" {
		return classifyGit(seg.words)
	}

	if mutatingVerbs[head] {
		return BashClassMutating
	}

	if c := classifyPackageManager(seg.words); c == BashClassMutating {
		return BashClassMutating
	}

	if (head == "sed" || head == "awk" || head == "gawk") && hasInPlaceFlag(seg.words) {
		return BashClassMutating
	}

	if readOnlyCommands[head] {
		return BashClassReadOnly
	}

	return BashClassUnknown
}

// mutatingVerbs are commands whose presence as the segment head makes
// the segment Mutating regardless of arguments. `tee` is included
// because its primary use writes to a file.
var mutatingVerbs = map[string]bool{
	"rm":       true,
	"mv":       true,
	"cp":       true,
	"chmod":    true,
	"chown":    true,
	"mkdir":    true,
	"rmdir":    true,
	"touch":    true,
	"ln":       true,
	"dd":       true,
	"tee":      true,
	"truncate": true,
	"install":  true,
}

// readOnlyCommands are first-tokens whose typical invocation only
// reads. `sed` and `awk` live here too but are intercepted earlier
// when an in-place flag is set.
var readOnlyCommands = map[string]bool{
	// File reads / search.
	"cat":   true,
	"head":  true,
	"tail":  true,
	"less":  true,
	"more":  true,
	"grep":  true,
	"egrep": true,
	"fgrep": true,
	"rg":    true,
	"ag":    true,
	"ack":   true,
	"find":  true,
	"fd":    true,
	"wc":    true,
	"file":  true,
	"stat":  true,
	"ls":    true,
	"tree":  true,
	// Path / metadata helpers.
	"dirname":  true,
	"basename": true,
	"realpath": true,
	"readlink": true,
	"pwd":      true,
	// System inspection.
	"whoami":   true,
	"hostname": true,
	"uname":    true,
	"which":    true,
	"whence":   true,
	"type":     true,
	"env":      true,
	"printenv": true,
	"echo":     true,
	"printf":   true,
	"date":     true,
	"df":       true,
	"du":       true,
	"ps":       true,
	"top":      true,
	"htop":     true,
	"free":     true,
	"uptime":   true,
	"id":       true,
	"groups":   true,
	// Stream filters that don't write by default.
	"awk":     true,
	"sed":     true,
	"cut":     true,
	"sort":    true,
	"uniq":    true,
	"tr":      true,
	"xxd":     true,
	"hexdump": true,
	"od":      true,
}

// classifyGit walks the small set of read-only / mutating subcommands
// the classifier knows. Anything not on either list — including bare
// `git` — falls through to Unknown so the user is asked.
func classifyGit(words []string) BashClass {
	if len(words) < 2 {
		return BashClassUnknown
	}
	sub := words[1]
	rest := words[2:]

	switch sub {
	case "status", "diff", "log", "show", "blame",
		"ls-files", "ls-tree", "rev-parse", "describe":
		return BashClassReadOnly
	case "branch", "tag":
		if hasFlag(rest, "-l") || hasFlag(rest, "--list") {
			return BashClassReadOnly
		}
	case "remote":
		// `git remote` alone lists; `git remote -v` lists with URLs.
		// Only the explicit -v form is read-only per the phase plan;
		// other forms (add/remove/set-url) mutate.
		if hasFlag(rest, "-v") {
			return BashClassReadOnly
		}
	case "config":
		if hasFlag(rest, "--get") || hasFlag(rest, "--list") {
			return BashClassReadOnly
		}
	case "stash":
		if len(rest) > 0 && rest[0] == "list" {
			return BashClassReadOnly
		}
	}

	switch sub {
	case "push", "commit", "reset", "checkout", "rebase", "merge",
		"cherry-pick", "tag", "branch", "stash", "add", "rm", "mv",
		"init", "clone", "fetch", "pull", "apply", "am", "revert":
		return BashClassMutating
	}

	return BashClassUnknown
}

// classifyPackageManager returns Mutating for the documented package
// manager invocations and Unknown otherwise. Returning Unknown means
// "no opinion" — caller continues other checks.
func classifyPackageManager(words []string) BashClass {
	if len(words) < 2 {
		return BashClassUnknown
	}
	head := words[0]
	sub := words[1]

	switch head {
	case "npm", "pnpm", "yarn", "bun":
		switch sub {
		case "install", "add", "update", "upgrade", "uninstall", "remove":
			return BashClassMutating
		}
	case "pip", "pip3":
		switch sub {
		case "install", "uninstall":
			return BashClassMutating
		}
	case "go":
		switch sub {
		case "install", "build", "run", "generate":
			return BashClassMutating
		case "mod":
			if len(words) >= 3 {
				switch words[2] {
				case "tidy", "download":
					return BashClassMutating
				}
			}
		}
	case "cargo":
		switch sub {
		case "install", "build", "run", "update":
			return BashClassMutating
		}
	}
	return BashClassUnknown
}

// hasInPlaceFlag returns true for `sed -i`, `sed --in-place`,
// `sed -i.bak ...`, and `gawk -i inplace ...`. The `awk` head also
// routes through here because some platforms symlink awk → gawk.
func hasInPlaceFlag(words []string) bool {
	if len(words) == 0 {
		return false
	}
	head := words[0]
	rest := words[1:]
	switch head {
	case "sed":
		for _, w := range rest {
			if w == "-i" || w == "--in-place" {
				return true
			}
			if strings.HasPrefix(w, "-i.") {
				return true
			}
		}
	case "awk", "gawk":
		for i, w := range rest {
			if w == "-i" && i+1 < len(rest) && rest[i+1] == "inplace" {
				return true
			}
		}
	}
	return false
}

func hasFlag(words []string, flag string) bool {
	for _, w := range words {
		if w == flag {
			return true
		}
	}
	return false
}
