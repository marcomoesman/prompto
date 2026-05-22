package command

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CustomCommand is a user-defined command loaded from a Markdown file in
// .prompto/commands/. The file's basename (minus .md) is the command name;
// the file's contents become the prompt fed through agent.Run. The literal
// substring "$ARGS" is replaced with the user's argument string at exec
// time, so a single template can take parameters.
type CustomCommand struct {
	name   string
	help   string
	body   string
	source string // file path; surfaced in error messages only
}

// Name returns the canonical name.
func (c *CustomCommand) Name() string { return c.name }

// Aliases lists alternate names.
func (c *CustomCommand) Aliases() []string { return nil }

// Kind reports KindExpanding — custom commands always synthesize prompts.
func (c *CustomCommand) Kind() Kind { return KindExpanding }

// Help is the one-liner. Uses the file's first non-empty line when no
// frontmatter description exists; falls back to a generic label.
func (c *CustomCommand) Help() string { return c.help }

// Exec substitutes $ARGS with the user's argument string and returns the
// resulting prompt. Empty args still substitute (yielding "" in place).
func (c *CustomCommand) Exec(_ context.Context, args []string, _ Env) (Result, error) {
	body := strings.ReplaceAll(c.body, "$ARGS", strings.Join(args, " "))
	return Result{Prompt: body}, nil
}

// LoadCustomCommands walks dir for *.md files and returns a CustomCommand
// for each. Names that collide with built-ins (per reg.IsReserved) are
// dropped with a warning written to stderr; the rest are returned in
// stable filename order. A missing dir is treated as success (no custom
// commands).
func LoadCustomCommands(dir string, reg *Registry) ([]Command, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read commands dir: %w", err)
	}
	var out []Command
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".md")
		if name == entry.Name() {
			continue // not a .md file
		}
		if name == "" {
			continue
		}
		if reg != nil && reg.IsReserved(name) {
			fmt.Fprintf(os.Stderr, "warning: custom command %q shadows a built-in; ignored\n", name)
			continue
		}
		path := filepath.Join(dir, entry.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		out = append(out, newCustomCommand(name, path, string(body)))
	}
	return out, nil
}

// newCustomCommand builds a CustomCommand from a file's contents. The help
// line is the first non-empty trimmed line of the body, capped at 80 chars
// for the /help table.
func newCustomCommand(name, source, body string) *CustomCommand {
	help := "custom command"
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if len(trimmed) > 80 {
			trimmed = trimmed[:77] + "..."
		}
		help = trimmed
		break
	}
	return &CustomCommand{
		name:   name,
		help:   help,
		body:   body,
		source: source,
	}
}

// RegisterCustomCommands loads custom commands from dir and adds them to
// reg. Logs (to stderr) but doesn't error on individual file failures so
// a corrupt file doesn't take down startup. Returns an error only when
// the directory is unreadable for reasons other than not existing.
func RegisterCustomCommands(reg *Registry, dir string) error {
	cmds, err := LoadCustomCommands(dir, reg)
	if err != nil {
		return err
	}
	for _, c := range cmds {
		if err := reg.Register(c); err != nil {
			fmt.Fprintf(os.Stderr, "warning: registering custom command %q: %v\n", c.Name(), err)
		}
	}
	return nil
}
