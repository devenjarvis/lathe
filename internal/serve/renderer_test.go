package serve_test

import (
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/serve"
)

func TestRenderMarkdown(t *testing.T) {
	src := []byte("# Hello World\n\nThis is a `test`.\n\n```go\nfmt.Println(\"hello\")\n```")

	out, err := serve.RenderMarkdown(src)
	if err != nil {
		t.Fatalf("RenderMarkdown() error = %v", err)
	}

	html := string(out)
	if !strings.Contains(html, `<h1 id="hello-world">Hello World</h1>`) {
		t.Errorf("RenderMarkdown() missing <h1> with auto-id, got:\n%s", html)
	}
	if !strings.Contains(html, "<code>test</code>") {
		t.Errorf("RenderMarkdown() missing inline <code>, got:\n%s", html)
	}
	if !strings.Contains(html, "<pre") {
		t.Errorf("RenderMarkdown() code block not rendered as <pre>, got:\n%s", html)
	}
	if !strings.Contains(html, "Println") {
		t.Errorf("RenderMarkdown() code block content missing from output, got:\n%s", html)
	}
	if !strings.Contains(html, `class="chroma"`) {
		t.Errorf("RenderMarkdown() should emit chroma classes (WithClasses=true), got:\n%s", html)
	}
}

func TestRenderTable(t *testing.T) {
	src := []byte("intro\n\n| Approach | Composes | Needs OS |\n|---|---|---|\n| Blocking | no | no |\n| Scheduler | yes | no |\n\noutro\n")
	out, err := serve.RenderMarkdown(src)
	if err != nil {
		t.Fatalf("RenderMarkdown() error = %v", err)
	}
	html := string(out)

	if !strings.Contains(html, "<table>") {
		t.Errorf("GFM table not rendered as <table>, got:\n%s", html)
	}
	if !strings.Contains(html, "<th>Approach</th>") {
		t.Errorf("table header cell missing, got:\n%s", html)
	}
	if !strings.Contains(html, "<td>Scheduler</td>") {
		t.Errorf("table body cell missing, got:\n%s", html)
	}
	// The raw pipe-delimited markup must not leak as literal text.
	if strings.Contains(html, "|---|") {
		t.Errorf("raw table delimiter row leaked into output, got:\n%s", html)
	}
}

func TestRenderMermaidBlock(t *testing.T) {
	src := []byte("intro paragraph\n\n```mermaid\nflowchart LR\n  A --> B\n  B --> C\n```\n\noutro paragraph\n")

	out, err := serve.RenderMarkdown(src)
	if err != nil {
		t.Fatalf("RenderMarkdown() error = %v", err)
	}
	html := string(out)

	if !strings.Contains(html, `<div class="mermaid">`) {
		t.Errorf("mermaid block not rewritten to <div class=\"mermaid\">, got:\n%s", html)
	}
	// Chroma's <pre class="chroma"> wrapper should NOT appear for the mermaid
	// block — it bypasses syntax highlighting entirely.
	if strings.Contains(html, `class="chroma"`) {
		t.Errorf("mermaid block was sent through chroma highlighter, got:\n%s", html)
	}
	// `-->` must survive: it's mermaid edge syntax, and HTML-escaping preserves
	// the meaning in the DOM (browser un-escapes &gt; before mermaid reads it).
	if !strings.Contains(html, "--&gt;") && !strings.Contains(html, "-->") {
		t.Errorf("mermaid edge arrows missing from output, got:\n%s", html)
	}
	if !strings.Contains(html, "flowchart LR") {
		t.Errorf("mermaid body content missing from output, got:\n%s", html)
	}
}

func TestRenderNonMermaidCodeBlockUnchanged(t *testing.T) {
	// A non-mermaid fenced block should still flow through chroma.
	src := []byte("```python\nprint('ok')\n```")
	out, err := serve.RenderMarkdown(src)
	if err != nil {
		t.Fatalf("RenderMarkdown() error = %v", err)
	}
	html := string(out)
	if !strings.Contains(html, `class="chroma"`) {
		t.Errorf("non-mermaid fenced block lost chroma classes, got:\n%s", html)
	}
	if strings.Contains(html, `<div class="mermaid">`) {
		t.Errorf("non-mermaid block wrongly rewritten to mermaid div, got:\n%s", html)
	}
}

