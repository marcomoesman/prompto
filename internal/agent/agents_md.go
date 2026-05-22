package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Maximum bytes of project-instruction content injected into the system
// prompt. A runaway AGENTS.md shouldn't blow the context budget on its
// own — and the per-tool-result aggregator caps tool output downstream.
const agentsMDMaxBytes = 64 * 1024

// LoadInstructionsInput bundles the inputs to LoadProjectInstructions.
// Declared before the function it serves per CLAUDE.md.
type LoadInstructionsInput struct {
	// Cwd is the directory the agent is running in.
	Cwd string
	// Filenames is the ordered list of instruction filenames to look for at
	// each walk-up step. Defaults to []string{"AGENTS.md"} when nil/empty.
	Filenames []string
	// HomeDir overrides the user's home directory for global-fallback lookup.
	// Empty means use os.UserHomeDir. Used by tests.
	HomeDir string
}

// LoadProjectInstructions walks from Cwd toward the filesystem root (stopping
// at the first .git boundary it finds, exclusive) and collects every
// instruction file it encounters. A global fallback at $HomeDir/.prompto/
// is appended last. Content is ordered deepest-first then global, so more
// specific instructions appear later in the prompt — the model attends to
// later content more strongly.
//
// Returns an empty string when nothing is found. Total content is capped at
// agentsMDMaxBytes.
func LoadProjectInstructions(in LoadInstructionsInput) (string, error) {
	filenames := in.Filenames
	if len(filenames) == 0 {
		filenames = []string{"AGENTS.md"}
	}

	cwd, err := filepath.Abs(in.Cwd)
	if err != nil {
		return "", fmt.Errorf("resolving cwd: %w", err)
	}

	// Collect (path, content) pairs walking up, root-first so deepest appears
	// last when we reverse-concatenate at the end.
	type entry struct {
		path    string
		content []byte
	}
	var found []entry

	dir := cwd
	for {
		for _, name := range filenames {
			p := filepath.Join(dir, name)
			data, err := os.ReadFile(p)
			if err == nil {
				found = append(found, entry{path: p, content: data})
			} else if !os.IsNotExist(err) {
				return "", fmt.Errorf("reading %s: %w", p, err)
			}
		}

		// Stop at .git boundary (but include instruction files at the .git
		// level itself — we checked above before breaking).
		if hasGitDir(dir) {
			break
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root.
			break
		}
		dir = parent
	}

	// Global fallback.
	homeDir := in.HomeDir
	if homeDir == "" {
		homeDir, _ = os.UserHomeDir()
	}
	if homeDir != "" {
		for _, name := range filenames {
			p := filepath.Join(homeDir, ".prompto", name)
			data, err := os.ReadFile(p)
			if err == nil {
				found = append(found, entry{path: p, content: data})
			} else if !os.IsNotExist(err) {
				return "", fmt.Errorf("reading %s: %w", p, err)
			}
		}
	}

	if len(found) == 0 {
		return "", nil
	}

	// Deepest first (end of the slice), then global (after that). The slice is
	// currently ordered cwd-first, walking-up, then global. cwd IS the
	// deepest, so reverse only the walk portion.
	//
	// Actually, ordering goal: most specific LAST (model attention). cwd is
	// most specific, root is least specific, global is broadest. So the order
	// we want is: global, root, ..., cwd. That's the reverse of the walk
	// order. Global lives at the end of `found` currently; pull it out, then
	// reverse the rest, then stitch global at the front.
	//
	// Simpler: just concatenate `found` in reverse order (except for the
	// global entries which were appended last — reversing naturally puts
	// them first, which is what we want).
	for i, j := 0, len(found)-1; i < j; i, j = i+1, j-1 {
		found[i], found[j] = found[j], found[i]
	}

	var b strings.Builder
	var total int
	truncated := false
	for _, e := range found {
		header := fmt.Sprintf("Instructions from %s:\n\n", e.path)
		chunk := header + string(e.content) + "\n\n"
		if total+len(chunk) > agentsMDMaxBytes {
			remaining := agentsMDMaxBytes - total
			if remaining > 0 {
				b.WriteString(chunk[:remaining])
			}
			truncated = true
			break
		}
		b.WriteString(chunk)
		total += len(chunk)
	}
	if truncated {
		b.WriteString("\n[Instructions truncated at 64KB.]\n")
	}

	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

func hasGitDir(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && info.IsDir()
}

// LoadedInstruction is one nested AGENTS.md surfaced by the lazy walk-up.
// Path is the absolute path of the file; Content is the raw bytes.
type LoadedInstruction struct {
	Path    string
	Content string
}

// LoadAgentsMDForFile walks up from the directory containing filePath and
// collects every AGENTS.md found, stopping when it reaches loadRoot
// (exclusive) or a .git boundary. Used by the read tool to surface
// nested instructions the eager pass missed.
//
// filePath and loadRoot are both resolved to absolute paths; if filePath
// is not under loadRoot the walk does not run (returns nil, nil).
// Returns entries deepest-last so the model attends to the most specific
// last when they are stitched into reminders.
//
// Errors: only IO errors other than "not exist" surface; missing
// AGENTS.md at any level is normal.
func LoadAgentsMDForFile(filePath, loadRoot string) ([]LoadedInstruction, error) {
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("resolving file path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(absFile); err == nil {
		absFile = resolved
	} else if dirResolved, derr := filepath.EvalSymlinks(filepath.Dir(absFile)); derr == nil {
		absFile = filepath.Join(dirResolved, filepath.Base(absFile))
	}
	absRoot, err := filepath.Abs(loadRoot)
	if err != nil {
		return nil, fmt.Errorf("resolving load root: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = resolved
	}

	// Only walk when the file lives under the load root. Reading a file
	// outside the project (e.g. /etc/hosts) must not surface AGENTS.md
	// from anywhere above it.
	rel, err := filepath.Rel(absRoot, absFile)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil, nil
	}

	dir := filepath.Dir(absFile)
	var found []LoadedInstruction
	for dir != absRoot && dir != "/" && dir != "." {
		p := filepath.Join(dir, "AGENTS.md")
		data, err := os.ReadFile(p)
		switch {
		case err == nil:
			found = append(found, LoadedInstruction{Path: p, Content: string(data)})
		case !os.IsNotExist(err):
			return nil, fmt.Errorf("reading %s: %w", p, err)
		}
		// Stop at .git boundary — but NOT at the load root's .git, which we
		// already exited via the loop condition above.
		if hasGitDir(dir) {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	// Reverse so the deepest entry is last (matches LoadProjectInstructions
	// ordering — the model attends most strongly to the final reminder).
	for i, j := 0, len(found)-1; i < j; i, j = i+1, j-1 {
		found[i], found[j] = found[j], found[i]
	}
	return found, nil
}
