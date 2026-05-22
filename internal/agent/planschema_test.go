package agent

import (
	"errors"
	"strings"
	"testing"
)

// validPlanBody returns a minimal plan markdown body that satisfies
// every entry in RequiredPlanSections. Tests that need a baseline
// plan extend this rather than re-stating the headings.
func validPlanBody() string {
	return `# Plan title

## Context
why we're doing this

## Goal & acceptance criteria
done when X

## Files
- foo.go — rationale

## Verification
- ` + "`go test ./...`" + `

## Risks / out-of-scope
- nothing
`
}

func TestValidatePlanMarkdown_AllPresent(t *testing.T) {
	if err := ValidatePlanMarkdown([]byte(validPlanBody())); err != nil {
		t.Fatalf("expected nil error for complete plan; got %v", err)
	}
}

func TestValidatePlanMarkdown_AllMissing(t *testing.T) {
	body := []byte(`# Plan

## Some other heading
not one of the required ones

## Notes
also not required
`)
	err := ValidatePlanMarkdown(body)
	var miss *MissingSectionsError
	if !errors.As(err, &miss) {
		t.Fatalf("expected *MissingSectionsError, got %T (%v)", err, err)
	}
	if len(miss.Sections) != len(RequiredPlanSections) {
		t.Errorf("missing count = %d, want %d (%v)", len(miss.Sections), len(RequiredPlanSections), miss.Sections)
	}
	// Order must match RequiredPlanSections.
	for i, name := range RequiredPlanSections {
		if miss.Sections[i] != name {
			t.Errorf("missing[%d] = %q, want %q", i, miss.Sections[i], name)
		}
	}
}

func TestValidatePlanMarkdown_OneMissing(t *testing.T) {
	body := []byte(`## Context
x

## Goal & acceptance criteria
y

## Files
z

## Verification
w
`)
	err := ValidatePlanMarkdown(body)
	var miss *MissingSectionsError
	if !errors.As(err, &miss) {
		t.Fatalf("expected MissingSectionsError, got %v", err)
	}
	if len(miss.Sections) != 1 || miss.Sections[0] != "Risks / out-of-scope" {
		t.Errorf("missing = %v, want [Risks / out-of-scope]", miss.Sections)
	}
}

func TestValidatePlanMarkdown_CaseInsensitive(t *testing.T) {
	body := []byte(`## CONTEXT
## Goal & Acceptance Criteria
## files
## VERIFICATION
## Risks / Out-Of-Scope
`)
	if err := ValidatePlanMarkdown(body); err != nil {
		t.Errorf("case-insensitive comparison should accept; got %v", err)
	}
}

func TestValidatePlanMarkdown_OutOfOrder(t *testing.T) {
	body := []byte(`## Verification
## Files
## Risks / out-of-scope
## Context
## Goal & acceptance criteria
`)
	if err := ValidatePlanMarkdown(body); err != nil {
		t.Errorf("out-of-order plan should validate; got %v", err)
	}
}

func TestValidatePlanMarkdown_AdditionalHeadingsAllowed(t *testing.T) {
	body := []byte(`## Context
## Goal & acceptance criteria
## Files
## Notes
## Implementation strategy
## Verification
## Risks / out-of-scope
## Open questions
`)
	if err := ValidatePlanMarkdown(body); err != nil {
		t.Errorf("additional headings should not break validation; got %v", err)
	}
}

func TestValidatePlanMarkdown_H1HeadingsDoNotCount(t *testing.T) {
	body := []byte(`# Context
# Goal & acceptance criteria
# Files
# Verification
# Risks / out-of-scope
`)
	err := ValidatePlanMarkdown(body)
	var miss *MissingSectionsError
	if !errors.As(err, &miss) {
		t.Fatal("expected MissingSectionsError; only `##` headings should count")
	}
	if len(miss.Sections) != len(RequiredPlanSections) {
		t.Errorf("missing count = %d, want %d", len(miss.Sections), len(RequiredPlanSections))
	}
}

func TestValidatePlanMarkdown_H3HeadingsDoNotCount(t *testing.T) {
	body := []byte(`### Context
### Goal & acceptance criteria
### Files
### Verification
### Risks / out-of-scope
`)
	err := ValidatePlanMarkdown(body)
	var miss *MissingSectionsError
	if !errors.As(err, &miss) {
		t.Fatal("expected MissingSectionsError; only `##` headings should count")
	}
}

