package command

import (
	"context"
	"strings"
	"testing"
)

func TestRegisterBuiltins_AllResolveWithoutDuplicates(t *testing.T) {
	r := NewRegistry()
	if err := RegisterBuiltins(r); err != nil {
		t.Fatalf("RegisterBuiltins: %v", err)
	}

	want := []string{
		"help", "quit", "clear", "new", "compact", "context",
		"resume", "sessions", "model", "mode", "agent", "plan",
		"build", "todo", "undo", "license", "init", "review",
	}
	for _, n := range want {
		if _, ok := r.Resolve(n); !ok {
			t.Errorf("Resolve(%q) = false; want true", n)
		}
	}
}

func TestLicenseCommand_ReturnsNotices(t *testing.T) {
	res, err := NewLicenseCommand().Exec(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !res.MessageMarkdown {
		t.Fatalf("MessageMarkdown = false; want true")
	}
	for _, want := range []string{
		"License: Apache-2.0",
		"github.com/bogdanfinn/tls-client",
		"**BSD-4-Clause**",
		"This product includes software developed by Bogdan Finn.",
	} {
		if !strings.Contains(res.Message, want) {
			t.Fatalf("license output missing %q", want)
		}
	}
}

func TestHelpCommand_SetsOpenHelpFlag(t *testing.T) {
	res, err := NewHelpCommand().Exec(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !res.OpenHelp {
		t.Errorf("OpenHelp = false; want true")
	}
}

func TestQuitCommand_SetsQuitFlag(t *testing.T) {
	res, err := NewQuitCommand().Exec(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !res.Quit {
		t.Errorf("Quit = false; want true")
	}
}

func TestInitCommand_KindExpanding(t *testing.T) {
	c := NewInitCommand()
	if c.Kind() != KindExpanding {
		t.Errorf("Kind = %v; want KindExpanding", c.Kind())
	}
	res, err := c.Exec(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.Prompt == "" {
		t.Errorf("Prompt is empty; expected synthesized text")
	}
}

func TestReviewCommand_TargetedAndDefault(t *testing.T) {
	c := NewReviewCommand()
	def, _ := c.Exec(context.Background(), nil, nil)
	if def.Prompt == "" {
		t.Errorf("default Prompt empty")
	}
	tgt, _ := c.Exec(context.Background(), []string{"main..feat"}, nil)
	if tgt.Prompt == def.Prompt {
		t.Errorf("targeted Prompt should differ from default")
	}
}
