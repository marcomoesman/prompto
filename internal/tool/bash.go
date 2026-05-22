package tool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
)

const (
	bashDefaultTimeout = 120 * time.Second
	// bashMaxTimeout caps any LLM-supplied timeout. The per-call
	// permission gate shows the model's chosen value to the user, but
	// defence-in-depth: a reflexively-approved `timeout: 99999999`
	// should not pin a goroutine for hours. 30m comfortably covers
	// legitimate long-running commands (full test suite with -count=10,
	// large builds) while bounding worst-case impact.
	bashMaxTimeout = 30 * time.Minute
	bashMaxOutput  = 50 * 1024 // 50KB output truncation
)

// BashInput defines the JSON parameters for the bash tool.
type BashInput struct {
	Command string `json:"command" jsonschema:"required,description=The shell command to execute"`
	Timeout int    `json:"timeout,omitzero" jsonschema:"description=Timeout in seconds. Defaults to 120."`
}

// BashTool executes shell commands and returns their output. The shell
// is resolved once at construction time so the description and Execute
// both reference the same interpreter — without that coupling the
// system prompt could advertise PowerShell syntax while the runtime
// dispatched to bash, or vice versa.
type BashTool struct {
	definition api.ToolDefinition
	shell      shellInfo
}

// shellInfo captures the resolved shell: the absolute path to the
// interpreter binary, the flag(s) preceding the command, and a
// human-friendly tag (`bash` or `powershell`) the description uses
// to tell the model which syntax applies.
type shellInfo struct {
	cmd    string
	args   []string
	syntax string // "bash" | "powershell"
	label  string // human-readable, e.g. "Git Bash" / "PowerShell 7" — surfaced in description
}

// NewBashTool creates a BashTool with its pre-computed schema. The
// description embeds the resolved shell so the model is reminded — at
// the tool-call site, not just in the environment header — which
// shell it is actually talking to and which syntax / path conventions
// apply.
func NewBashTool() *BashTool {
	sh := resolveShell()
	return &BashTool{
		shell: sh,
		definition: api.ToolDefinition{
			Name:        "bash",
			Description: bashDescription(sh),
			InputSchema: GenerateSchema(BashInput{}),
		},
	}
}

// resolveShell picks the interpreter at construction time. On Linux
// and macOS that's plain bash. On Windows we prefer Git Bash (Git for
// Windows ships POSIX coreutils alongside it, so the model's bash
// muscle-memory works) and fall back to PowerShell 7 (`pwsh`) or
// Windows PowerShell 5.1 (`powershell`) when Git Bash is absent.
//
// The Windows preference order matches Claude Code's documented
// behaviour: "Git Bash if present, PowerShell otherwise."
func resolveShell() shellInfo {
	if runtime.GOOS == "windows" {
		if bashPath, ok := findGitBash(); ok {
			return shellInfo{
				cmd:    bashPath,
				args:   []string{"-c"},
				syntax: "bash",
				label:  "Git Bash",
			}
		}
		shell := "powershell"
		label := "Windows PowerShell"
		if _, err := exec.LookPath("pwsh"); err == nil {
			shell = "pwsh"
			label = "PowerShell 7"
		}
		return shellInfo{
			cmd:    shell,
			args:   []string{"-NoProfile", "-NonInteractive", "-Command"},
			syntax: "powershell",
			label:  label,
		}
	}
	return shellInfo{cmd: "bash", args: []string{"-c"}, syntax: "bash", label: "bash"}
}