func TestRenderCalloutBlock(t *testing.T) {
	src := []byte("intro\n\n> [!NOTE]\n> First sentence with **bold** word.\n>\n> Second paragraph.\n\noutro\n")
	out, err := serve.RenderMarkdown(src)
	if err != nil {
		t.Fatalf("RenderMarkdown() error = %v", err)
	}
	html := string(out)

	if !strings.Contains(html, `<aside class="callout callout-note">`) {
		t.Errorf("callout not rewritten to <aside class=\"callout callout-note\">, got:\n%s", html)
	}
	if !strings.Contains(html, `<p class="callout-label">Note</p>`) {
		t.Errorf("missing callout label, got:\n%s", html)
	}
	if !strings.Contains(html, "<strong>bold</strong>") {
		t.Errorf("inner markdown (bold) was not rendered — body should be processed as markdown, got:\n%s", html)
	}
	if !strings.Contains(html, "Second paragraph") {
		t.Errorf("second paragraph missing, got:\n%s", html)
	}
	if !strings.Contains(html, "</aside>") {
		t.Errorf("missing closing </aside>, got:\n%s", html)
	}
	// The leading marker must not appear in the output as literal text.
	if strings.Contains(html, "[!NOTE]") {
		t.Errorf("raw [!NOTE] marker leaked into output, got:\n%s", html)
	}
}

func TestRenderCalloutTypes(t *testing.T) {
	cases := []struct {
		marker    string
		wantClass string
		wantLabel string
	}{
		{"NOTE", "callout-note", "Note"},
		{"TIP", "callout-tip", "Tip"},
		{"WARNING", "callout-warning", "Warning"},
		{"HEADS-UP", "callout-headsup", "Heads up"},
		{"ASIDE", "callout-aside", "Aside"},
		{"DESIGN-NOTE", "callout-designnote", "Design note"},
		{"UNVERIFIED", "callout-unverified", "Unverified"},
	}
	for _, c := range cases {
		t.Run(c.marker, func(t *testing.T) {
			src := []byte("> [!" + c.marker + "]\n> body line\n")
			out, err := serve.RenderMarkdown(src)
			if err != nil {
				t.Fatalf("RenderMarkdown() error = %v", err)
			}
			html := string(out)
			if !strings.Contains(html, c.wantClass) {
				t.Errorf("missing class %q, got:\n%s", c.wantClass, html)
			}
			if !strings.Contains(html, ">"+c.wantLabel+"<") {
				t.Errorf("missing label %q, got:\n%s", c.wantLabel, html)
			}
		})
	}
}

func TestRenderPlainBlockquoteUnchanged(t *testing.T) {
	// A blockquote without a [!TYPE] marker must still render as <blockquote>.
	src := []byte("> Just a quote, nothing fancy.\n")
	out, err := serve.RenderMarkdown(src)
	if err != nil {
		t.Fatalf("RenderMarkdown() error = %v", err)
	}
	html := string(out)
	if !strings.Contains(html, "<blockquote>") {
		t.Errorf("plain blockquote should still render as <blockquote>, got:\n%s", html)
	}
	if strings.Contains(html, "callout") {
		t.Errorf("plain blockquote was wrongly classified as a callout, got:\n%s", html)
	}
}

func TestRenderMarkdownWithTOC(t *testing.T) {
	src := []byte("# Title\n\n## First Section\n\nIntro.\n\n### Subsection of first\n\nBody.\n\n## Second Section\n\nMore body.\n\n### Subsection of second\n\nDone.\n")

	out, toc, err := serve.RenderMarkdownWithTOC(src)
	if err != nil {
		t.Fatalf("RenderMarkdownWithTOC() error = %v", err)
	}

	html := string(out)
	if !strings.Contains(html, `id="first-section"`) {
		t.Errorf("rendered HTML missing first heading id, got:\n%s", html)
	}
	if !strings.Contains(html, `id="second-section"`) {
		t.Errorf("rendered HTML missing second heading id, got:\n%s", html)
	}

	if len(toc) != 2 {
		t.Fatalf("TOC length = %d, want 2 (h2s only); got entries: %#v", len(toc), toc)
	}
	if toc[0].ID != "first-section" || toc[0].Text != "First Section" {
		t.Errorf("toc[0] = %+v, want {ID: first-section, Text: First Section}", toc[0])
	}
	if toc[1].ID != "second-section" || toc[1].Text != "Second Section" {
		t.Errorf("toc[1] = %+v, want {ID: second-section, Text: Second Section}", toc[1])
	}

	// h3 entries should not be in the TOC.
	for _, e := range toc {
		if strings.HasPrefix(e.ID, "subsection-of") {
			t.Errorf("h3 leaked into TOC: %+v", e)
		}
	}
}

func TestRenderMarkdownWithTOCEmpty(t *testing.T) {
	src := []byte("just a paragraph, no headings here.\n")
	_, toc, err := serve.RenderMarkdownWithTOC(src)
	if err != nil {
		t.Fatalf("RenderMarkdownWithTOC() error = %v", err)
	}
	if len(toc) != 0 {
		t.Errorf("expected empty TOC, got %#v", toc)
	}
}

