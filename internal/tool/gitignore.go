package tool

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	ignore "github.com/sabhiram/go-gitignore"
)

// LoadGitignore walks from dir up to the git root, collecting all .gitignore
// patterns, and returns a compiled matcher. Returns nil if no .gitignore
// files are found.
func LoadGitignore(dir string) *ignore.GitIgnore {
	root := findGitRoot(dir)
	if root == "" {
		root = dir
	}

	var patterns []string

	// Always ignore .git directory itself.
	patterns = append(patterns, ".git")

	// Walk from root down to dir collecting .gitignore files.
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		rel = "."
	}

	// Collect directories from root to dir.
	dirs := []string{root}
	if rel != "." {
		parts := strings.Split(rel, string(filepath.Separator))
		cur := root
		for _, p := range parts {
			cur = filepath.Join(cur, p)
			dirs = append(dirs, cur)
		}
	}

	for _, d := range dirs {
		gi := filepath.Join(d, ".gitignore")
		lines, err := readLines(gi)
		if err != nil {
			continue
		}
		// Make patterns relative to root by prefixing with the directory's
		// relative path from root.
		dirRel, err := filepath.Rel(root, d)
		if err != nil || dirRel == "." {
			patterns = append(patterns, lines...)
		} else {
			for _, line := range lines {
				if strings.HasPrefix(line, "!") {
					// Negation pattern — prefix the pattern after "!".
					patterns = append(patterns, "!"+dirRel+"/"+line[1:])
				} else {
					patterns = append(patterns, dirRel+"/"+line)
				}
			}
		}
	}

	if len(patterns) <= 1 {
		// Only the hardcoded ".git" pattern, no real gitignore files found.
		return ignore.CompileIgnoreLines(patterns...)
	}

	return ignore.CompileIgnoreLines(patterns...)
}

// IsGitIgnored checks whether a path (relative to the git root) is ignored.
// Returns false if matcher is nil.
func IsGitIgnored(matcher *ignore.GitIgnore, relPath string) bool {
	if matcher == nil {
		return false
	}
	return matcher.MatchesPath(relPath)
}

// findGitRoot walks up from dir looking for a .git directory.
// Returns the directory containing .git, or empty string if not found.
func findGitRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if info, err := os.Stat(filepath.Join(abs, ".git")); err == nil && info.IsDir() {
			return abs
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return ""
		}
		abs = parent
	}
}

// readLines reads non-empty, non-comment lines from a file.
func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return lines, scanner.Err()
}
