package tui

import (
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/command"
)

func TestModelPickerSettingsDefaultsAdjustAndReset(t *testing.T) {
	picker := NewModelPickerModel([]command.ModelInfo{{
		Name:            "qwen",
		Provider:        "openrouter",
		ProviderKind:    "openai",
		MaxTokens:       8192,
		Temperature:     1.0,
		PresencePenalty: 0.0,
	}}, "qwen")
	picker.SetSize(100, 30)
	picker.ToggleSettings()

	view := stripANSI(picker.View())
	if !strings.Contains(view, "model settings") {
		t.Fatalf("settings view missing title:\n%s", view)
	}
	if !strings.Contains(view, "temperature") || !strings.Contains(view, "1.0 default") {
		t.Fatalf("settings view missing temperature default:\n%s", view)
	}
	if !strings.Contains(view, "presence penalty") || !strings.Contains(view, "0.0 default") {
		t.Fatalf("settings view missing presence penalty default:\n%s", view)
	}

	updated, ok := picker.AdjustSetting(0.1)
	if !ok {
		t.Fatal("AdjustSetting returned ok=false")
	}
	if updated.Temperature != 1.1 || !updated.TemperatureConfigured {
		t.Fatalf("temperature = %v configured=%v, want 1.1/true", updated.Temperature, updated.TemperatureConfigured)
	}
	if updated.PresencePenaltyConfigured {
		t.Fatalf("presence penalty configured = true, want false")
	}

	picker.Move(1)
	updated, ok = picker.AdjustSetting(-0.1)
	if !ok {
		t.Fatal("AdjustSetting presence returned ok=false")
	}
	if updated.PresencePenalty != -0.1 || !updated.PresencePenaltyConfigured {
		t.Fatalf("presence = %v configured=%v, want -0.1/true", updated.PresencePenalty, updated.PresencePenaltyConfigured)
	}

	reset, ok := picker.ResetSettings()
	if !ok {
		t.Fatal("ResetSettings returned ok=false")
	}
	if reset.TemperatureConfigured || reset.PresencePenaltyConfigured {
		t.Fatalf("reset configured flags = %v/%v, want false/false", reset.TemperatureConfigured, reset.PresencePenaltyConfigured)
	}
}
