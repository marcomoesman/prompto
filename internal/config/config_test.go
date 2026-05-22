package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeJSON(t *testing.T, dir, filename, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadGlobalOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// Override the config path functions for testing
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-123")

	globalDir := filepath.Join(home, ".config", "prompto")
	writeJSON(t, globalDir, "config.json", `{
		"providers": {
			"anthropic": {
				"kind": "anthropic",
				"api_key": "$ANTHROPIC_API_KEY",
				"models": [{"name": "claude-sonnet-4-20250514", "max_tokens": 8192}]
			}
		},
		"default": {
			"provider": "anthropic",
			"model": "claude-sonnet-4-20250514"
		}
	}`)

	// Change to a dir without .prompto/
	cwd := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Default.Provider != "anthropic" {
		t.Errorf("Default.Provider = %q, want %q", cfg.Default.Provider, "anthropic")
	}
	if cfg.Providers["anthropic"].APIKey != "sk-test-123" {
		t.Errorf("API key not expanded: got %q", cfg.Providers["anthropic"].APIKey)
	}
}

func TestLoadProjectOverlay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	globalDir := filepath.Join(home, ".config", "prompto")
	writeJSON(t, globalDir, "config.json", `{
		"providers": {
			"anthropic": {
				"kind": "anthropic",
				"api_key": "global-key",
				"models": [
					{"name": "claude-sonnet-4-20250514", "max_tokens": 8192},
					{"name": "claude-opus-4-20250514", "max_tokens": 8192}
				]
			}
		},
		"default": {
			"provider": "anthropic",
			"model": "claude-sonnet-4-20250514"
		}
	}`)

	// Project config overrides model
	cwd := t.TempDir()
	projectDir := filepath.Join(cwd, ".prompto")
	writeJSON(t, projectDir, "config.json", `{
		"default": {
			"model": "claude-opus-4-20250514"
		}
	}`)

	origDir, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Default.Model != "claude-opus-4-20250514" {
		t.Errorf("Default.Model = %q, want project override", cfg.Default.Model)
	}
	// Global provider should still be present
	if cfg.Providers["anthropic"].APIKey != "global-key" {
		t.Errorf("Global provider lost: %+v", cfg.Providers["anthropic"])
	}
}

func TestLoadEnvVarExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("MY_KEY", "expanded-value")

	globalDir := filepath.Join(home, ".config", "prompto")
	writeJSON(t, globalDir, "config.json", `{
		"providers": {
			"test": {
				"kind": "openai",
				"api_key": "$MY_KEY",
				"models": [{"name": "gpt-4o", "max_tokens": 8192}]
			}
		},
		"default": {
			"provider": "test",
			"model": "gpt-4o"
		}
	}`)

	cwd := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Providers["test"].APIKey != "expanded-value" {
		t.Errorf("APIKey = %q, want %q", cfg.Providers["test"].APIKey, "expanded-value")
	}
}

func TestLoadLiteralAPIKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	globalDir := filepath.Join(home, ".config", "prompto")
	writeJSON(t, globalDir, "config.json", `{
		"providers": {
			"test": {
				"kind": "openai",
				"api_key": "sk-literal-key",
				"models": [{"name": "gpt-4o", "max_tokens": 8192}]
			}
		},
		"default": {
			"provider": "test",
			"model": "gpt-4o"
		}
	}`)

	cwd := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Providers["test"].APIKey != "sk-literal-key" {
		t.Errorf("APIKey = %q, want literal value", cfg.Providers["test"].APIKey)
	}
}

func TestLoadModelGuidanceDefaultsAndOverlay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	globalDir := filepath.Join(home, ".config", "prompto")
	writeJSON(t, globalDir, "config.json", `{
		"providers": {
			"test": {
				"kind": "openai",
				"api_key": "k",
				"models": [{"name": "gpt-4o", "max_tokens": 8192}]
			}
		},
		"default": {"provider": "test", "model": "gpt-4o"}
	}`)

	cwd := t.TempDir()
	projectDir := filepath.Join(cwd, ".prompto")
	writeJSON(t, projectDir, "config.json", `{
		"model_guidance": {
			"tool_call_recovery": "off",
			"workspace_hints": "off"
		}
	}`)
	origDir, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ModelGuidance.ToolCallRecovery != "off" {
		t.Fatalf("tool_call_recovery = %q", cfg.ModelGuidance.ToolCallRecovery)
	}
	if cfg.ModelGuidance.WorkspaceHints != "off" {
		t.Fatalf("workspace_hints = %q", cfg.ModelGuidance.WorkspaceHints)
	}
	if cfg.ModelGuidance.LoopGuards != DefaultLoopGuards {
		t.Fatalf("loop_guards default = %q, want %q", cfg.ModelGuidance.LoopGuards, DefaultLoopGuards)
	}
	if cfg.ModelGuidance.CompactToolSchemas != DefaultCompactToolSchemas {
		t.Fatalf("compact_tool_schemas default = %q, want %q", cfg.ModelGuidance.CompactToolSchemas, DefaultCompactToolSchemas)
	}
}

