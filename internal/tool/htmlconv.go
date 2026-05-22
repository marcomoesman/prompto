package tool

import (
	"fmt"
	"net/url"
	"strings"

	readability "codeberg.org/readeck/go-readability/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// htmlMaxOutput caps the markdown we hand to the summarizer. Sized
// generously because the summarizer LLM call is the costly step — we'd
// rather feed it 200KB once than re-fetch a too-aggressively-trimmed
// page. Readability extraction (below) usually shrinks the body well
// under this; the cap is a safety net for pages where extraction
// declined to fire and we fall through to the full-document path.
const htmlMaxOutput = 200 * 1024 // 200KB

// readabilityMinBody is the minimum rendered-HTML length we'll accept
// from readability extraction. Below this we assume the extractor
// misidentified the article (e.g. on index/dashboard pages with no
// long-prose body) and fall back to the full-page conversion path.
const readabilityMinBody = 512

// strippedTags are HTML elements removed before markdown conversion.
// Two categories:
//
//   - Always-noise: script/style/noscript/svg never contain useful
//     content for an LLM and would otherwise leak code or vector data
//     into the markdown.
//   - Structural chrome: nav/header/footer/aside. These wrap navigation,
//     sign-in CTAs, language pickers, and copyright boilerplate. The
//     summarizer LLM call is expensive and for nav-heavy pages
//     (github.com/trending was the canonical example) the chrome can
//     dwarf the actual content and crowd it past the truncation cap.
//     Readability extraction handles this for article-shaped pages;
//     this fallback strip catches index/list/dashboard pages where
//     readability declines to fire.
//
// `<form>` is intentionally NOT stripped: server-rendered apps
// sometimes wrap result lists or content in a form for filtering, so
// the false-positive risk outweighs the marginal cleanup.
var strippedTags = map[atom.Atom]bool{
	atom.Script:   true,
	atom.Style:    true,
	atom.Noscript: true,
	atom.Svg:      true,
	atom.Nav:      true,
	atom.Header:   true,
	atom.Footer:   true,
	atom.Aside:    true,
}

// stripTags removes the elements in strippedTags from the HTML, plus
// any `<img>` whose `alt` attribute is empty or missing. Decorative-
// image stripping is what lets
// WithLinkEmptyContentBehavior(LinkBehaviorSkip) downstream catch
// `<a><img alt=""></a>` patterns: with the image gone, the link is
// genuinely empty-content and gets skipped.
func stripTags(htmlContent string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(htmlContent))
	var b strings.Builder
	skipDepth := 0

	// shouldDropImg reports whether the current image token has no
	// useful alt text. The token's attributes are read from the
	// tokenizer's per-attribute iterator.
	shouldDropImg := func() bool {
		var (
			alt    string
			hasAlt bool
		)
		for {
			key, val, more := tokenizer.TagAttr()
			if string(key) == "alt" {
				alt = strings.TrimSpace(string(val))
				hasAlt = true
			}
			if !more {
				break
			}
		}
		return !hasAlt || alt == ""
	}

	for {
		tt := tokenizer.Next()
		if tt == html.ErrorToken {
			break
		}

		raw := string(tokenizer.Raw())

		switch tt {
		case html.StartTagToken:
			tn, _ := tokenizer.TagName()
			a := atom.Lookup(tn)
			if strippedTags[a] {
				skipDepth++
				continue
			}
			// `<img>` is *technically* a void element but some HTML
			// in the wild emits it as a start tag. Drop the start
			// AND its closing partner (we don't track depth — img
			// has no real children).
			if a == atom.Img && shouldDropImg() {
				continue
			}
			if skipDepth == 0 {
				b.WriteString(raw)
			}

		case html.EndTagToken:
			tn, _ := tokenizer.TagName()
			a := atom.Lookup(tn)
			if strippedTags[a] {
				if skipDepth > 0 {
					skipDepth--
				}
				continue
			}
			if a == atom.Img {
				// Pair to the dropped start tag — also dropped.
				continue
			}
			if skipDepth == 0 {
				b.WriteString(raw)
			}

		case html.SelfClosingTagToken:
			tn, _ := tokenizer.TagName()
			a := atom.Lookup(tn)
			if strippedTags[a] {
				continue
			}
			if a == atom.Img && shouldDropImg() {
				continue
			}
			if skipDepth == 0 {
				b.WriteString(raw)
			}

		default:
			if skipDepth == 0 {
				b.WriteString(raw)
			}
		}
	}

	return b.String()
}

// markdownConverter is constructed once at package init with the
// Phase-17 cleanup options applied:
//
//   - LinkBehaviorSkip on empty-content links so an `<a><img alt=""></a>`
//     pattern (decorative image inside a link) doesn't render as the
//     `[![](url)]` artifact — the link is skipped, leaving just the
//     image (or nothing if the image had no alt and no src).
//   - LinkBehaviorSkip on empty-href links so `<a href="">text</a>`
//     renders as plain text instead of `[text]()`.
var markdownConverter = converter.NewConverter(
	converter.WithPlugins(
		base.NewBasePlugin(),
		commonmark.NewCommonmarkPlugin(
			commonmark.WithLinkEmptyContentBehavior(commonmark.LinkBehaviorSkip),
			commonmark.WithLinkEmptyHrefBehavior(commonmark.LinkBehaviorSkip),
		),
	),
)

