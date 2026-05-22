package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

func TestCompactToolSchemaResolverShortensDefinitions(t *testing.T) {
	originalDesc := strings.Repeat("Read a local file with a very long description. ", 8)
	originalSchema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"file_path":{"type":"string","description":"The absolute path to the local file to read from disk with lots of detail."},
			"limit":{"type":"integer","description":"Maximum number of lines to return from the file."}
		},
		"required":["file_path"]
	}`)
	resolver := staticDefinitionResolver{defs: []api.ToolDefinition{{
		Name:        "read",
		Description: originalDesc,
		InputSchema: originalSchema,
	}}}

	defs := NewCompactToolSchemaResolver(resolver).Definitions()
	if len(defs) != 1 {
		t.Fatalf("defs = %d, want 1", len(defs))
	}
	got := defs[0]
	if got.Name != "read" {
		t.Fatalf("name = %q, want read", got.Name)
	}
	if len(got.Description) >= len(originalDesc) {
		t.Fatalf("description was not shortened: %q", got.Description)
	}
	if !json.Valid(got.InputSchema) {
		t.Fatalf("compact schema is invalid JSON: %s", got.InputSchema)
	}

	var schema map[string]any
	if err := json.Unmarshal(got.InputSchema, &schema); err != nil {
		t.Fatalf("unmarshal compact schema: %v", err)
	}
	props := schema["properties"].(map[string]any)
	filePath := props["file_path"].(map[string]any)
	if filePath["type"] != "string" {
		t.Fatalf("file_path type = %v, want string", filePath["type"])
	}
	if len(filePath["description"].(string)) >= len("The absolute path to the local file to read from disk with lots of detail.") {
		t.Fatalf("field description was not shortened: %q", filePath["description"])
	}
	required := schema["required"].([]any)
	if len(required) != 1 || required[0] != "file_path" {
		t.Fatalf("required = %#v, want file_path preserved", required)
	}
}

func TestCompactToolSchemasEnabledModes(t *testing.T) {
	tests := []struct {
		name  string
		mode  string
		local bool
		want  bool
	}{
		{"auto local", "auto", true, true},
		{"auto cloud", "auto", false, false},
		{"on cloud", "on", false, true},
		{"off local", "off", true, false},
		{"empty defaults auto local", "", true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := New(NewAgentInput{
				Provider:      &fakeProvider{},
				Tools:         newFakeResolver(),
				LocalProvider: tc.local,
				ModelGuidance: ModelGuidanceOptions{CompactToolSchemas: tc.mode},
			})
			if got := a.compactToolSchemasEnabled(); got != tc.want {
				t.Fatalf("compactToolSchemasEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRunUsesCompactSchemasForLocalAutoOnly(t *testing.T) {
	longDesc := strings.Repeat("Read a local file with a verbose schema. ", 6)
	schema := json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string","description":"Long field description that should be shortened for local models."}},"required":["file_path"]}`)
	for _, tc := range []struct {
		name        string
		local       bool
		wantCompact bool
	}{
		{"local", true, true},
		{"cloud", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prov := &fakeProvider{responses: [][]api.StreamEvent{textResponse("done")}}
			agnt := New(NewAgentInput{
				Provider:      prov,
				Model:         "test",
				Tools:         newFakeResolver(definitionTool{def: api.ToolDefinition{Name: "read", Description: longDesc, InputSchema: schema}}),
				Notifier:      NewNotifier(),
				LocalProvider: tc.local,
				ModelGuidance: ModelGuidanceOptions{CompactToolSchemas: "auto"},
			})
			conv := NewConversation()
			conv.Append(api.NewUserMessage("hi"))
			if err := drain(t, agnt.Run(t.Context(), RunInput{Conversation: conv, CanUseTool: allowAll})); !errors.Is(err, ErrEndTurn) {
				t.Fatalf("run err = %v", err)
			}
			if len(prov.params) != 1 || len(prov.params[0].Tools) != 1 {
				t.Fatalf("captured params = %#v", prov.params)
			}
			gotDesc := prov.params[0].Tools[0].Description
			gotCompact := gotDesc != longDesc
			if gotCompact != tc.wantCompact {
				t.Fatalf("compact = %v (desc %q), want %v", gotCompact, gotDesc, tc.wantCompact)
			}
		})
	}
}

type staticDefinitionResolver struct {
	defs []api.ToolDefinition
}

func (r staticDefinitionResolver) Resolve(string) (Tool, bool) { return nil, false }
func (r staticDefinitionResolver) Definitions() []api.ToolDefinition {
	return append([]api.ToolDefinition(nil), r.defs...)
}

type definitionTool struct {
	def api.ToolDefinition
}

func (t definitionTool) Name() string                   { return t.def.Name }
func (t definitionTool) Definition() api.ToolDefinition { return t.def }
func (t definitionTool) FormatForDisplay([]byte) string { return t.def.Name + "()" }
func (t definitionTool) MaxResultBytes() int            { return 0 }
func (t definitionTool) Execute(context.Context, ToolContext, []byte) (Result, error) {
	return Result{Content: "ok", Bytes: 2}, nil
}
func (t definitionTool) IsReadOnly() bool            { return true }
func (t definitionTool) IsConcurrencySafe() bool     { return true }
func (t definitionTool) PermissionKey([]byte) string { return "" }
