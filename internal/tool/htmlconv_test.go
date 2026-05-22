package tool

import (
	"fmt"
	"strings"
	"testing"
)

func TestStripTags(t *testing.T) {
	input := `<html><body>
<nav><a href="/">NavHome</a></nav>
<header><a href="/login">SignIn</a></header>
<aside>SidebarBlurb</aside>
<form><input name="q"></form>
<h1>Title</h1>
<p>Content here</p>
<footer>Copyright 2024</footer>
<script>alert("xss")</script>
<style>.red { color: red; }</style>
</body></html>`

	result := stripTags(input)

	if strings.Contains(result, "<script>") || strings.Contains(result, "alert") {
		t.Error("script element should be stripped")
	}
	if strings.Contains(result, "<style>") || strings.Contains(result, ".red") {
		t.Error("style element should be stripped")
	}
	// Structural chrome is now stripped — see strippedTags godoc for why.
	for _, junk := range []string{"NavHome", "SignIn", "SidebarBlurb", "Copyright 2024"} {
		if strings.Contains(result, junk) {
			t.Errorf("structural-chrome content %q should be stripped, got:\n%s", junk, result)
		}
	}
	// `<form>` is intentionally preserved (false-positive risk on
	// SPA-style filter UIs).
	if !strings.Contains(result, `name="q"`) {
		t.Errorf("form contents should be preserved, got:\n%s", result)
	}
	if !strings.Contains(result, "<h1>Title</h1>") {
		t.Errorf("h1 should be preserved, got:\n%s", result)
	}
	if !strings.Contains(result, "Content here") {
		t.Errorf("paragraph content should be preserved, got:\n%s", result)
	}
}

func TestStripTagsNested(t *testing.T) {
	input := `<script>var x = 1; <script>nested</script></script><p>Keep</p>`

	result := stripTags(input)

	if strings.Contains(result, "var x") {
		t.Error("nested script content should be stripped")
	}
	if !strings.Contains(result, "Keep") {
		t.Error("content outside script should be preserved")
	}
}

func TestHtmlToMarkdown(t *testing.T) {
	input := `<html><body>
<h1>Welcome</h1>
<p>This is a <strong>test</strong> page.</p>
<ul><li>Item 1</li><li>Item 2</li></ul>
<script>document.write("hidden")</script>
</body></html>`

	md, err := htmlToMarkdown(input, "https://example.com")
	if err != nil {
		t.Fatalf("htmlToMarkdown: %v", err)
	}

	if !strings.Contains(md, "# Welcome") {
		t.Errorf("expected heading, got:\n%s", md)
	}
	if !strings.Contains(md, "**test**") {
		t.Errorf("expected bold, got:\n%s", md)
	}
	if strings.Contains(md, "hidden") {
		t.Error("script content should be stripped")
	}
}

func TestHtmlToMarkdownTruncation(t *testing.T) {
	// Each paragraph is unique so the Phase-17 dup-collapse pass
	// doesn't optimise away the test.
	var b strings.Builder
	b.WriteString("<html><body>")
	for i := range 5000 {
		fmt.Fprintf(&b, "<p>Paragraph number %d with enough text to fill up the output buffer quickly.</p>", i)
	}
	b.WriteString("</body></html>")

	md, err := htmlToMarkdown(b.String(), "")
	if err != nil {
		t.Fatalf("htmlToMarkdown: %v", err)
	}

	if !strings.Contains(md, "truncated") {
		t.Error("expected truncation notice")
	}
}

func TestHtmlToMarkdownRelativeURLs(t *testing.T) {
	input := `<html><body><a href="/docs/api">API Docs</a></body></html>`

	md, err := htmlToMarkdown(input, "https://example.com/page")
	if err != nil {
		t.Fatalf("htmlToMarkdown: %v", err)
	}

	if !strings.Contains(md, "https://example.com/docs/api") {
		t.Errorf("expected absolute URL, got:\n%s", md)
	}
}

func TestHtmlToMarkdownEmptyInput(t *testing.T) {
	md, err := htmlToMarkdown("", "")
	if err != nil {
		t.Fatalf("htmlToMarkdown: %v", err)
	}
	if md != "" {
		t.Errorf("expected empty result, got: %q", md)
	}
}

// TestHtmlToMarkdown_ReadabilityStripsChrome is the regression test for
// the github.com/trending feedback loop: a page whose chrome dwarfs the
// content used to crowd the actual article past the truncation cap.
// With readability extraction in the pipeline the article body is what
// reaches the summarizer.
func TestHtmlToMarkdown_ReadabilityStripsChrome(t *testing.T) {
	// Build an article-shaped page: a long real body wrapped in heavy
	// nav/header/footer chrome. Readability scores by text density, so
	// the body must dominate.
	var body strings.Builder
	body.WriteString("<article><h1>The Real Article</h1>")
	for i := range 40 {
		fmt.Fprintf(&body, "<p>This is genuine article paragraph number %d. It is long enough that readability scores it as the main content of the page, well above the navigation chrome.</p>", i)
	}
	body.WriteString("</article>")

	chrome := `<header><nav><a href="/x">NavLinkA</a><a href="/y">NavLinkB</a></nav></header>
<aside><a href="/z">SidebarLink</a></aside>
<footer><a href="/q">FooterLink</a> Copyright junk text</footer>`

	page := "<html><body>" + chrome + body.String() + chrome + "</body></html>"

	md, err := htmlToMarkdown(page, "https://example.com")
	if err != nil {
		t.Fatalf("htmlToMarkdown: %v", err)
	}

	if !strings.Contains(md, "The Real Article") {
		t.Errorf("article heading should be preserved, got:\n%s", md)
	}
	for _, junk := range []string{"NavLinkA", "NavLinkB", "SidebarLink", "FooterLink"} {
		if strings.Contains(md, junk) {
			t.Errorf("readability should have stripped %q, got:\n%s", junk, md)
		}
	}
}

// TestHtmlToMarkdown_FallsBackWhenNotReadable covers the non-article
// case: short / list-shaped pages where readability declines to extract.
// We must still return the full markdown rather than nothing.
func TestHtmlToMarkdown_FallsBackWhenNotReadable(t *testing.T) {
	// A sparse page that readability is unlikely to score as readable:
	// short, no <article>, mostly links.
	page := `<html><body><h1>Index</h1><ul><li><a href="/a">A</a></li><li><a href="/b">B</a></li></ul></body></html>`

	md, err := htmlToMarkdown(page, "https://example.com")
	if err != nil {
		t.Fatalf("htmlToMarkdown: %v", err)
	}
	if !strings.Contains(md, "Index") {
		t.Errorf("fallback path should return the original content, got:\n%s", md)
	}
}

// TestTryReadabilityExtract_BelowMinReturnsEmpty asserts the threshold
// guard: extractions shorter than readabilityMinBody are treated as
// "extractor confused" and discarded so the caller falls back.
func TestTryReadabilityExtract_BelowMinReturnsEmpty(t *testing.T) {
	tiny := `<html><body><article><p>x</p></article></body></html>`
	if got := tryReadabilityExtract(tiny, nil); got != "" {
		t.Errorf("expected empty extraction for tiny input, got %d bytes:\n%s", len(got), got)
	}
}
