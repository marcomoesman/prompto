package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBashToolEcho(t *testing.T) {
	bt := NewBashTool()
	input, _ := json.Marshal(BashInput{Command: "echo hello"})
	result, err := bt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.TrimSpace(result.Content) != "hello" {
		t.Errorf("result = %q", result.Content)
	}
}

func TestBashToolStderr(t *testing.T) {
	bt := NewBashTool()
	input, _ := json.Marshal(BashInput{Command: "echo out && echo err >&2"})
	result, err := bt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result.Content, "out") || !strings.Contains(result.Content, "err") {
		t.Errorf("result = %q, expected both stdout and stderr", result.Content)
	}
}

func TestBashToolNonZeroExit(t *testing.T) {
	bt := NewBashTool()
	input, _ := json.Marshal(BashInput{Command: "exit 42"})
	result, err := bt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if !strings.Contains(strings.ToLower(result.Content), "exit") {
		t.Errorf("result = %q, expected exit code info", result.Content)
	}
}

func TestBashToolTimeout(t *testing.T) {
	bt := NewBashTool()
	input, _ := json.Marshal(BashInput{Command: "sleep 10", Timeout: 1})
	result, err := bt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(strings.ToLower(result.Content), "timed out") {
		t.Errorf("result = %q, expected timeout message", result.Content)
	}
}

func TestResolveBashTimeout(t *testing.T) {
	tests := []struct {
		name      string
		requested int
		wantDur   time.Duration
		wantClamp bool
	}{
		{"zero uses default", 0, bashDefaultTimeout, false},
		{"negative uses default", -5, bashDefaultTimeout, false},
		{"small value preserved", 30, 30 * time.Second, false},
		{"at limit not clamped", int(bashMaxTimeout / time.Second), bashMaxTimeout, false},
		{"over limit clamped", int(bashMaxTimeout/time.Second) + 1, bashMaxTimeout, true},
		{"huge value clamped", 9999999, bashMaxTimeout, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotDur, gotClamp := resolveBashTimeout(tc.requested)
			if gotDur != tc.wantDur {
				t.Errorf("duration = %s, want %s", gotDur, tc.wantDur)
			}
			if gotClamp != tc.wantClamp {
				t.Errorf("clamped = %v, want %v", gotClamp, tc.wantClamp)
			}
		})
	}
}

func TestBashToolTimeoutClampedAnnotation(t *testing.T) {
	bt := NewBashTool()
	// echo returns immediately; we don't actually wait for the clamped
	// timeout. We just verify the annotation reaches the result.
	input, _ := json.Marshal(BashInput{Command: "echo hi", Timeout: 9999999})
	result, err := bt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result.Content, "timeout clamped") {
		t.Errorf("result missing clamp annotation: %q", result.Content)
	}
}

func TestBashToolEmptyCommand(t *testing.T) {
	bt := NewBashTool()
	input, _ := json.Marshal(BashInput{Command: ""})
	_, err := bt.Execute(t.Context(), newTestCtx(t), input)
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestBashToolContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	bt := NewBashTool()
	input, _ := json.Marshal(BashInput{Command: "sleep 30"})
	// Should not hang
	_, _ = bt.Execute(ctx, newTestCtx(t), input)
}

func TestBashToolHugeOutputIsCapped(t *testing.T) {
	bt := NewBashTool()
	// Pick a generator that matches the shell BashTool actually
	// resolved on this host. resolveShell() returns Git Bash on
	// Windows-with-Git, PowerShell otherwise — and the two have
	// completely different "spew N lines" idioms.
	cmd := "yes x | head -n 100000" // POSIX (Linux/macOS/Git Bash)
	if bt.shell.syntax == "powershell" {
		// PowerShell: emit 100k lines via range pipeline. Each
		// element renders as "x\r\n" (~3 bytes) so the total comes
		// out around ~300KB — well past the 50KB cap.
		cmd = "1..100000 | ForEach-Object { 'x' }"
	}
	input, _ := json.Marshal(BashInput{Command: cmd})
	result, err := bt.Execute(t.Context(), newTestCtx(t), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Content) > bashMaxOutput+100 {
		t.Fatalf("content length = %d, want capped near %d", len(result.Content), bashMaxOutput)
	}
	if !strings.Contains(result.Content, "[Output truncated at 50KB]") {
		t.Fatalf("result missing truncation marker")
	}
	if result.Bytes <= bashMaxOutput {
		t.Fatalf("Bytes = %d, want observed output beyond cap", result.Bytes)
	}
}

func TestBashToolDefinitionSchema(t *testing.T) {
	bt := NewBashTool()
	def := bt.Definition()
	if def.Name != "bash" {
		t.Errorf("Name = %q", def.Name)
	}
	var schema map[string]any
	if err := json.Unmarshal(def.InputSchema, &schema); err != nil {
		t.Fatalf("schema parse: %v", err)
	}
	props := schema["properties"].(map[string]any)
	if _, ok := props["command"]; !ok {
		t.Error("schema missing command property")
	}
}
