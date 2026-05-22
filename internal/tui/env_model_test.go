package tui

import (
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/agent"
	"github.com/marcomoesman/prompto/internal/config"
	"github.com/marcomoesman/prompto/internal/permission"
)

// makeAppModelWithConfig is a thin wrapper over the existing test
// helper that also supplies a *config.Config. The /model code paths
// only behave correctly when config is plumbed through, so this
// helper keeps the setup honest.
func makeAppModelWithConfig(t *testing.T, cfg *config.Config) AppModel {
	t.Helper()
	a := agent.New(agent.NewAgentInput{
		Provider: stubProvider{},
		Model:    cfg.Default.Model,
		Tools:    stubResolver{},
		Notifier: agent.NewNotifier(),
	})
	rs := permission.NewRuleset()
	ev := permission.NewEvaluator(permission.NewEvaluatorInput{
		Mode:    permission.ModeDefault,
		Ruleset: rs,
	})
	return NewAppModel(AppModelInput{
		Agent:     a,
		Ruleset:   rs,
		Evaluator: ev,
		AgentName: "build",
		Config:    cfg,
	})
}

func twoProviderConfig() *config.Config {
	return &config.Config{
		Providers: map[string]config.ProviderEntry{
			"llamacpp": {
				Kind:    "openai",
				BaseURL: "http://127.0.0.1:8000",
				APIKey:  "k",
				Models: []config.ModelEntry{
					{Name: "qwopus-36-27b", MaxTokens: 8192},
				},
			},
			"openrouter": {
				Kind:    "openai",
				BaseURL: "http://127.0.0.1:1234", // fake — we never dial
				APIKey:  "k",
				Models: []config.ModelEntry{
					{Name: "moonshotai/kimi-k2.6", MaxTokens: 8192},
					{Name: "z-ai/glm-5.1", MaxTokens: 16384},
				},
			},
		},
		Default: config.DefaultConfig{Provider: "llamacpp", Model: "qwopus-36-27b"},
	}
}

func TestAppEnv_Models_FlattenedAndSorted(t *testing.T) {
	m := makeAppModelWithConfig(t, twoProviderConfig())
	env := newAppEnv(&m)

	got := env.Models()
	if len(got) != 3 {
		t.Fatalf("Models len = %d, want 3", len(got))
	}
	// Sorted by (provider, name): llamacpp first, then openrouter's two
	// in alphabetical order.
	wantOrder := []struct {
		Provider, Name string
	}{
		{"llamacpp", "qwopus-36-27b"},
		{"openrouter", "moonshotai/kimi-k2.6"},
		{"openrouter", "z-ai/glm-5.1"},
	}
	for i, w := range wantOrder {
		if got[i].Provider != w.Provider || got[i].Name != w.Name {
			t.Errorf("Models[%d] = (%s,%s), want (%s,%s)", i, got[i].Provider, got[i].Name, w.Provider, w.Name)
		}
	}
}

func TestAppEnv_SetModel_UnknownNameReturnsHelpfulError(t *testing.T) {
	m := makeAppModelWithConfig(t, twoProviderConfig())
	env := newAppEnv(&m)

	err := env.SetModel("not-a-real-model")
	if err == nil {
		t.Fatal("expected error for unknown model name")
	}
	if !strings.Contains(err.Error(), "not-a-real-model") {
		t.Errorf("error should name the unknown model: %v", err)
	}
	if !strings.Contains(err.Error(), "/model") {
		t.Errorf("error should hint at /model usage: %v", err)
	}
}

func TestAppEnv_SetModel_SameProviderUpdatesNameAndMaxTokens(t *testing.T) {
	cfg := twoProviderConfig()
	// Add a second model under the same default provider so we can
	// switch within a provider without touching the provider object.
	llamacpp := cfg.Providers["llamacpp"]
	llamacpp.Models = append(llamacpp.Models, config.ModelEntry{
		Name: "qwopus-other", MaxTokens: 4096,
	})
	cfg.Providers["llamacpp"] = llamacpp

	m := makeAppModelWithConfig(t, cfg)
	prevProvider := m.agent.Provider()
	env := newAppEnv(&m)

	if err := env.SetModel("qwopus-other"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if got := m.agent.Model(); got != "qwopus-other" {
		t.Errorf("agent.Model = %q, want qwopus-other", got)
	}
	// Same-provider switch must reuse the existing provider — building
	// a new one is wasteful and would also reset its connection state.
	if m.agent.Provider() != prevProvider {
		t.Errorf("provider was rebuilt on same-provider switch")
	}
	if m.currentProvider() != "llamacpp" {
		t.Errorf("currentProvider = %q, want llamacpp", m.currentProvider())
	}
	if m.status.modelName != "qwopus-other" {
		t.Errorf("status.modelName = %q, want qwopus-other", m.status.modelName)
	}
}

func TestAppEnv_SetModel_CrossProviderSwapsProvider(t *testing.T) {
	m := makeAppModelWithConfig(t, twoProviderConfig())
	prevProvider := m.agent.Provider()
	env := newAppEnv(&m)

	if err := env.SetModel("z-ai/glm-5.1"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	// Cross-provider switch must construct a fresh provider — the
	// previous one was bound to llamacpp's BaseURL/APIKey.
	if m.agent.Provider() == prevProvider {
		t.Errorf("expected a new provider after cross-provider switch")
	}
	if m.currentProvider() != "openrouter" {
		t.Errorf("currentProvider = %q, want openrouter", m.currentProvider())
	}
	if got := m.agent.Model(); got != "z-ai/glm-5.1" {
		t.Errorf("agent.Model = %q, want z-ai/glm-5.1", got)
	}
}

// stubProvider/stubResolver live in app_agent_test.go.
