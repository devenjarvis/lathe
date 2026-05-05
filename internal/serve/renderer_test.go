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
	if !strings.Contains(html, "<h1>Hello World</h1>") {
		t.Errorf("RenderMarkdown() missing <h1>, got:\n%s", html)
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
		marker     string
		wantClass  string
		wantLabel  string
	}{
		{"NOTE", "callout-note", "Note"},
		{"TIP", "callout-tip", "Tip"},
		{"WARNING", "callout-warning", "Warning"},
		{"HEADS-UP", "callout-headsup", "Heads up"},
		{"ASIDE", "callout-aside", "Aside"},
		{"DESIGN-NOTE", "callout-designnote", "Design note"},
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
	// Spot-check that both palettes appear: github (light) uses #fff for bg,
	// monokai (dark) uses #f8f8f2 for default fg.
	if !strings.Contains(strings.ToLower(s), "#fff") {
		t.Error("HighlightCSS() missing expected light-theme color")
	}
	if !strings.Contains(strings.ToLower(s), "#f8f8f2") {
		t.Error("HighlightCSS() missing expected dark-theme color")
	}
}
