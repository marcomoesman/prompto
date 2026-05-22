package agent

import (
	"encoding/json"
	"strings"

	"github.com/marcomoesman/prompto/internal/api"
)

// NewCompactToolSchemaResolver wraps a resolver so provider-facing
// definitions use shorter descriptions. Resolve delegates to inner unchanged,
// so execution, permission keys, and Go-level validation still use the
// original tools.
func NewCompactToolSchemaResolver(inner ToolResolver) ToolResolver {
	if inner == nil {
		return nil
	}
	return compactToolSchemaResolver{inner: inner}
}

type compactToolSchemaResolver struct {
	inner ToolResolver
}

func (r compactToolSchemaResolver) Resolve(name string) (Tool, bool) {
	return r.inner.Resolve(name)
}

func (r compactToolSchemaResolver) Definitions() []api.ToolDefinition {
	defs := r.inner.Definitions()
	out := make([]api.ToolDefinition, 0, len(defs))
	for _, def := range defs {
		out = append(out, compactToolDefinition(def))
	}
	return out
}

func compactToolDefinition(def api.ToolDefinition) api.ToolDefinition {
	def.Description = compactToolDescription(def.Name, def.Description)
	def.InputSchema = compactInputSchema(def.Name, def.InputSchema)
	return def
}

func compactToolDescription(name, original string) string {
	if desc, ok := compactToolDescriptions[name]; ok {
		return desc
	}
	return compactSentence(original, 120)
}

var compactToolDescriptions = map[string]string{
	"read":              "Read a local file. Use offset/limit for slices.",
	"bash":              "Run a shell command in the workspace.",
	"edit":              "Replace exact text in a file. Read first.",
	"replace_lines":     "Replace a 1-based inclusive line range. Read first.",
	"write":             "Create or overwrite a file.",
	"grep":              "Search file contents.",
	"glob":              "Find files by pattern.",
	"webfetch":          "Fetch a URL as markdown.",
	"webfetch_headless": "Fetch a URL with a browser.",
	"websearch":         "Search the web.",
	"task":              "Spawn a subagent.",
	"todowrite":         "Replace the task list.",
	"plan_exit":         "Submit the current plan for approval.",
}

var compactFieldDescriptions = map[string]map[string]string{
	"read": {
		"file_path": "Path to read.",
		"offset":    "0-based start line.",
		"limit":     "Max lines.",
	},
	"bash": {
		"command": "Command to run.",
		"timeout": "Timeout seconds.",
	},
	"edit": {
		"file_path":  "Path to edit.",
		"old_string": "Exact unique text to find.",
		"new_string": "Replacement text.",
		"edits":      "Atomic replacements against original file.",
	},
	"replace_lines": {
		"file_path":   "Path to edit.",
		"start_line":  "First line, 1-based.",
		"end_line":    "Last line, inclusive.",
		"replacement": "Replacement text.",
	},
	"write": {
		"file_path": "Path to write.",
		"content":   "Full file content.",
	},
	"grep": {
		"pattern": "Regex to search.",
		"path":    "File or directory.",
		"glob":    "File glob filter.",
	},
	"glob": {
		"pattern": "Glob pattern.",
		"path":    "Directory to search.",
	},
	"webfetch": {
		"url":    "URL to fetch.",
		"prompt": "Optional extraction instruction.",
	},
	"websearch": {
		"query": "Search query.",
		"limit": "Max results.",
	},
	"task": {
		"subagent_type": "Subagent name.",
		"description":   "Short task label.",
		"prompt":        "Task instructions.",
		"task_id":       "Existing child session.",
	},
	"todowrite": {
		"todos": "Complete task list.",
	},
	"plan_exit": {
		"summary": "Plan summary.",
	},
}

func compactInputSchema(toolName string, raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	compactSchemaValue(toolName, v)
	data, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return data
}

func compactSchemaValue(toolName string, v any) {
	switch x := v.(type) {
	case map[string]any:
		if desc, ok := x["description"].(string); ok {
			x["description"] = compactSentence(desc, 80)
		}
		if props, ok := x["properties"].(map[string]any); ok {
			for name, prop := range props {
				if p, ok := prop.(map[string]any); ok {
					if desc := compactFieldDescription(toolName, name); desc != "" {
						p["description"] = desc
					}
				}
			}
		}
		for _, child := range x {
			compactSchemaValue(toolName, child)
		}
	case []any:
		for _, child := range x {
			compactSchemaValue(toolName, child)
		}
	}
}

func compactFieldDescription(toolName, field string) string {
	if fields, ok := compactFieldDescriptions[toolName]; ok {
		return fields[field]
	}
	return ""
}

func compactSentence(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	if idx := strings.Index(s, ". "); idx > 0 && idx <= max {
		return s[:idx+1]
	}
	if max <= 1 {
		return s[:max]
	}
	return strings.TrimSpace(s[:max-1]) + "…"
}
