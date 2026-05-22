package config

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestDefault_CompactFields(t *testing.T) {
	cfg := Default()
	if cfg.Context.DefaultLimit != DefaultContextLimit {
		t.Errorf("Context.DefaultLimit = %d, want %d", cfg.Context.DefaultLimit, DefaultContextLimit)
	}
	if cfg.Context.MaxOverride != DefaultMaxOverride {
		t.Errorf("Context.MaxOverride = %d, want %d", cfg.Context.MaxOverride, DefaultMaxOverride)
	}
	if cfg.Compact.ThresholdPct != DefaultThresholdPct {
		t.Errorf("Compact.ThresholdPct = %d, want %d", cfg.Compact.ThresholdPct, DefaultThresholdPct)
	}
	if cfg.Compact.KeepRecentMessages != DefaultKeepRecentMessages {
		t.Errorf("Compact.KeepRecentMessages = %d, want %d", cfg.Compact.KeepRecentMessages, DefaultKeepRecentMessages)
	}
}

func TestApplyDefaults_FillsZeroFields(t *testing.T) {
	cfg := &Config{}
	ApplyDefaults(cfg)
	if cfg.Context.DefaultLimit != DefaultContextLimit {
		t.Errorf("missing DefaultLimit default")
	}
	if cfg.Compact.ThresholdPct != DefaultThresholdPct {
		t.Errorf("missing ThresholdPct default")
	}
}

func TestApplyDefaults_PreservesExplicitValues(t *testing.T) {
	cfg := &Config{
		Context: ContextConfig{DefaultLimit: 50_000, MaxOverride: 100_000},
		Compact: CompactConfig{Model: "haiku", ThresholdPct: 60, KeepRecentMessages: 2},
	}
	ApplyDefaults(cfg)
	if cfg.Context.DefaultLimit != 50_000 {
		t.Errorf("overridden DefaultLimit clobbered: got %d", cfg.Context.DefaultLimit)
	}
	if cfg.Compact.ThresholdPct != 60 {
		t.Errorf("overridden ThresholdPct clobbered: got %d", cfg.Compact.ThresholdPct)
	}
	if cfg.Compact.Model != "haiku" {
		t.Errorf("compact.Model clobbered: got %q", cfg.Compact.Model)
	}
}

func TestCompactConfig_JSONRoundTrip(t *testing.T) {
	raw := `{
		"providers": {"anthropic": {"kind": "anthropic", "api_key": "$ANTHROPIC_API_KEY",
			"models": [{"name": "claude-sonnet-4-6", "max_tokens": 8192}]}},
		"default": {"provider": "anthropic", "model": "claude-sonnet-4-6"},
		"context": {"default_limit": 150000, "max_override": 300000},
		"compact": {"model": "claude-haiku-4-5", "threshold_pct": 70, "keep_recent_messages": 6}
	}`

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	writeJSON(t, filepath.Join(dir, ".config", "prompto"), "config.json", raw)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Context.DefaultLimit != 150_000 {
		t.Errorf("DefaultLimit = %d", cfg.Context.DefaultLimit)
	}
	if cfg.Context.MaxOverride != 300_000 {
		t.Errorf("MaxOverride = %d", cfg.Context.MaxOverride)
	}
	if cfg.Compact.Model != "claude-haiku-4-5" {
		t.Errorf("Compact.Model = %q", cfg.Compact.Model)
	}
	if cfg.Compact.ThresholdPct != 70 {
		t.Errorf("Compact.ThresholdPct = %d", cfg.Compact.ThresholdPct)
	}

	// Ensure zero-field default fill happens when a field is absent.
	raw2 := `{
		"providers": {"anthropic": {"kind": "anthropic", "api_key": "$ANTHROPIC_API_KEY",
			"models": [{"name": "claude-sonnet-4-6", "max_tokens": 8192}]}},
		"default": {"provider": "anthropic", "model": "claude-sonnet-4-6"}
	}`
	dir2 := t.TempDir()
	t.Setenv("HOME", dir2)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir2, ".config"))
	writeJSON(t, filepath.Join(dir2, ".config", "prompto"), "config.json", raw2)

	cfg2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Context.DefaultLimit != DefaultContextLimit {
		t.Errorf("bare config DefaultLimit = %d, want default", cfg2.Context.DefaultLimit)
	}
	if cfg2.Compact.KeepRecentMessages != DefaultKeepRecentMessages {
		t.Errorf("bare config KeepRecentMessages = %d, want default", cfg2.Compact.KeepRecentMessages)
	}
}

// Silence unused-import for JSON test doc (round-trip tests above).
var _ = json.Marshal

// TestCompactConfig_AcceptsLegacyKeepRecentTurnsKey covers the
// rename: existing user configs spelt `keep_recent_turns`. The
// canonical key is `keep_recent_messages`; the UnmarshalJSON shim
// continues to accept the legacy name so old configs don't silently
// fall back to the default after upgrading prompto.
func TestCompactConfig_AcceptsLegacyKeepRecentTurnsKey(t *testing.T) {
	raw := []byte(`{"threshold_pct":70, "keep_recent_turns":12}`)
	var c CompactConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if c.KeepRecentMessages != 12 {
		t.Errorf("legacy keep_recent_turns not honoured: KeepRecentMessages = %d", c.KeepRecentMessages)
	}
}

func TestCompactConfig_CanonicalKeyWinsOverLegacy(t *testing.T) {
	// Both keys present — canonical takes precedence so users can
	// migrate by adding the new key without removing the old one
	// first (and not get whichever value the JSON parser happened
	// to read last).
	raw := []byte(`{"keep_recent_turns":3, "keep_recent_messages":7}`)
	var c CompactConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.KeepRecentMessages != 7 {
		t.Errorf("KeepRecentMessages = %d, want 7 (canonical key wins)", c.KeepRecentMessages)
	}
}
