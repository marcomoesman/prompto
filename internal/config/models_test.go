package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestModelEntry_UnmarshalString(t *testing.T) {
	var m ModelEntry
	if err := json.Unmarshal([]byte(`"gpt-4o"`), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Name != "gpt-4o" {
		t.Errorf("Name = %q, want gpt-4o", m.Name)
	}
	if m.MaxParallel != 0 {
		t.Errorf("MaxParallel = %d, want 0 (inherit)", m.MaxParallel)
	}
}

func TestModelEntry_UnmarshalObject(t *testing.T) {
	var m ModelEntry
	if err := json.Unmarshal([]byte(`{"name":"qwen","max_parallel":2}`), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Name != "qwen" {
		t.Errorf("Name = %q", m.Name)
	}
	if m.MaxParallel != 2 {
		t.Errorf("MaxParallel = %d, want 2", m.MaxParallel)
	}
}

func TestModelEntry_UnmarshalMixedSlice(t *testing.T) {
	var entries []ModelEntry
	raw := []byte(`["gpt-4o", {"name":"qwen","max_parallel":1}]`)
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len = %d, want 2", len(entries))
	}
	if entries[0].Name != "gpt-4o" || entries[0].MaxParallel != 0 {
		t.Errorf("entry[0] = %+v", entries[0])
	}
	if entries[1].Name != "qwen" || entries[1].MaxParallel != 1 {
		t.Errorf("entry[1] = %+v", entries[1])
	}
}

func TestResolveModelLimits_ModelOverridesProvider(t *testing.T) {
	cfg := &Config{
		Providers: map[string]ProviderEntry{
			"ollama": {
				Kind:        "openai",
				MaxParallel: 1,
				Models: []ModelEntry{
					{Name: "qwen"},
					{Name: "fast", MaxParallel: 4},
				},
			},
		},
	}
	if got := ResolveModelLimits(cfg, "ollama", "fast"); got != 4 {
		t.Errorf("fast = %d, want 4 (model override)", got)
	}
	if got := ResolveModelLimits(cfg, "ollama", "qwen"); got != 1 {
		t.Errorf("qwen = %d, want 1 (provider default)", got)
	}
	if got := ResolveModelLimits(cfg, "ollama", "unknown-model"); got != 1 {
		t.Errorf("unknown = %d, want 1 (provider default)", got)
	}
}

func TestResolveModelLimits_DefaultUnbounded(t *testing.T) {
	cfg := &Config{
		Providers: map[string]ProviderEntry{
			"anthropic": {Kind: "anthropic"},
		},
	}
	if got := ResolveModelLimits(cfg, "anthropic", "claude"); got != UnboundedParallel {
		t.Errorf("anthropic.claude = %d, want %d", got, UnboundedParallel)
	}
}

func TestResolveModelLimits_UnknownProvider(t *testing.T) {
	cfg := &Config{Providers: map[string]ProviderEntry{}}
	if got := ResolveModelLimits(cfg, "missing", "x"); got != UnboundedParallel {
		t.Errorf("got %d, want %d", got, UnboundedParallel)
	}
}

func TestResolveModelLimits_NilConfig(t *testing.T) {
	if got := ResolveModelLimits(nil, "p", "m"); got != UnboundedParallel {
		t.Errorf("got %d, want %d", got, UnboundedParallel)
	}
}

func TestResolveMaxTokens(t *testing.T) {
	cfg := &Config{
		Providers: map[string]ProviderEntry{
			"openai": {
				Kind: "openai",
				Models: []ModelEntry{
					{Name: "gpt-4o", MaxTokens: 8192},
					{Name: "gpt-4o-mini", MaxTokens: 4096},
				},
			},
			"local": {Kind: "openai"},
		},
	}
	if got := ResolveMaxTokens(cfg, "openai", "gpt-4o"); got != 8192 {
		t.Errorf("gpt-4o = %d, want 8192", got)
	}
	if got := ResolveMaxTokens(cfg, "openai", "gpt-4o-mini"); got != 4096 {
		t.Errorf("gpt-4o-mini = %d, want 4096", got)
	}
	if got := ResolveMaxTokens(cfg, "openai", "unknown"); got != 0 {
		t.Errorf("unknown = %d, want 0", got)
	}
	if got := ResolveMaxTokens(cfg, "local", "anything"); got != 0 {
		t.Errorf("provider with no models = %d, want 0", got)
	}
	if got := ResolveMaxTokens(cfg, "missing", "anything"); got != 0 {
		t.Errorf("unknown provider = %d, want 0", got)
	}
	if got := ResolveMaxTokens(nil, "p", "m"); got != 0 {
		t.Errorf("nil cfg = %d, want 0", got)
	}
}

func TestResolveModelSampling_DefaultsWhenUnset(t *testing.T) {
	cfg := &Config{
		Providers: map[string]ProviderEntry{
			"openai": {Kind: "openai", Models: []ModelEntry{{Name: "qwen", MaxTokens: 8192}}},
		},
	}
	got := ResolveModelSampling(cfg, "openai", "qwen")
	if got.Temperature != DefaultTemperature || got.PresencePenalty != DefaultPresencePenalty {
		t.Fatalf("sampling = %+v, want defaults", got)
	}
	if got.TemperatureConfigured || got.PresencePenaltyConfigured {
		t.Fatalf("configured flags = %+v, want false", got)
	}
}

func TestSetAndResetModelSamplingPersistsConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Chdir(dir)
	projectDir := filepath.Join(dir, ".prompto")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(projectDir, "config.json")
	raw := []byte(`{
  "providers": {
    "openai": {
      "kind": "openai",
      "api_key": "$OPENAI_API_KEY",
      "models": [{"name": "qwen", "max_tokens": 8192}]
    }
  },
  "default": {"provider": "openai", "model": "qwen"}
}`)
	if err := os.WriteFile(configPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = SetModelSampling(cfg, "openai", "qwen", ModelSampling{
		Temperature:               0.6,
		PresencePenalty:           1.1,
		TemperatureConfigured:     true,
		PresencePenaltyConfigured: true,
	})
	if err != nil {
		t.Fatalf("SetModelSampling: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"temperature": 0.6`)) || !bytes.Contains(data, []byte(`"presence_penalty": 1.1`)) {
		t.Fatalf("config did not persist sampling fields:\n%s", data)
	}
	if bytes.Contains(data, []byte("expanded")) {
		t.Fatalf("config should not write expanded env values:\n%s", data)
	}
	if err := ResetModelSampling(cfg, "openai", "qwen"); err != nil {
		t.Fatalf("ResetModelSampling: %v", err)
	}
	data, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("temperature")) || bytes.Contains(data, []byte("presence_penalty")) {
		t.Fatalf("reset should remove sampling fields:\n%s", data)
	}
}

func TestSetModelSamplingPersistsOnlyConfiguredFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Chdir(dir)
	projectDir := filepath.Join(dir, ".prompto")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(projectDir, "config.json")
	raw := []byte(`{
  "providers": {
    "openai": {
      "kind": "openai",
      "api_key": "$OPENAI_API_KEY",
      "models": [{"name": "qwen", "max_tokens": 8192}]
    }
  },
  "default": {"provider": "openai", "model": "qwen"}
}`)
	if err := os.WriteFile(configPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = SetModelSampling(cfg, "openai", "qwen", ModelSampling{
		Temperature:           0.6,
		PresencePenalty:       DefaultPresencePenalty,
		TemperatureConfigured: true,
	})
	if err != nil {
		t.Fatalf("SetModelSampling: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"temperature": 0.6`)) {
		t.Fatalf("config did not persist configured temperature:\n%s", data)
	}
	if bytes.Contains(data, []byte("presence_penalty")) {
		t.Fatalf("config persisted unconfigured presence_penalty:\n%s", data)
	}
}

func TestFindModel(t *testing.T) {
	cfg := &Config{
		Providers: map[string]ProviderEntry{
			"zed":      {Kind: "openai", Models: []ModelEntry{{Name: "alpha", MaxTokens: 4096}}},
			"acme":     {Kind: "openai", Models: []ModelEntry{{Name: "beta", MaxTokens: 8192}}},
			"shared-a": {Kind: "openai", Models: []ModelEntry{{Name: "shared", MaxTokens: 100}}},
			"shared-z": {Kind: "openai", Models: []ModelEntry{{Name: "shared", MaxTokens: 999}}},
		},
	}

	if pname, m, ok := FindModel(cfg, "alpha"); !ok || pname != "zed" || m.MaxTokens != 4096 {
		t.Errorf("alpha: provider=%q max=%d ok=%v, want zed/4096/true", pname, m.MaxTokens, ok)
	}
	if pname, _, ok := FindModel(cfg, "beta"); !ok || pname != "acme" {
		t.Errorf("beta: provider=%q ok=%v, want acme/true", pname, ok)
	}
	if _, _, ok := FindModel(cfg, "ghost"); ok {
		t.Error("ghost: ok=true, want false (unknown model must return false)")
	}
	// Alphabetically-first provider wins on duplicate listings.
	if pname, m, ok := FindModel(cfg, "shared"); !ok || pname != "shared-a" || m.MaxTokens != 100 {
		t.Errorf("duplicate: provider=%q max=%d, want shared-a/100", pname, m.MaxTokens)
	}
	if _, _, ok := FindModel(nil, "x"); ok {
		t.Error("nil cfg: ok=true, want false")
	}
	if _, _, ok := FindModel(cfg, ""); ok {
		t.Error("empty name: ok=true, want false")
	}
}

func TestResolveModelLimits_StringSliceBackcompat(t *testing.T) {
	raw := []byte(`{
		"providers": {
			"openai": {
				"kind": "openai",
				"api_key": "k",
				"models": ["gpt-4o", "gpt-4o-mini"]
			}
		},
		"default": { "provider": "openai", "model": "gpt-4o" }
	}`)
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	prov := cfg.Providers["openai"]
	if len(prov.Models) != 2 {
		t.Fatalf("Models len = %d, want 2", len(prov.Models))
	}
	if prov.Models[0].Name != "gpt-4o" {
		t.Errorf("Models[0].Name = %q", prov.Models[0].Name)
	}
	if got := ResolveModelLimits(&cfg, "openai", "gpt-4o"); got != UnboundedParallel {
		t.Errorf("got %d, want unbounded for legacy string-slice config", got)
	}
}
