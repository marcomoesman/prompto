package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// WorkspaceSummary is a compact, deterministic snapshot of high-confidence
// project facts detected from root manifests only.
type WorkspaceSummary struct {
	Runtime        string
	PackageManager string
	Entry          string
	Build          string
	Test           string
}

// VerificationHint lists exact commands prompto can recommend after edits.
type VerificationHint struct {
	Commands []string
}

func (s WorkspaceSummary) Present() bool {
	return s.Runtime != "" || s.PackageManager != "" || s.Entry != "" || s.Build != "" || s.Test != ""
}

func (h VerificationHint) Present() bool {
	return len(h.Commands) > 0
}

func (s WorkspaceSummary) PromptText() string {
	var parts []string
	if s.Runtime != "" {
		parts = append(parts, "runtime: "+s.Runtime)
	}
	if s.PackageManager != "" {
		parts = append(parts, "package manager: "+s.PackageManager)
	}
	if s.Entry != "" {
		parts = append(parts, "entry: "+s.Entry)
	}
	if s.Build != "" {
		parts = append(parts, "build: `"+s.Build+"`")
	}
	if s.Test != "" {
		parts = append(parts, "test: `"+s.Test+"`")
	}
	if len(parts) == 0 {
		return ""
	}
	return "# Workspace summary\n" + strings.Join(parts, "; ")
}

func (h VerificationHint) PromptText() string {
	if len(h.Commands) == 0 {
		return ""
	}
	var quoted []string
	for _, cmd := range h.Commands {
		quoted = append(quoted, "`"+cmd+"`")
	}
	return "# Verification\nRecommended command(s): " + strings.Join(quoted, ", ")
}

// DetectWorkspace reads only root-level manifests and returns a short summary.
func DetectWorkspace(cwd string) WorkspaceSummary {
	if exists(filepath.Join(cwd, "go.mod")) {
		return WorkspaceSummary{
			Runtime: "Go",
			Entry:   detectGoEntry(cwd),
			Build:   "go build ./...",
			Test:    "go test ./...",
		}
	}
	if pkg, ok := readPackageJSON(cwd); ok {
		pm := detectNodePackageManager(cwd)
		return WorkspaceSummary{
			Runtime:        "Node",
			PackageManager: pm,
			Entry:          firstNonEmpty(pkg.Main, scriptCommand(pkg.Scripts, "start"), scriptCommand(pkg.Scripts, "dev")),
			Build:          nodeScriptCommand(pm, pkg.Scripts, "build"),
			Test:           nodeScriptCommand(pm, pkg.Scripts, "test"),
		}
	}
	if exists(filepath.Join(cwd, "pyproject.toml")) || exists(filepath.Join(cwd, "pytest.ini")) ||
		exists(filepath.Join(cwd, "requirements.txt")) || exists(filepath.Join(cwd, "manage.py")) {
		hint := DetectVerification(cwd)
		return WorkspaceSummary{
			Runtime: "Python",
			Entry:   pythonEntry(cwd),
			Test:    firstCommand(hint.Commands),
		}
	}
	if exists(filepath.Join(cwd, "Cargo.toml")) {
		return WorkspaceSummary{
			Runtime: "Rust",
			Build:   "cargo build",
			Test:    "cargo test",
		}
	}
	return WorkspaceSummary{}
}

// DetectVerification returns high-confidence verification commands for cwd.
func DetectVerification(cwd string) VerificationHint {
	if exists(filepath.Join(cwd, "go.mod")) {
		return VerificationHint{Commands: []string{"go test ./...", "go vet ./..."}}
	}
	if pkg, ok := readPackageJSON(cwd); ok {
		pm := detectNodePackageManager(cwd)
		var cmds []string
		if cmd := nodeScriptCommand(pm, pkg.Scripts, "test"); cmd != "" {
			cmds = append(cmds, cmd)
		}
		if cmd := nodeScriptCommand(pm, pkg.Scripts, "build"); cmd != "" {
			cmds = append(cmds, cmd)
		}
		return VerificationHint{Commands: cmds}
	}
	if exists(filepath.Join(cwd, "Cargo.toml")) {
		return VerificationHint{Commands: []string{"cargo test"}}
	}
	if exists(filepath.Join(cwd, "manage.py")) {
		return VerificationHint{Commands: []string{"python manage.py test"}}
	}
	if exists(filepath.Join(cwd, "pytest.ini")) || pyprojectMentionsPytest(cwd) || requirementsMentionPytest(cwd) {
		return VerificationHint{Commands: []string{"python -m pytest"}}
	}
	return VerificationHint{}
}

type packageJSON struct {
	Main    string            `json:"main"`
	Scripts map[string]string `json:"scripts"`
}

func readPackageJSON(cwd string) (packageJSON, bool) {
	data, err := os.ReadFile(filepath.Join(cwd, "package.json"))
	if err != nil {
		return packageJSON{}, false
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return packageJSON{}, false
	}
	return pkg, true
}

func detectGoEntry(cwd string) string {
	if exists(filepath.Join(cwd, "main.go")) {
		return "main.go"
	}
	entries, err := os.ReadDir(filepath.Join(cwd, "cmd"))
	if err != nil {
		return ""
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, filepath.Join("cmd", e.Name()))
		}
	}
	slices.Sort(names)
	if len(names) == 0 {
		return "cmd/"
	}
	return names[0]
}

func detectNodePackageManager(cwd string) string {
	switch {
	case exists(filepath.Join(cwd, "pnpm-lock.yaml")):
		return "pnpm"
	case exists(filepath.Join(cwd, "yarn.lock")):
		return "yarn"
	case exists(filepath.Join(cwd, "bun.lockb")), exists(filepath.Join(cwd, "bun.lock")):
		return "bun"
	default:
		return "npm"
	}
}

func nodeScriptCommand(pm string, scripts map[string]string, name string) string {
	if strings.TrimSpace(scripts[name]) == "" {
		return ""
	}
	switch pm {
	case "yarn":
		return "yarn " + name
	case "pnpm":
		return "pnpm " + name
	case "bun":
		return "bun run " + name
	default:
		if name == "test" {
			return "npm test"
		}
		return "npm run " + name
	}
}

func scriptCommand(scripts map[string]string, name string) string {
	return strings.TrimSpace(scripts[name])
}

func pythonEntry(cwd string) string {
	if exists(filepath.Join(cwd, "manage.py")) {
		return "manage.py"
	}
	return ""
}

func pyprojectMentionsPytest(cwd string) bool {
	data, err := os.ReadFile(filepath.Join(cwd, "pyproject.toml"))
	if err != nil {
		return false
	}
	body := strings.ToLower(string(data))
	return strings.Contains(body, "pytest")
}

func requirementsMentionPytest(cwd string) bool {
	data, err := os.ReadFile(filepath.Join(cwd, "requirements.txt"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.ToLower(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "pytest" || strings.HasPrefix(line, "pytest==") || strings.HasPrefix(line, "pytest>") || strings.HasPrefix(line, "pytest<") {
			return true
		}
	}
	return false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstCommand(cmds []string) string {
	if len(cmds) == 0 {
		return ""
	}
	return cmds[0]
}
