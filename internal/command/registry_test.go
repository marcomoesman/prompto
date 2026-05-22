package command

import (
	"context"
	"testing"
)

type stubCmd struct {
	name    string
	aliases []string
	kind    Kind
	help    string
}

func (s stubCmd) Name() string                                        { return s.name }
func (s stubCmd) Aliases() []string                                   { return s.aliases }
func (s stubCmd) Kind() Kind                                          { return s.kind }
func (s stubCmd) Help() string                                        { return s.help }
func (s stubCmd) Exec(context.Context, []string, Env) (Result, error) { return Result{}, nil }

func TestRegistryRegisterAndResolve(t *testing.T) {
	r := NewRegistry()

	if err := r.Register(stubCmd{name: "help"}); err != nil {
		t.Fatalf("Register help: %v", err)
	}
	if err := r.Register(stubCmd{name: "quit", aliases: []string{"exit", "q"}}); err != nil {
		t.Fatalf("Register quit: %v", err)
	}

	got, ok := r.Resolve("help")
	if !ok || got.Name() != "help" {
		t.Errorf("Resolve(\"help\") = %v, %v; want help", got, ok)
	}
	got, ok = r.Resolve("exit")
	if !ok || got.Name() != "quit" {
		t.Errorf("Resolve(\"exit\") = %v, %v; want quit (via alias)", got, ok)
	}
	got, ok = r.Resolve("q")
	if !ok || got.Name() != "quit" {
		t.Errorf("Resolve(\"q\") = %v, %v; want quit", got, ok)
	}
	if _, ok := r.Resolve("unknown"); ok {
		t.Error("Resolve(\"unknown\") should fail")
	}
}

func TestRegistryDuplicateName(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(stubCmd{name: "help"}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := r.Register(stubCmd{name: "help"}); err == nil {
		t.Error("expected error on duplicate name")
	}
}

func TestRegistryAliasCollidesWithName(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(stubCmd{name: "help"})
	err := r.Register(stubCmd{name: "h", aliases: []string{"help"}})
	if err == nil {
		t.Error("expected error when alias collides with existing command name")
	}
}

func TestRegistryNameCollidesWithExistingAlias(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(stubCmd{name: "quit", aliases: []string{"q"}})
	err := r.Register(stubCmd{name: "q"})
	if err == nil {
		t.Error("expected error when name collides with existing alias")
	}
}

func TestRegistryAllOrdered(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(stubCmd{name: "zeta"})
	_ = r.Register(stubCmd{name: "alpha"})
	_ = r.Register(stubCmd{name: "mid"})

	all := r.All()
	want := []string{"alpha", "mid", "zeta"}
	if len(all) != len(want) {
		t.Fatalf("All() returned %d, want %d", len(all), len(want))
	}
	for i, name := range want {
		if all[i].Name() != name {
			t.Errorf("All()[%d] = %q, want %q", i, all[i].Name(), name)
		}
	}
}

func TestRegistryIsReserved(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(stubCmd{name: "help"})
	_ = r.Register(stubCmd{name: "quit", aliases: []string{"q"}})

	if !r.IsReserved("help") {
		t.Error("help should be reserved")
	}
	if !r.IsReserved("q") {
		t.Error("q should be reserved (alias of quit)")
	}
	if r.IsReserved("custom") {
		t.Error("custom should not be reserved")
	}
}

func TestRegistryNilSafe(t *testing.T) {
	var r *Registry
	if _, ok := r.Resolve("help"); ok {
		t.Error("nil registry should not resolve")
	}
	if r.IsReserved("help") {
		t.Error("nil registry should not reserve")
	}
	if all := r.All(); all != nil {
		t.Errorf("nil registry All() = %v, want nil", all)
	}
}