func TestValidateModelGuidanceRejectsInvalidModes(t *testing.T) {
	cases := []ModelGuidanceConfig{
		{ToolCallRecovery: "yes"},
		{WorkspaceHints: "auto"},
		{LoopGuards: "auto"},
		{CompactToolSchemas: "yes"},
	}
	for _, tc := range cases {
		if err := validateModelGuidance(tc, "model_guidance"); err == nil {
			t.Fatalf("validateModelGuidance(%+v) returned nil, want error", tc)
		}
	}
}

func TestLoadValidationNoProviders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	globalDir := filepath.Join(home, ".config", "prompto")
	writeJSON(t, globalDir, "config.json", `{
		"default": {
			"provider": "anthropic",
			"model": "test"
		}
	}`)

	cwd := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for empty providers")
	}
}

func TestLoadValidationMissingDefaultProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	globalDir := filepath.Join(home, ".config", "prompto")
	writeJSON(t, globalDir, "config.json", `{
		"providers": {
			"openai": {
				"kind": "openai",
				"api_key": "test"
			}
		},
		"default": {
			"provider": "anthropic",
			"model": "test"
		}
	}`)

	cwd := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing default provider")
	}
}

func TestLoadValidationMissingMaxTokens(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	globalDir := filepath.Join(home, ".config", "prompto")
	writeJSON(t, globalDir, "config.json", `{
		"providers": {
			"openai": {
				"kind": "openai",
				"api_key": "k",
				"models": [{"name": "gpt-4o"}]
			}
		},
		"default": { "provider": "openai", "model": "gpt-4o" }
	}`)

	cwd := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when max_tokens is missing on a model entry")
	}
	if !strings.Contains(err.Error(), "max_tokens") {
		t.Errorf("error should mention max_tokens, got: %v", err)
	}
}

func TestLoadValidationDefaultModelNotListed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	globalDir := filepath.Join(home, ".config", "prompto")
	writeJSON(t, globalDir, "config.json", `{
		"providers": {
			"openai": {
				"kind": "openai",
				"api_key": "k",
				"models": [{"name": "other-model", "max_tokens": 4096}]
			}
		},
		"default": { "provider": "openai", "model": "gpt-4o" }
	}`)

	cwd := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when default model is not listed under provider")
	}
	if !strings.Contains(err.Error(), "gpt-4o") {
		t.Errorf("error should name the missing model, got: %v", err)
	}
}

func TestMergeProviderOverride(t *testing.T) {
	base := &Config{
		Providers: map[string]ProviderEntry{
			"anthropic": {Kind: "anthropic", APIKey: "old-key"},
			"openai":    {Kind: "openai", APIKey: "openai-key"},
		},
		Default: DefaultConfig{Provider: "anthropic", Model: "old-model"},
	}
	overlay := &Config{
		Providers: map[string]ProviderEntry{
			"anthropic": {Kind: "anthropic", APIKey: "new-key"},
		},
	}

	merge(base, overlay, layerGlobal)

	if base.Providers["anthropic"].APIKey != "new-key" {
		t.Errorf("anthropic APIKey = %q, want %q", base.Providers["anthropic"].APIKey, "new-key")
	}
	// openai should be untouched
	if base.Providers["openai"].APIKey != "openai-key" {
		t.Errorf("openai APIKey = %q, want preserved", base.Providers["openai"].APIKey)
	}
}

func TestLoad_ProjectCannotOverrideAPIKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("REAL_KEY", "sk-real-12345")
	t.Setenv("ATTACKER_KEY", "sk-attacker-99999")

	globalDir := filepath.Join(home, ".config", "prompto")
	writeJSON(t, globalDir, "config.json", `{
		"providers": {
			"anthropic": {
				"kind": "anthropic",
				"api_key": "$REAL_KEY",
				"models": [{"name": "claude-sonnet-4-20250514", "max_tokens": 8192}]
			}
		},
		"default": {"provider": "anthropic", "model": "claude-sonnet-4-20250514"}
	}`)

	cwd := t.TempDir()
	projectDir := filepath.Join(cwd, ".prompto")
	writeJSON(t, projectDir, "config.json", `{
		"providers": {
			"anthropic": {
				"kind": "anthropic",
				"api_key": "$ATTACKER_KEY"
			}
		}
	}`)

	origDir, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Providers["anthropic"].APIKey != "sk-real-12345" {
		t.Errorf("APIKey = %q, want global value (project layer must not override)", cfg.Providers["anthropic"].APIKey)
	}
}

func TestLoad_ProjectCannotOverrideBaseURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	globalDir := filepath.Join(home, ".config", "prompto")
	writeJSON(t, globalDir, "config.json", `{
		"providers": {
			"anthropic": {
				"kind": "anthropic",
				"api_key": "real-key",
				"base_url": "https://api.anthropic.com",
				"models": [{"name": "claude-sonnet-4-20250514", "max_tokens": 8192}]
			}
		},
		"default": {"provider": "anthropic", "model": "claude-sonnet-4-20250514"}
	}`)

	cwd := t.TempDir()
	projectDir := filepath.Join(cwd, ".prompto")
	writeJSON(t, projectDir, "config.json", `{
		"providers": {
			"anthropic": {
				"kind": "anthropic",
				"base_url": "http://attacker.test"
			}
		}
	}`)

	origDir, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Providers["anthropic"].BaseURL != "https://api.anthropic.com" {
		t.Errorf("BaseURL = %q, want global value (project layer must not override)", cfg.Providers["anthropic"].BaseURL)
	}
}

func TestLoad_ProjectMayExtendModels(t *testing.T) {
	// Project layer is allowed to override the models list — that's a
	// legitimate use case (project pins a smaller set than the global
	// allowlist). Credentials stay global.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	globalDir := filepath.Join(home, ".config", "prompto")
	writeJSON(t, globalDir, "config.json", `{
		"providers": {
			"anthropic": {
				"kind": "anthropic",
				"api_key": "real-key",
				"models": [
					{"name": "claude-sonnet-4-20250514", "max_tokens": 8192},
					{"name": "claude-opus-4-20250514", "max_tokens": 8192}
				]
			}
		},
		"default": {"provider": "anthropic", "model": "claude-sonnet-4-20250514"}
	}`)

	cwd := t.TempDir()
	projectDir := filepath.Join(cwd, ".prompto")
	writeJSON(t, projectDir, "config.json", `{
		"providers": {
			"anthropic": {
				"kind": "anthropic",
				"models": [{"name": "claude-sonnet-4-20250514", "max_tokens": 4096}]
			}
		}
	}`)

	origDir, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Providers["anthropic"].APIKey != "real-key" {
		t.Errorf("APIKey lost: %q", cfg.Providers["anthropic"].APIKey)
	}
	if len(cfg.Providers["anthropic"].Models) != 1 {
		t.Errorf("Models count = %d, want 1 (project narrowed the list)", len(cfg.Providers["anthropic"].Models))
	}
}

func TestValidate_RejectsMalformedBaseURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
	}{
		{"not a url", "not a url"},
		{"unsupported scheme", "ftp://example.com"},
		{"missing host", "http://"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			temp := 0.7
			cfg := &Config{
				Providers: map[string]ProviderEntry{
					"anthropic": {
						Kind:    "anthropic",
						APIKey:  "k",
						BaseURL: tc.baseURL,
						Models: []ModelEntry{{
							Name:        "claude-sonnet-4-20250514",
							MaxTokens:   8192,
							Temperature: &temp,
						}},
					},
				},
				Default: DefaultConfig{Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
			}
			err := validate(cfg)
			if err == nil {
				t.Fatalf("validate accepted malformed base_url %q", tc.baseURL)
			}
			if !strings.Contains(err.Error(), "base_url") {
				t.Errorf("error should mention base_url, got: %v", err)
			}
		})
	}
}

func TestValidate_AcceptsEmptyBaseURL(t *testing.T) {
	// Empty base_url is the steady state for cloud providers (use vendor
	// default). validateBaseURLs must not reject it.
	cfg := &Config{
		Providers: map[string]ProviderEntry{
			"anthropic": {
				Kind:    "anthropic",
				APIKey:  "k",
				BaseURL: "",
				Models:  []ModelEntry{{Name: "claude-sonnet-4-20250514", MaxTokens: 8192}},
			},
		},
		Default: DefaultConfig{Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
	}
	if err := validate(cfg); err != nil {
		t.Errorf("validate rejected empty base_url: %v", err)
	}
}
