package command

import (
	"fmt"
	"sort"
)

// Registry holds the resolved Commands keyed by name + alias. Construct
// with NewRegistry; populate with Register. Reads (Resolve, All) are
// safe for concurrent callers; Register is not — sequence all
// registrations during startup.
type Registry struct {
	commands map[string]Command // canonical name → Command
	aliases  map[string]string  // alias → canonical
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		commands: make(map[string]Command),
		aliases:  make(map[string]string),
	}
}

// Register adds a Command. Duplicate canonical names return an error so
// startup wiring fails loudly. Alias collisions also error. Built-in
// commands populate first so custom commands collide deterministically.
func (r *Registry) Register(c Command) error {
	name := c.Name()
	if name == "" {
		return fmt.Errorf("command: register: empty name")
	}
	if _, exists := r.commands[name]; exists {
		return fmt.Errorf("command: register: duplicate name %q", name)
	}
	if _, exists := r.aliases[name]; exists {
		return fmt.Errorf("command: register: name %q collides with existing alias", name)
	}
	for _, alias := range c.Aliases() {
		if alias == "" {
			continue
		}
		if _, exists := r.commands[alias]; exists {
			return fmt.Errorf("command: register: alias %q collides with command name", alias)
		}
		if existing, ok := r.aliases[alias]; ok {
			return fmt.Errorf("command: register: alias %q already mapped to %q", alias, existing)
		}
	}
	r.commands[name] = c
	for _, alias := range c.Aliases() {
		if alias == "" {
			continue
		}
		r.aliases[alias] = name
	}
	return nil
}

// Resolve looks up a command by name or alias. Returns (nil, false) when
// no match.
func (r *Registry) Resolve(name string) (Command, bool) {
	if r == nil {
		return nil, false
	}
	if c, ok := r.commands[name]; ok {
		return c, true
	}
	if canonical, ok := r.aliases[name]; ok {
		return r.commands[canonical], true
	}
	return nil, false
}

// All returns every registered command in deterministic (name) order. The
// help overlay uses this to render the command list.
func (r *Registry) All() []Command {
	if r == nil {
		return nil
	}
	out := make([]Command, 0, len(r.commands))
	for _, c := range r.commands {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// IsReserved reports whether name (or any alias of an already-registered
// command) is taken. Custom commands check this before registering so
// .prompto/commands/*.md cannot shadow built-ins.
func (r *Registry) IsReserved(name string) bool {
	if r == nil {
		return false
	}
	if _, ok := r.commands[name]; ok {
		return true
	}
	if _, ok := r.aliases[name]; ok {
		return true
	}
	return false
}
