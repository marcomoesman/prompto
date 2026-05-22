package agent

import (
	"context"
	"testing"

	"github.com/marcomoesman/prompto/internal/api"
)

// Compile-time assertion: the fake tools in this package satisfy Tool.
var (
	_ Tool = (*echoTool)(nil)
	_ Tool = (*fileReadTool)(nil)
	_ Tool = (*fileEditTool)(nil)
	_ Tool = (*extWriterTool)(nil)
)

// shapeCheckTool verifies the Tool interface shape hasn't regressed. Adding
// a method to Tool without updating this test will fail to compile.
type shapeCheckTool struct{}

func (shapeCheckTool) Name() string                   { return "shape" }
func (shapeCheckTool) Definition() api.ToolDefinition { return api.ToolDefinition{} }
func (shapeCheckTool) FormatForDisplay([]byte) string { return "" }
func (shapeCheckTool) MaxResultBytes() int            { return 0 }
func (shapeCheckTool) IsReadOnly() bool               { return true }
func (shapeCheckTool) IsConcurrencySafe() bool        { return true }
func (shapeCheckTool) PermissionKey([]byte) string    { return "" }
func (shapeCheckTool) Execute(context.Context, ToolContext, []byte) (Result, error) {
	return Result{}, nil
}

var _ Tool = shapeCheckTool{}

func TestDecisionValues(t *testing.T) {
	// DecisionAllow must be distinct from DecisionDeny. Adding new
	// Decision values (e.g. DecisionAsk) won't break this test as long
	// as the two pre-existing values stay distinct.
	if DecisionAllow == DecisionDeny {
		t.Fatal("DecisionAllow must differ from DecisionDeny")
	}
}
