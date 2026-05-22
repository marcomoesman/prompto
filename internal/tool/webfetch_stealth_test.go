package tool

import (
	"strings"
	"testing"
)

// TestStealthScript_HasRequiredPatches asserts the embedded script
// contains every patch in the medium set. The chromedp end-to-end
// behaviour is exercised separately (manual smoke), but the embed
// itself is unit-checkable.
func TestStealthScript_HasRequiredPatches(t *testing.T) {
	if len(stealthScript) < 200 {
		t.Fatalf("stealthScript looks empty (%d bytes)", len(stealthScript))
	}
	if !strings.HasPrefix(stealthScript, "// @prompto-stealth medium") {
		t.Errorf("missing version marker on first line")
	}
	required := []string{
		"navigator", "webdriver",
		"navigator", "languages",
		"navigator", "plugins",
		"navigator", "userAgentData",
		"window.chrome",
		"Permissions",
	}
	// Pair entries match against substrings; we only check each
	// substring exists once.
	for _, want := range required {
		if !strings.Contains(stealthScript, want) {
			t.Errorf("stealthScript missing %q", want)
		}
	}
}
