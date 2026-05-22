package agent

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// RequiredPlanSections is the canonical list of `##` headings that
// must appear in a plan markdown body for plan_exit to succeed. The
// order here is the canonical reporting order: when a plan is
// missing several headings, MissingSectionsError lists them in this
// order regardless of document order. The validator does NOT enforce
// in-document ordering.
//
// Names are sourced verbatim from the plan-mode prompt's "Plan
// schema" section so the model and the validator share one source
// of truth.
var RequiredPlanSections = []string{
	"Context",
	"Goal & acceptance criteria",
	"Files",
	"Verification",
	"Risks / out-of-scope",
}

// MissingSectionsError reports which required `##` sections were
// absent from a plan markdown body. Sections is preserved in
// RequiredPlanSections order so a single render of the message is
// stable across runs.
type MissingSectionsError struct {
	Sections []string
}

// Error returns a single-line message shaped for the model: it
// names every missing section and reminds the model the heading
// must be `##` exactly.
func (e *MissingSectionsError) Error() string {
	if len(e.Sections) == 0 {
		return "plan validation: missing sections (none reported)"
	}
	return fmt.Sprintf(
		"plan is missing required `##` heading(s): %s. Add the heading exactly as named.",
		strings.Join(e.Sections, ", "),
	)
}

// ValidatePlanMarkdown scans body for every entry in
// RequiredPlanSections (case-insensitive) and returns nil when all
// are present. Missing entries surface as a *MissingSectionsError
// with the canonical-order Sections slice; callers use errors.As to
// inspect.
//
// Scan rules:
//   - Only `##` headings count. `#` (h1) and `###`+ (h3+) are
//     ignored, matching how the prompt instructs the model to use
//     `##` exactly.
//   - Headings inside fenced code blocks (``` or ~~~) are ignored.
//     This avoids false positives from embedded plan examples.
//   - ATX-style trailing closes (`## Foo ##`) are tolerated.
//   - Comparison is case-insensitive after trimming whitespace and
//     trailing close-hashes.
//   - In-document ordering is irrelevant.
//   - Additional `##` headings beyond the required set are allowed.
//
// Empty bodies always fail (every section is missing).
func ValidatePlanMarkdown(body []byte) error {
	found := make(map[string]bool, len(RequiredPlanSections))
	required := make(map[string]string, len(RequiredPlanSections))
	for _, name := range RequiredPlanSections {
		required[strings.ToLower(name)] = name
	}

	sc := bufio.NewScanner(bytes.NewReader(body))
	// Plans can run long; raise the line buffer ceiling so giant
	// fenced code blocks (e.g. SQL or generated config) don't blow
	// the default 64 KB cap mid-parse.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	inFence := false
	fenceMarker := ""
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " \t")

		if inFence {
			if strings.HasPrefix(trimmed, fenceMarker) {
				inFence = false
				fenceMarker = ""
			}
			continue
		}

		if strings.HasPrefix(trimmed, "```") {
			inFence = true
			fenceMarker = "```"
			continue
		}
		if strings.HasPrefix(trimmed, "~~~") {
			inFence = true
			fenceMarker = "~~~"
			continue
		}

		heading, ok := matchH2(trimmed)
		if !ok {
			continue
		}
		key := strings.ToLower(heading)
		if canonical, ok := required[key]; ok {
			found[canonical] = true
		}
	}

	var missing []string
	for _, req := range RequiredPlanSections {
		if !found[req] {
			missing = append(missing, req)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &MissingSectionsError{Sections: missing}
}

// matchH2 returns the trimmed heading text after `## ` if line is
// exactly an h2 ATX heading. `### foo` returns ("", false) because
// "###" doesn't match the literal `## ` prefix. Trailing close
// hashes and whitespace are stripped.
func matchH2(line string) (string, bool) {
	if !strings.HasPrefix(line, "## ") {
		return "", false
	}
	h := strings.TrimPrefix(line, "## ")
	// ATX closing: "## foo ##" → "foo".
	h = strings.TrimRight(h, "# \t")
	h = strings.TrimSpace(h)
	if h == "" {
		return "", false
	}
	return h, true
}
