package command

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCustomCommands_BasicLoad(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ship.md"), []byte("Ship the changes for $ARGS."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("First non-empty line.\n\nbody."), 0o644); err != nil {
		t.Fatal(err)
	}

	cmds, err := LoadCustomCommands(dir, NewRegistry())
	if err != nil {
		t.Fatalf("LoadCustomCommands: %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("len(cmds) = %d, want 2", len(cmds))
	}
	for _, c := range cmds {
		if c.Kind() != KindExpanding {
			t.Errorf("%q Kind = %v, want KindExpanding", c.Name(), c.Kind())
		}
	}
}

func TestCustomCommand_ArgsSubstitution(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tag.md"), []byte("tag $ARGS now"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmds, err := LoadCustomCommands(dir, NewRegistry())
	if err != nil || len(cmds) != 1 {
		t.Fatalf("load: err=%v len=%d", err, len(cmds))
	}
	res, err := cmds[0].Exec(context.Background(), []string{"v0.5"}, nil)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.Prompt != "tag v0.5 now" {
		t.Errorf("Prompt = %q, want %q", res.Prompt, "tag v0.5 now")
	}
}

func TestLoadCustomCommands_ShadowedByBuiltinDropped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "help.md"), []byte("not allowed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok.md"), []byte("allowed"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	if err := RegisterBuiltins(reg); err != nil {
		t.Fatal(err)
	}
	cmds, err := LoadCustomCommands(dir, reg)
	if err != nil {
		t.Fatalf("LoadCustomCommands: %v", err)
	}
	if len(cmds) != 1 || cmds[0].Name() != "ok" {
		t.Errorf("loaded = %v; expected only [ok]", cmds)
	}
}

func TestLoadCustomCommands_MissingDirOK(t *testing.T) {
	cmds, err := LoadCustomCommands(filepath.Join(t.TempDir(), "does-not-exist"), NewRegistry())
	if err != nil {
		t.Fatalf("LoadCustomCommands: %v", err)
	}
	if len(cmds) != 0 {
		t.Errorf("got %d cmds; want 0", len(cmds))
	}
}
