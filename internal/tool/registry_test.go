package tool

import "testing"

func TestRegistryGetAndDefinitions(t *testing.T) {
	r := NewRegistry(NewReadTool(), NewBashTool(), NewReplaceLinesTool())

	if r.Get("read") == nil {
		t.Error("Get('read') returned nil")
	}
	if r.Get("bash") == nil {
		t.Error("Get('bash') returned nil")
	}
	if r.Get("nonexistent") != nil {
		t.Error("Get('nonexistent') should return nil")
	}

	defs := r.Definitions()
	if r.Get("replace_lines") == nil {
		t.Error("Get('replace_lines') returned nil")
	}
	if len(defs) != 3 {
		t.Fatalf("Definitions() returned %d, want 3", len(defs))
	}
	if defs[0].Name != "read" {
		t.Errorf("defs[0].Name = %q, want 'read'", defs[0].Name)
	}
	if defs[1].Name != "bash" {
		t.Errorf("defs[1].Name = %q, want 'bash'", defs[1].Name)
	}
	if defs[2].Name != "replace_lines" {
		t.Errorf("defs[2].Name = %q, want 'replace_lines'", defs[2].Name)
	}
}

func TestRegistryDuplicatePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate tool name")
		}
	}()
	NewRegistry(NewReadTool(), NewReadTool())
}
