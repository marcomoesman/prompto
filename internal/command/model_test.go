package command

import (
	"context"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/api"
	"github.com/marcomoesman/prompto/internal/compact"
	"github.com/marcomoesman/prompto/internal/permission"
	"github.com/marcomoesman/prompto/internal/store"
)

// stubEnv satisfies the full command.Env interface with zero values.
// Tests embed it and override only the methods relevant to the
// command under test — keeps each test focused without 22 lines of
// shim methods per case.
type stubEnv struct {
	model    string
	models   []ModelInfo
	setErr   error
	setModel string // captured argument from the most recent SetModel call
}

func (stubEnv) AppendSystemMessage(string)                 {}
func (stubEnv) EndCurrentSession(context.Context) error    { return nil }
func (stubEnv) StartNewSession(context.Context) error      { return nil }
func (stubEnv) AdoptSession(context.Context, string) error { return nil }
func (stubEnv) SessionID() string                          { return "" }
func (stubEnv) AgentName() string                          { return "" }
func (e stubEnv) Model() string                            { return e.model }
func (stubEnv) Cwd() string                                { return "" }
func (stubEnv) Version() string                            { return "" }
func (stubEnv) Conversation() *agent.Conversation          { return nil }
func (stubEnv) SystemPromptText() string                   { return "" }
func (stubEnv) AGENTSMdText() string                       { return "" }
func (stubEnv) Store() *store.Store                        { return nil }
func (stubEnv) Compactor() *compact.Compactor              { return nil }
func (stubEnv) Evaluator() *permission.Evaluator           { return nil }
func (stubEnv) Agent() *agent.Agent                        { return nil }
func (stubEnv) Registry() *agent.AgentRegistry             { return nil }
func (stubEnv) Notifier() agent.RemindNotifier             { return nil }
func (stubEnv) ToolDefinitions() []api.ToolDefinition      { return nil }
func (stubEnv) SwitchAgent(string) error                   { return nil }
func (e *stubEnv) SetModel(name string) error {
	e.setModel = name
	return e.setErr
}
func (e stubEnv) Models() []ModelInfo { return e.models }

func TestRenderModelList_NoConfigShowsBareCurrent(t *testing.T) {
	got := renderModelList("qwopus-36-27b", nil)
	if got != "model: qwopus-36-27b" {
		t.Errorf("got %q, want bare current-model line", got)
	}
}

func TestRenderModelList_MarksCurrentAndShowsAll(t *testing.T) {
	models := []ModelInfo{
		{Name: "qwopus-36-27b", Provider: "llamacpp", MaxTokens: 8192},
		{Name: "moonshotai/kimi-k2.6", Provider: "openrouter", MaxTokens: 8192},
		{Name: "z-ai/glm-5.1", Provider: "openrouter", MaxTokens: 8192},
	}
	got := renderModelList("qwopus-36-27b", models)

	if !strings.HasPrefix(got, "model: qwopus-36-27b\n\navailable:\n") {
		t.Errorf("missing header in output: %q", got)
	}
	// Active model gets the marker; others get plain indent.
	if !strings.Contains(got, "▸ qwopus-36-27b") {
		t.Errorf("active model not marked with ▸: %q", got)
	}
	for _, m := range []string{"moonshotai/kimi-k2.6", "z-ai/glm-5.1"} {
		if !strings.Contains(got, m) {
			t.Errorf("missing model %q in list: %q", m, got)
		}
		// Non-active models must not get the marker.
		if strings.Contains(got, "▸ "+m) {
			t.Errorf("non-active model %q wrongly marked: %q", m, got)
		}
	}
	// Provider + max_tokens annotation per row.
	if !strings.Contains(got, "(llamacpp, max_tokens 8192)") {
		t.Errorf("missing provider/max_tokens annotation: %q", got)
	}
	if !strings.Contains(got, "(openrouter, max_tokens 8192)") {
		t.Errorf("missing openrouter annotation: %q", got)
	}
	// Trailing usage hint so the user knows how to switch.
	if !strings.HasSuffix(strings.TrimRight(got, "\n"), "switch with: /model <name>") {
		t.Errorf("missing usage hint at end: %q", got)
	}
}

func TestModelCommand_NoArgsOpensPickerWhenConfigured(t *testing.T) {
	env := &stubEnv{
		model: "qwopus-36-27b",
		models: []ModelInfo{
			{Name: "qwopus-36-27b", Provider: "llamacpp", MaxTokens: 8192},
			{Name: "z-ai/glm-5.1", Provider: "openrouter", MaxTokens: 8192},
		},
	}
	res, err := ModelCommand{}.Exec(context.Background(), nil, env)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !res.OpenModelPicker {
		t.Errorf("expected OpenModelPicker=true when models are configured, got %+v", res)
	}
	if res.Message != "" {
		t.Errorf("picker path should not emit a chat message; got %q", res.Message)
	}
	if env.setModel != "" {
		t.Errorf("Exec should not call SetModel when no args, but got %q", env.setModel)
	}
}

// TestModelCommand_NoArgsFallsBackToTextWhenNoModelsConfigured covers
// the fallback path: AppModels constructed without a config (e.g.,
// some unit tests) have an empty Models() list, in which case
// /model still emits its legacy text summary instead of opening a
// picker that would have nothing to show.
func TestModelCommand_NoArgsFallsBackToTextWhenNoModelsConfigured(t *testing.T) {
	env := &stubEnv{model: "qwopus-36-27b"} // models slice is nil
	res, err := ModelCommand{}.Exec(context.Background(), nil, env)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.OpenModelPicker {
		t.Errorf("no-config path should not open the picker (nothing to pick)")
	}
	if !strings.Contains(res.Message, "model: qwopus-36-27b") {
		t.Errorf("expected fallback text listing with current model: %q", res.Message)
	}
}

func TestModelCommand_ArgInvokesSetModel(t *testing.T) {
	env := &stubEnv{model: "qwopus-36-27b"}
	res, err := ModelCommand{}.Exec(context.Background(), []string{"z-ai/glm-5.1"}, env)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if env.setModel != "z-ai/glm-5.1" {
		t.Errorf("SetModel got %q, want %q", env.setModel, "z-ai/glm-5.1")
	}
	if res.Message != "model: z-ai/glm-5.1" {
		t.Errorf("Result.Message = %q", res.Message)
	}
}

func TestModelCommand_SetModelErrorPropagates(t *testing.T) {
	env := &stubEnv{model: "qwopus-36-27b", setErr: errStubUnknown}
	_, err := ModelCommand{}.Exec(context.Background(), []string{"unknown-model"}, env)
	if err == nil {
		t.Fatal("expected error from SetModel to surface")
	}
	if !strings.Contains(err.Error(), "set model") {
		t.Errorf("error should be wrapped with 'set model:' prefix; got %v", err)
	}
}

// errStubUnknown is a sentinel so the test file doesn't reach for fmt.Errorf
// in two places. Inline-declared so it isn't accidentally exported.
var errStubUnknown = stubError("unknown-model is not configured")

type stubError string

func (e stubError) Error() string { return string(e) }