// findGitBash locates Git Bash's bash.exe on Windows, or returns
// false. We deliberately do NOT use `LookPath("bash")` because that
// can resolve to WSL bash (`C:\Windows\System32\bash.exe`), which
// runs in a Linux filesystem with its own PATH and won't see the
// host's `go.exe` / project files. Git Bash specifically ships at
// `<gitInstall>/bin/bash.exe` and is the contract Git for Windows
// guarantees.
//
// Resolution order:
//  1. Standard install paths in ProgramFiles / ProgramFiles(x86) /
//     LocalAppData (covers per-machine and per-user installs).
//  2. Sibling of `git.exe` on PATH: `<gitInstall>/cmd/git.exe` →
//     `<gitInstall>/bin/bash.exe`. Catches non-default install
//     locations and portable installs.
func findGitBash() (string, bool) {
	candidates := []string{}
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		candidates = append(candidates, filepath.Join(pf, "Git", "bin", "bash.exe"))
	}
	if pf86 := os.Getenv("ProgramFiles(x86)"); pf86 != "" {
		candidates = append(candidates, filepath.Join(pf86, "Git", "bin", "bash.exe"))
	}
	if lad := os.Getenv("LocalAppData"); lad != "" {
		candidates = append(candidates, filepath.Join(lad, "Programs", "Git", "bin", "bash.exe"))
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	if gitPath, err := exec.LookPath("git"); err == nil {
		// `<gitInstall>/cmd/git.exe` is the standard layout; sibling
		// `bin/bash.exe` is Git Bash. filepath.Dir twice walks
		// `cmd` -> `<gitInstall>`.
		gitDir := filepath.Dir(gitPath)
		bash := filepath.Join(filepath.Dir(gitDir), "bin", "bash.exe")
		if _, err := os.Stat(bash); err == nil {
			return bash, true
		}
	}
	return "", false
}

// bashDescription renders the tool description with shell-specific
// guidance baked in. The model gets a sharp signal at call time about
// which syntax / path conventions apply — important because the
// resolved shell varies per host (bash on Linux/macOS, Git Bash on
// Windows-with-Git, PowerShell on Windows-without-Git).
func bashDescription(sh shellInfo) string {
	const prefix = "Execute a shell command and return its output (stdout and stderr combined). " +
		"The command runs in the project working directory with a default timeout of 120 seconds. "

	switch {
	case runtime.GOOS == "windows" && sh.syntax == "bash":
		return prefix +
			"Platform: windows (using " + sh.label + "). Commands run under bash with POSIX syntax — " +
			"`ls`, `cat`, `grep`, `head`, `find`, `$VAR`, forward-slash paths. Windows drives are " +
			"mounted at `/c/`, `/g/`, etc., so a path like `G:\\Go Workspace\\prompto` becomes " +
			"`/g/Go\\ Workspace/prompto` (or use forward slashes inside double-quotes: `\"G:/Go Workspace/prompto\"`)."
	case runtime.GOOS == "windows":
		return prefix +
			"Platform: windows (using " + sh.label + "). Commands run under PowerShell — use Windows-native " +
			"paths (e.g. `G:\\path\\to\\file`, no escaping; quoting follows PowerShell rules) and PowerShell " +
			"syntax (`Get-ChildItem`, `Remove-Item`, `Select-String`, `Select-Object -First N`, `$env:VAR`). " +
			"Do NOT emit POSIX-shell forms like `cat`, `head`, `tail`, `grep`, `awk`, `sed`, `find`, `ls`, " +
			"`wc`, `cut`, `sort`, `uniq`, `cmd /c ...`, or `bash -c` — those are not built-ins under PowerShell."
	default:
		return prefix +
			"On " + runtime.GOOS + " commands run under bash (POSIX syntax, forward-slash paths, `$VAR`)."
	}
}

func (t *BashTool) Name() string                   { return "bash" }
func (t *BashTool) Definition() api.ToolDefinition { return t.definition }

// MaxResultBytes returns 0 to opt into the central default (50 KB). Bash's
// internal bashMaxOutput constant is a separate memory-safety ceiling on the
// buffer; Result.Bytes continues to report pre-ceiling size.
func (t *BashTool) MaxResultBytes() int { return 0 }

// Bash has arbitrary side effects; never auto-anything.
func (t *BashTool) IsReadOnly() bool        { return false }
func (t *BashTool) IsConcurrencySafe() bool { return false }

// PermissionKey returns the raw command so rules can target specific
// commands like "git status" or "go test *" via globs.
func (t *BashTool) PermissionKey(input []byte) string {
	params, err := unmarshalInput[BashInput](input)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(params.Command)
}