func TestValidatePlanMarkdown_FencedCodeIgnored(t *testing.T) {
	// A plan body that has every required heading ONLY inside a
	// fenced code block — the code block is a teaching example, not
	// real plan content. Validation must still fail.
	body := []byte("# Plan\n\n" +
		"Here's an example of the schema:\n\n" +
		"```markdown\n" +
		"## Context\n" +
		"## Goal & acceptance criteria\n" +
		"## Files\n" +
		"## Verification\n" +
		"## Risks / out-of-scope\n" +
		"```\n" +
		"\n" +
		"That's the structure.\n")
	err := ValidatePlanMarkdown(body)
	var miss *MissingSectionsError
	if !errors.As(err, &miss) {
		t.Fatal("headings inside fenced code blocks must not satisfy the schema")
	}
	if len(miss.Sections) != len(RequiredPlanSections) {
		t.Errorf("missing count = %d, want %d", len(miss.Sections), len(RequiredPlanSections))
	}
}

func TestValidatePlanMarkdown_TildeFencedCodeIgnored(t *testing.T) {
	body := []byte("~~~markdown\n" +
		"## Context\n" +
		"## Goal & acceptance criteria\n" +
		"## Files\n" +
		"## Verification\n" +
		"## Risks / out-of-scope\n" +
		"~~~\n")
	err := ValidatePlanMarkdown(body)
	var miss *MissingSectionsError
	if !errors.As(err, &miss) {
		t.Fatal("headings inside ~~~ fenced blocks must not count")
	}
}

func TestValidatePlanMarkdown_FencedThenRealHeadings(t *testing.T) {
	// Half the headings inside a fence (don't count), the other half
	// outside (count). Should report only the in-fence ones as
	// missing.
	body := []byte("```markdown\n" +
		"## Context\n" +
		"## Goal & acceptance criteria\n" +
		"```\n" +
		"\n" +
		"## Files\n" +
		"## Verification\n" +
		"## Risks / out-of-scope\n")
	err := ValidatePlanMarkdown(body)
	var miss *MissingSectionsError
	if !errors.As(err, &miss) {
		t.Fatalf("expected MissingSectionsError, got %v", err)
	}
	want := []string{"Context", "Goal & acceptance criteria"}
	if len(miss.Sections) != len(want) {
		t.Fatalf("missing = %v, want %v", miss.Sections, want)
	}
	for i, name := range want {
		if miss.Sections[i] != name {
			t.Errorf("missing[%d] = %q, want %q", i, miss.Sections[i], name)
		}
	}
}

func TestValidatePlanMarkdown_TrailingCloseHashesTolerated(t *testing.T) {
	body := []byte(`## Context ##
## Goal & acceptance criteria ##
## Files ###
## Verification #
## Risks / out-of-scope ##
`)
	if err := ValidatePlanMarkdown(body); err != nil {
		t.Errorf("ATX close-hashes should be tolerated; got %v", err)
	}
}

func TestValidatePlanMarkdown_EmptyBody(t *testing.T) {
	if err := ValidatePlanMarkdown(nil); err == nil {
		t.Error("nil body should fail")
	}
	if err := ValidatePlanMarkdown([]byte("")); err == nil {
		t.Error("empty body should fail")
	}
}

func TestValidatePlanMarkdown_LargeBody(t *testing.T) {
	var sb strings.Builder
	for _, h := range RequiredPlanSections {
		sb.WriteString("## " + h + "\n")
		// ~10 KB of filler per section — total >50 KB body.
		for i := 0; i < 250; i++ {
			sb.WriteString("filler line " + strings.Repeat("x", 40) + "\n")
		}
	}
	if err := ValidatePlanMarkdown([]byte(sb.String())); err != nil {
		t.Errorf("large body should validate; got %v", err)
	}
}

func TestValidatePlanMarkdown_WindowsLineEndings(t *testing.T) {
	// Convert the valid body to CRLF; Scanner's default ScanLines
	// drops the trailing \r, so this should still validate.
	crlf := strings.ReplaceAll(validPlanBody(), "\n", "\r\n")
	if err := ValidatePlanMarkdown([]byte(crlf)); err != nil {
		t.Errorf("CRLF body should validate; got %v", err)
	}
}

func TestMissingSectionsError_Message(t *testing.T) {
	err := &MissingSectionsError{Sections: []string{"Context", "Files"}}
	msg := err.Error()
	for _, want := range []string{"Context", "Files", "##"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not contain %q", msg, want)
		}
	}
}

func TestMissingSectionsError_EmptyRendersSafely(t *testing.T) {
	err := &MissingSectionsError{}
	if msg := err.Error(); msg == "" {
		t.Error("empty error should still render a non-empty message")
	}
}

func TestValidatePlanMarkdown_OnlyHeadingNoBody(t *testing.T) {
	// Headings without bodies still satisfy the schema — the
	// validator only checks heading presence, not section depth.
	// Whether the model fills them is a separate quality question.
	body := []byte(`## Context
## Goal & acceptance criteria
## Files
## Verification
## Risks / out-of-scope
`)
	if err := ValidatePlanMarkdown(body); err != nil {
		t.Errorf("heading-only plan should validate; got %v", err)
	}
}
