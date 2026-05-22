package permission

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/marcomoesman/prompto/internal/privatefs"
)

// LoadRulesetInput bundles the inputs to LoadRuleset.
type LoadRulesetInput struct {
	// ProjectPath is the path to the per-project JSON file
	// (typically <cwd>/.prompto/permissions.json). Missing file is not an
	// error; a fresh empty ruleset is returned with project-scope saving
	// wired to this path.
	ProjectPath string
}

// LoadRuleset loads project-scoped rules from the JSON file (if present)
// and returns a Ruleset whose saver writes back to that file on project
// appends. A missing file yields an empty ruleset, not an error.
func LoadRuleset(in LoadRulesetInput) (*Ruleset, error) {
	r := NewRuleset()
	if in.ProjectPath != "" {
		if err := loadProjectFile(r, in.ProjectPath); err != nil {
			return nil, err
		}
		path := in.ProjectPath
		r.SetSaver(func(rules []Rule) error {
			return saveProjectFile(path, rules)
		})
	}
	return r, nil
}

type projectFileFormat struct {
	Rules []Rule `json:"rules"`
}

func loadProjectFile(r *Ruleset, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // no file yet; empty ruleset is fine
		}
		return fmt.Errorf("permission: reading %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil
	}
	var parsed projectFileFormat
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("permission: parsing %s: %w", path, err)
	}
	for _, rule := range parsed.Rules {
		rule.Scope = ScopeProject
		r.rules = append(r.rules, rule)
	}
	return nil
}

func saveProjectFile(path string, rules []Rule) error {
	if err := privatefs.EnsureDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("permission: creating dir for %s: %w", path, err)
	}
	data, err := json.MarshalIndent(projectFileFormat{Rules: rules}, "", "  ")
	if err != nil {
		return fmt.Errorf("permission: marshaling rules: %w", err)
	}
	// Trailing newline so the file is POSIX-friendly for manual editing.
	data = append(data, '\n')
	if err := privatefs.WriteFile(path, data); err != nil {
		return fmt.Errorf("permission: writing %s: %w", path, err)
	}
	return nil
}
