package tool

import (
	"strings"
	"testing"
)

func TestCleanupMarkdown_DropsLoneBackslashLines(t *testing.T) {
	in := strings.Join([]string{
		"**Product One**",
		`\`,
		"A description of the product.",
		`\`,
		"Price: $9.99",
	}, "\n")
	got := cleanupMarkdown(in)
	if strings.Contains(got, `\`) {
		t.Errorf("backslash continuation lines should be dropped:\n%s", got)
	}
	for _, want := range []string{"Product One", "description", "Price"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output:\n%s", want, got)
		}
	}
}

func TestCleanupMarkdown_CollapsesRepeatedParagraphs(t *testing.T) {
	// Carousel pattern: same line three times in a row, separated by
	// blank lines.
	in := strings.Join([]string{
		"the same scrolling banner text",
		"",
		"the same scrolling banner text",
		"",
		"the same scrolling banner text",
	}, "\n")
	got := cleanupMarkdown(in)
	if c := strings.Count(got, "scrolling banner text"); c != 1 {
		t.Errorf("expected 1 occurrence, got %d:\n%s", c, got)
	}
}

func TestCleanupMarkdown_LeavesDistinctParagraphsAlone(t *testing.T) {
	in := strings.Join([]string{
		"alpha",
		"",
		"beta",
		"",
		"alpha",
	}, "\n")
	got := cleanupMarkdown(in)
	if c := strings.Count(got, "alpha"); c != 2 {
		t.Errorf("non-adjacent dupes must survive; got %d alphas:\n%s", c, got)
	}
}

func TestCleanupMarkdown_CollapsesTripleBlanks(t *testing.T) {
	in := "a\n\n\n\nb"
	got := cleanupMarkdown(in)
	if got != "a\n\nb" {
		t.Errorf("got %q, want %q", got, "a\n\nb")
	}
}

func TestCleanupMarkdown_TrimsOuterWhitespace(t *testing.T) {
	in := "\n\n\n  hello  \n\n\n"
	got := cleanupMarkdown(in)
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestHtmlToMarkdown_SkipsEmptyContentLink(t *testing.T) {
	// `<a href="x"><img src="..." alt=""></a>` — decorative image
	// inside a link. Pre-Phase-17 this rendered as `[![](url)](href)`;
	// with WithLinkEmptyContentBehavior(LinkBehaviorSkip) the link is
	// skipped entirely.
	in := `<html><body><a href="https://example.com/page"><img src="https://example.com/icon.svg" alt=""></a></body></html>`
	got, err := htmlToMarkdown(in, "https://example.com/")
	if err != nil {
		t.Fatalf("htmlToMarkdown: %v", err)
	}
	if strings.Contains(got, "[![](") {
		t.Errorf("empty-content link still rendered:\n%s", got)
	}
}

func TestHtmlToMarkdown_SkipsEmptyHrefLink(t *testing.T) {
	// `<a href="">text</a>` should render as plain text, not `[text]()`.
	in := `<html><body><a href="">click me</a></body></html>`
	got, err := htmlToMarkdown(in, "https://example.com/")
	if err != nil {
		t.Fatalf("htmlToMarkdown: %v", err)
	}
	if strings.Contains(got, "[click me]()") {
		t.Errorf("empty-href link still rendered as link syntax:\n%s", got)
	}
	if !strings.Contains(got, "click me") {
		t.Errorf("link text was dropped entirely:\n%s", got)
	}
}