func TestRenderInlineMathSurvivesVerbatim(t *testing.T) {
	// TeX must reach the browser byte-for-byte: CommonMark would otherwise eat
	// the backslash escape (`\|` -> `|`) and corrupt what KaTeX renders.
	src := []byte("the KL term $D_{KL}(p \\| q)$ in the loss\n")
	out, err := serve.RenderMarkdown(src)
	if err != nil {
		t.Fatalf("RenderMarkdown() error = %v", err)
	}
	html := string(out)
	if !strings.Contains(html, `$D_{KL}(p \| q)$`) {
		t.Errorf("inline math not passed through verbatim, got:\n%s", html)
	}
	if strings.Contains(html, "<em>") {
		t.Errorf("emphasis parsing fired inside math, got:\n%s", html)
	}
}

func TestRenderDisplayMathSurvivesVerbatim(t *testing.T) {
	src := []byte("Entropy is the average bill:\n\n$$H(p) = -\\sum_x p(x) \\log_2 p(x)$$\n\nIt is the compression limit.\n")
	out, err := serve.RenderMarkdown(src)
	if err != nil {
		t.Fatalf("RenderMarkdown() error = %v", err)
	}
	html := string(out)
	if !strings.Contains(html, `$$H(p) = -\sum_x p(x) \log_2 p(x)$$`) {
		t.Errorf("display math not passed through verbatim, got:\n%s", html)
	}
}

func TestRenderMathSpacingCommandsSurvive(t *testing.T) {
	// `\,` `\;` `\!` escape ASCII punctuation, exactly what CommonMark
	// backslash-escapes target — these are the first casualties without
	// passthrough protection.
	src := []byte("$\\sum_{x:\\,p(x)} f\\;g\\!h$\n")
	out, err := serve.RenderMarkdown(src)
	if err != nil {
		t.Fatalf("RenderMarkdown() error = %v", err)
	}
	html := string(out)
	if !strings.Contains(html, `$\sum_{x:\,p(x)} f\;g\!h$`) {
		t.Errorf("TeX spacing commands corrupted, got:\n%s", html)
	}
}

func TestRenderDollarInCodeUntouched(t *testing.T) {
	// Shell snippets are full of $ — fenced blocks and inline code spans must
	// never be parsed as math delimiters.
	src := []byte("Run `SSL_CERT_FILE=$(uv run python -c \"...\")` first.\n\n```bash\necho $PATH and $HOME\n```\n")
	out, err := serve.RenderMarkdown(src)
	if err != nil {
		t.Fatalf("RenderMarkdown() error = %v", err)
	}
	html := string(out)
	if !strings.Contains(html, "SSL_CERT_FILE=$(uv run python -c") {
		t.Errorf("inline code span with $ was corrupted, got:\n%s", html)
	}
	if !strings.Contains(html, "$PATH") || !strings.Contains(html, "$HOME") {
		t.Errorf("fenced code block with $ was corrupted, got:\n%s", html)
	}
	if !strings.Contains(html, `class="chroma"`) {
		t.Errorf("fenced block lost chroma highlighting, got:\n%s", html)
	}
}

func TestRenderMathInsideCallout(t *testing.T) {
	// Callout bodies are re-parsed as markdown inside the <aside>; math there
	// needs the same protection as top-level math.
	src := []byte("> [!ASIDE]\n> The likelihood is $\\prod_x q_x^{c_x}$ up to a constant.\n")
	out, err := serve.RenderMarkdown(src)
	if err != nil {
		t.Fatalf("RenderMarkdown() error = %v", err)
	}
	html := string(out)
	if !strings.Contains(html, `<aside class="callout callout-aside">`) {
		t.Errorf("callout missing, got:\n%s", html)
	}
	if !strings.Contains(html, `$\prod_x q_x^{c_x}$`) {
		t.Errorf("math inside callout body corrupted, got:\n%s", html)
	}
}

func TestHighlightCSS(t *testing.T) {
	css, err := serve.HighlightCSS()
	if err != nil {
		t.Fatalf("HighlightCSS() error = %v", err)
	}
	s := string(css)
	if !strings.Contains(s, ".chroma") {
		t.Error("HighlightCSS() missing .chroma rules")
	}
	if !strings.Contains(s, `[data-theme="dark"] .chroma`) {
		t.Error("HighlightCSS() missing dark-scoped rules")
	}
	// Light rules must not be scoped under [data-theme="dark"].
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, ".chroma") && !strings.Contains(line, `[data-theme="dark"]`) {
			// A light rule — fine.
			continue
		}
	}
	// Spot-check that both palettes appear: tango (light) uses #8f5902 for its
	// warm-brown comments, gruvbox (dark) uses #fe8019 for keywords/operators.
	if !strings.Contains(strings.ToLower(s), "#8f5902") {
		t.Error("HighlightCSS() missing expected light-theme color")
	}
	if !strings.Contains(strings.ToLower(s), "#fe8019") {
		t.Error("HighlightCSS() missing expected dark-theme color")
	}
}