// FormatForDisplay renders the bash call header. Unlike other tools we
// deliberately bypass FormatCall's 80-char QuoteArg cap and emit the
// full command — bash is shell-execution, and the user must be able to
// read every byte they're approving.
func (t *BashTool) FormatForDisplay(input []byte) string {
	params, err := unmarshalInput[BashInput](input)
	if err != nil {
		return "Bash(?)"
	}
	return "Bash(command: " + QuoteArg(params.Command, NoTruncate) + ")"
}

// Execute runs params.Command through the resolved shell with `-c`.
//
// Security model: the model-supplied command string is passed verbatim
// to the shell, so anything the user-controlled model emits is
// arbitrary code execution by design — that is what a `bash` tool is.
// The boundary is NOT the validateCommand heuristic below; it is the
// per-call permission gate wired in cmd/prompto/main.go (canUseTool →
// TUI approval prompt), which the agent loop consults BEFORE this
// Execute method ever runs. validateCommand only catches the most
// obvious destructive patterns to surface friction; bypassing it via
// variable substitution, command substitution, or long-form flags
// still hits the user-approval gate first.
func (t *BashTool) Execute(ctx context.Context, tc agent.ToolContext, input []byte) (agent.Result, error) {
	params, err := unmarshalInput[BashInput](input)
	if err != nil {
		return agent.Result{}, err
	}

	if strings.TrimSpace(params.Command) == "" {
		return agent.Result{}, fmt.Errorf("command is required")
	}

	if err := validateCommand(params.Command); err != nil {
		return agent.Result{}, err
	}

	timeout, clamped := resolveBashTimeout(params.Timeout)

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := append(append([]string{}, t.shell.args...), params.Command)
	cmd := exec.CommandContext(cmdCtx, t.shell.cmd, args...)
	if tc.Cwd != "" {
		cmd.Dir = tc.Cwd
	} else {
		cmd.Dir, _ = os.Getwd()
	}

	output := newLimitedOutputBuffer(bashMaxOutput)
	cmd.Stdout = output
	cmd.Stderr = output

	start := time.Now()
	err = cmd.Run()
	dur := time.Since(start)

	result := output.String()
	fullBytes := output.Observed()

	if output.Truncated() {
		result = result + "\n[Output truncated at 50KB]"
	}
	if clamped {
		result = result + fmt.Sprintf("\n[Note: requested timeout clamped to %s]", formatDur(bashMaxTimeout))
	}

	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			s := fmt.Sprintf("%s\n[Command timed out after %s]", result, timeout)
			return agent.Result{
				Content:        s,
				Bytes:          fullBytes,
				DisplaySummary: fmt.Sprintf("timed out after %s", formatDur(timeout)),
			}, nil
		}
		// Non-zero exit — include in output, not as Go error.
		exitCode := -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		s := fmt.Sprintf("%s\n[Exit code: %s]", result, err.Error())
		return agent.Result{
			Content:        s,
			Bytes:          fullBytes,
			DisplaySummary: fmt.Sprintf("exit %d · %s", exitCode, formatDur(dur)),
		}, nil
	}

	return agent.Result{
		Content:        result,
		Bytes:          fullBytes,
		DisplaySummary: fmt.Sprintf("exit 0 · %s", formatDur(dur)),
	}, nil
}

// resolveBashTimeout maps the LLM-supplied timeout (seconds, 0 means "use
// default") to the effective duration applied to the command. Values
// above bashMaxTimeout are clamped; the second return reports whether
// clamping fired so the caller can annotate the result for the model.
func resolveBashTimeout(requestedSeconds int) (time.Duration, bool) {
	if requestedSeconds <= 0 {
		return bashDefaultTimeout, false
	}
	requested := time.Duration(requestedSeconds) * time.Second
	if requested > bashMaxTimeout {
		return bashMaxTimeout, true
	}
	return requested, false
}

// formatDur is a compact duration renderer for tool summary lines:
// sub-second values render as "850ms"; >=1s renders as "12.3s"; >=60s
// switches to "1m23s".
func formatDur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	mins := int(d / time.Minute)
	secs := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%dm%ds", mins, secs)
}
