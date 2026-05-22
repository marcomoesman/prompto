package command

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
		wantArgs []string
	}{
		{"simple", "/help", "help", nil},
		{"with args", "/resume abc123 def", "resume", []string{"abc123", "def"}},
		{"leading whitespace", "  /quit", "quit", nil},
		{"trailing whitespace", "/clear  ", "clear", nil},
		{"multi-space args", "/undo   3", "undo", []string{"3"}},
		{"no slash", "hello world", "", nil},
		{"empty", "", "", nil},
		{"slash only", "/", "", nil},
		{"slash whitespace", "/  ", "", nil},
		{"alias", "/q", "q", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotArgs := Parse(tt.input)
			if gotName != tt.wantName {
				t.Errorf("name = %q, want %q", gotName, tt.wantName)
			}
			if !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Errorf("args = %#v, want %#v", gotArgs, tt.wantArgs)
			}
		})
	}
}

func TestArgString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/commit message with multiple words", "message with multiple words"},
		{"/commit", ""},
		{"/commit  hello   world", "hello   world"},
		{"  /init   --force  ", "--force"},
		{"hello world", ""}, // no slash; treated as no-op (still strips leading space if any)
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ArgString(tt.input)
			if got != tt.want {
				t.Errorf("ArgString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