// htmlToMarkdown converts a fetched HTML page to markdown for the
// summarizer LLM. The pipeline is:
//
//  1. Readability extraction (Mozilla Readability.js port) — reduces
//     the document to its main article body, dropping nav, header,
//     footer, sidebar, ads, and other chrome at the structural level.
//     This is the primary defence against pages whose chrome is so
//     large it crowds out the actual content (e.g. github.com/trending
//     hides its repo cards behind ~50KB of nav + filter UI).
//  2. Fallback to the full document when readability declines (index
//     pages, dashboards, JSON endpoints rendered as HTML) or returns
//     too little content. The fallback path still benefits from
//     stripTags' script/style/noscript/svg removal.
//  3. html-to-markdown/v2 conversion with link-empty-skip options.
//  4. cleanupMarkdown for cosmetic warts the library can't address
//     (carousel duplication, lone `\` continuation lines).
//  5. Hard cap at htmlMaxOutput to bound payload size.
//
// sourceURL is used to resolve relative URLs in the output and is
// passed to the readability extractor for its base-URL handling.
func htmlToMarkdown(htmlContent string, sourceURL string) (string, error) {
	var pageURL *url.URL
	if sourceURL != "" {
		if u, err := url.Parse(sourceURL); err == nil {
			pageURL = u
		}
	}

	body := htmlContent
	if extracted := tryReadabilityExtract(htmlContent, pageURL); extracted != "" {
		body = extracted
	}

	cleaned := stripTags(body)

	var opts []converter.ConvertOptionFunc
	if pageURL != nil && pageURL.Host != "" {
		opts = append(opts, converter.WithDomain(pageURL.Scheme+"://"+pageURL.Host))
	}

	mdBytes, err := markdownConverter.ConvertString(cleaned, opts...)
	if err != nil {
		return "", fmt.Errorf("converting HTML to markdown: %w", err)
	}
	md := cleanupMarkdown(mdBytes)

	if len(md) > htmlMaxOutput {
		md = truncateOnRune(md, htmlMaxOutput) + fmt.Sprintf("\n\n[Content truncated at %dKB]", htmlMaxOutput/1024)
	}

	return md, nil
}

// tryReadabilityExtract runs readability on the input HTML and returns
// the rendered article body. Returns "" when the extractor errored,
// produced no Node, or returned a body shorter than readabilityMinBody
// — in those cases the caller should fall back to the original HTML
// rather than trust an unreliable extraction.
func tryReadabilityExtract(htmlContent string, pageURL *url.URL) string {
	article, err := readability.FromReader(strings.NewReader(htmlContent), pageURL)
	if err != nil || article.Node == nil {
		return ""
	}
	var buf strings.Builder
	if err := article.RenderHTML(&buf); err != nil {
		return ""
	}
	out := buf.String()
	if len(out) < readabilityMinBody {
		return ""
	}
	return out
}

// cleanupMarkdown post-processes the converter output to remove the
// cosmetic warts our hooks can't catch:
//
//  1. **Lone `\` continuation lines** — the library escapes blank
//     lines inside multi-line link content with a single backslash
//     (CommonMark requires this, otherwise the blank line would
//     close the link). Outside of a link context the model just
//     sees stray `\` lines; we drop them.
//  2. **Repeated identical paragraphs** — carousels and ticker
//     animations duplicate text in HTML so the loop appears
//     seamless. The markdown faithfully preserves it. We collapse
//     2+ consecutive identical lines (after trim) to one.
//  3. **Excess blank lines** — collapse 3+ newlines to 2 (existing
//     behaviour, preserved).
//  4. **Leading / trailing whitespace** — final trim.
func cleanupMarkdown(in string) string {
	lines := strings.Split(in, "\n")
	out := make([]string, 0, len(lines))
	var lastNonEmpty string
	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)

		// 1. Drop lone `\` lines (link-internal blank-line escape
		// leak).
		if trimmed == `\` {
			continue
		}

		// 2. Collapse repeated identical paragraphs. Comparison is
		// against the most recent non-empty line; blank lines
		// between repeats still count as separators (so we collapse
		// "X\n\nX" → "X" but leave "X\nY\nX" alone).
		if trimmed != "" && trimmed == lastNonEmpty {
			continue
		}
		if trimmed != "" {
			lastNonEmpty = trimmed
		}
		out = append(out, raw)
	}
	md := strings.Join(out, "\n")

	// 3. Collapse 3+ blank lines to 2.
	for strings.Contains(md, "\n\n\n") {
		md = strings.ReplaceAll(md, "\n\n\n", "\n\n")
	}

	// 4. Trim outer whitespace.
	return strings.TrimSpace(md)
}
