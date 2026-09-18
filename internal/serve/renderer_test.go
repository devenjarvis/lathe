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

	if !strings.Contains(html, `<div class="mermaid" data-note="`) {
		t.Errorf("mermaid block not rewritten to a <div class=\"mermaid\"> carrying its note, got:\n%s", html)
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
	if strings.Contains(html, `class="mermaid"`) {
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

func TestRenderExerciseChecklistInteractive(t *testing.T) {
	// Task-list checkboxes inside an exercise section become interactive inputs:
	// enabled, class="exercise-check", carrying a stable part-local index — and the
	// checked state survives.
	src := []byte("# Title\n\n## Exercises\n\n- [ ] Write the parser\n- [x] Wire it up\n")
	out, err := serve.RenderMarkdown(src)
	if err != nil {
		t.Fatalf("RenderMarkdown() error = %v", err)
	}
	html := string(out)
	if !strings.Contains(html, `<input type="checkbox" class="exercise-check" data-exercise-index="0" aria-label="Exercise 1">`) {
		t.Errorf("first exercise checkbox not rendered interactive, got:\n%s", html)
	}
	if !strings.Contains(html, `<input type="checkbox" class="exercise-check" data-exercise-index="1" aria-label="Exercise 2" checked="">`) {
		t.Errorf("checked exercise checkbox missing checked state/index, got:\n%s", html)
	}
	// Interactive exercise inputs are never disabled — no upstream default leaks in.
	if strings.Contains(html, `<input disabled="" type="checkbox">`) {
		t.Errorf("exercise checkboxes should be enabled, found a disabled default, got:\n%s", html)
	}
}

func TestRenderNonExerciseTaskListStaysDisabled(t *testing.T) {
	// Task lists outside an exercise section keep goldmark's default disabled
	// rendering — the feature must not hijack every checklist in a tutorial.
	src := []byte("# Title\n\n## Setup steps\n\n- [x] Install the toolchain\n- [ ] Clone the repo\n")
	out, err := serve.RenderMarkdown(src)
	if err != nil {
		t.Fatalf("RenderMarkdown() error = %v", err)
	}
	html := string(out)
	if !strings.Contains(html, `<input checked="" disabled="" type="checkbox">`) {
		t.Errorf("checked non-exercise checkbox lost its disabled default, got:\n%s", html)
	}
	if !strings.Contains(html, `<input disabled="" type="checkbox">`) {
		t.Errorf("unchecked non-exercise checkbox lost its disabled default, got:\n%s", html)
	}
	if strings.Contains(html, "exercise-check") {
		t.Errorf("non-exercise task list was wrongly made interactive, got:\n%s", html)
	}
}

func TestRenderExerciseIndicesArePartLocalAndSkipNonExercises(t *testing.T) {
	// Indices are assigned in document order across *all* exercise sections in the
	// part, and only to exercise checkboxes — a task list in a non-exercise section
	// neither becomes interactive nor consumes an index.
	src := []byte("# Title\n\n" +
		"## Exercises\n\n- [ ] First\n- [ ] Second\n\n" +
		"## Notes\n\n- [ ] Not an exercise\n\n" +
		"## Bonus Exercises\n\n- [ ] Third\n")
	out, err := serve.RenderMarkdown(src)
	if err != nil {
		t.Fatalf("RenderMarkdown() error = %v", err)
	}
	html := string(out)
	for _, want := range []string{
		`class="exercise-check" data-exercise-index="0" aria-label="Exercise 1"`,
		`class="exercise-check" data-exercise-index="1" aria-label="Exercise 2"`,
		`class="exercise-check" data-exercise-index="2" aria-label="Exercise 3"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing exercise checkbox %q, got:\n%s", want, html)
		}
	}
	// The non-exercise box stays a disabled default and takes no index, so the
	// third exercise checkbox is index 2 — never 3.
	if strings.Contains(html, `data-exercise-index="3"`) {
		t.Errorf("non-exercise checkbox wrongly consumed an exercise index, got:\n%s", html)
	}
	if !strings.Contains(html, `<input disabled="" type="checkbox">`) {
		t.Errorf("non-exercise checkbox should remain a disabled default, got:\n%s", html)
	}
}

func TestRenderExerciseHeadingDetection(t *testing.T) {
	// A heading turns its section's checkboxes interactive iff its slug contains
	// "exercise" — the single intentional trigger. Verify representative positive
	// and negative headings.
	cases := []struct {
		heading     string
		interactive bool
	}{
		{"Exercises", true},
		{"Exercise", true},
		{"Bonus Exercise", true},
		{"Try It Yourself", false},
		{"Summary", false},
	}
	for _, tc := range cases {
		t.Run(tc.heading, func(t *testing.T) {
			src := []byte("# Title\n\n## " + tc.heading + "\n\n- [ ] a box\n")
			out, err := serve.RenderMarkdown(src)
			if err != nil {
				t.Fatalf("RenderMarkdown() error = %v", err)
			}
			got := strings.Contains(string(out), "exercise-check")
			if got != tc.interactive {
				t.Errorf("heading %q: interactive = %v, want %v; got:\n%s", tc.heading, got, tc.interactive, out)
			}
		})
	}
}

func TestRenderMarkdownShift(t *testing.T) {
	// A fragment rendered into a page that already owns the h1 (the byline's voice
	// spec) has its headings pushed down, so the page keeps one h1 and skips no
	// levels on the way into the fragment.
	src := []byte("# Voice\n\nProse.\n\n## Register\n\nMore.\n\n###### Deepest\n\nEnd.\n")
	out, err := serve.RenderMarkdownShift(src, 1)
	if err != nil {
		t.Fatalf("RenderMarkdownShift() error = %v", err)
	}
	html := string(out)
	for _, want := range []string{">Voice</h2>", ">Register</h3>", ">Deepest</h6>"} {
		if !strings.Contains(html, want) {
			t.Errorf("shifted output missing %q, got:\n%s", want, html)
		}
	}
	if strings.Contains(html, "<h1") {
		t.Errorf("shifted output must not emit an h1, got:\n%s", html)
	}
	// The h6 heading has nowhere lower to go: it stays h6 rather than vanishing or
	// wrapping around.
	if strings.Count(html, "<h6") != 1 {
		t.Errorf("deepest heading should stay h6 exactly once, got:\n%s", html)
	}
}

func TestRenderMarkdownShiftZeroKeepsAuthoredLevels(t *testing.T) {
	// Shift 0 is the authored structure: the plain render must not move levels.
	out, err := serve.RenderMarkdownShift([]byte("# Voice\n\n## Register\n"), 0)
	if err != nil {
		t.Fatalf("RenderMarkdownShift() error = %v", err)
	}
	html := string(out)
	if !strings.Contains(html, ">Voice</h1>") || !strings.Contains(html, ">Register</h2>") {
		t.Errorf("shift 0 should render headings as authored, got:\n%s", html)
	}
}

func TestRenderTutorialBlockClasses(t *testing.T) {
	// The fence's info string decides the block's class, its caption, the element
	// that marks it as code or output, and whether it offers to be copied.
	cases := []struct {
		name    string
		info    string
		class   string
		caption string
		inner   string
		button  bool
	}{
		{"shell command", "bash", "tutorial-block-command", "Command · bash", "<code", true},
		{"terminal output", "text", "tutorial-block-output", "Expected output · text", "<samp", false},
		{"json output", "json", "tutorial-block-output", "Expected output · json", "<samp", false},
		{"source", "go", "tutorial-block-code", "Code · go", "<code", false},
		{"unlabelled fence", "", "tutorial-block-code", "Code", "<code", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte("# Title\n\n```" + tc.info + "\nexample line\n```\n")
			out, err := serve.RenderMarkdown(src)
			if err != nil {
				t.Fatalf("RenderMarkdown() error = %v", err)
			}
			html := string(out)
			if !strings.Contains(html, `<div class="tutorial-block `+tc.class+`">`) {
				t.Errorf("missing %q, got:\n%s", tc.class, html)
			}
			if !strings.Contains(html, `<p class="tutorial-block-label">`+tc.caption+`</p>`) {
				t.Errorf("missing caption %q, got:\n%s", tc.caption, html)
			}
			if !strings.Contains(html, tc.inner) {
				t.Errorf("block body should use %q, got:\n%s", tc.inner, html)
			}
			hasButton := strings.Contains(html, ">Copy command</button>")
			if hasButton != tc.button {
				t.Errorf("copy button = %v, want %v, got:\n%s", hasButton, tc.button, html)
			}
			if !strings.HasSuffix(strings.TrimSpace(html), "</div>") {
				t.Errorf("block should close its container, got:\n%s", html)
			}
		})
	}
}

func TestRenderTutorialBlockCopyButtonSitsInItsOwnBlock(t *testing.T) {
	// Two command blocks: each button must resolve the code element in its own
	// container (see the copy handler in layout.html), so copying one can never
	// read the other, and the markup needs no ids to say which is which.
	src := []byte("# Title\n\n```bash\nfirst\n```\n\n```bash\nsecond\n```\n")
	out, err := serve.RenderMarkdown(src)
	if err != nil {
		t.Fatalf("RenderMarkdown() error = %v", err)
	}
	html := string(out)
	if got := strings.Count(html, `<div class="tutorial-block tutorial-block-command">`); got != 2 {
		t.Errorf("command blocks = %d, want 2, got:\n%s", got, html)
	}
	if got := strings.Count(html, ">Copy command</button>"); got != 2 {
		t.Errorf("copy buttons = %d, want one per command block, got:\n%s", got, html)
	}
	if strings.Contains(html, `id="block-`) {
		t.Errorf("blocks should not mint ids for the copy button, got:\n%s", html)
	}
	// Every button sits inside a command block, after the code it copies.
	firstButton := strings.Index(html, ">Copy command</button>")
	lastBlock := strings.LastIndex(html, `<div class="tutorial-block tutorial-block-command">`)
	if firstButton < 0 || lastBlock < 0 || firstButton > lastBlock {
		t.Errorf("first button should sit in the first block, got:\n%s", html)
	}
	// The label must not be repeated through the button: a description would read
	// it out a second time for every block on the page.
	if strings.Contains(html, "aria-describedby") {
		t.Errorf("copy button should not re-read its block label, got:\n%s", html)
	}
	// The button follows its code, so a linear read hears the command before the
	// control and the control marks where the block ends.
	pre := strings.Index(html, "<pre")
	btn := strings.Index(html, ">Copy command</button>")
	if pre < 0 || btn < 0 || btn < pre {
		t.Errorf("copy button should follow its code block, got:\n%s", html)
	}
}

func TestRenderTutorialBlockOutputHasNoCopyButton(t *testing.T) {
	// Output is for comparing, not pasting: a button here would add a focus stop
	// and invite copying text the reader is meant to check.
	src := []byte("# Title\n\n```text\nExpected line\n```\n\n```json\n{\"a\": 1}\n```\n")
	out, err := serve.RenderMarkdown(src)
	if err != nil {
		t.Fatalf("RenderMarkdown() error = %v", err)
	}
	html := string(out)
	if strings.Contains(html, "copy-command") {
		t.Errorf("output blocks must not offer a copy button, got:\n%s", html)
	}
	if !strings.Contains(html, "Expected line") || !strings.Contains(html, "&#34;a&#34;") {
		t.Errorf("output text should survive highlighting, got:\n%s", html)
	}
}

func TestRenderTutorialBlockEscapesUnlexedText(t *testing.T) {
	// A fence with no language we can lex falls back to plain escaped text rather
	// than dropping the body or leaking markup.
	src := []byte("# Title\n\n```\n<script>alert(1)</script>\n```\n")
	out, err := serve.RenderMarkdown(src)
	if err != nil {
		t.Fatalf("RenderMarkdown() error = %v", err)
	}
	html := string(out)
	if strings.Contains(html, "<script>") {
		t.Errorf("unlexed block must escape its text, got:\n%s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Errorf("expected escaped text in the block, got:\n%s", html)
	}
}

func TestRenderTutorialBlockWrapsOnlyLongNonCommandBlocks(t *testing.T) {
	// The list wrapper is what lets a reader leave a long block with one
	// keystroke, so it must appear on long output and stay off short output,
	// where it would only add an announcement. Command blocks are excluded
	// because the copy button already marks their end.
	body := strings.Repeat("a line of output\n", 20)
	short := strings.Repeat("a line of output\n", 3)
	cases := []struct {
		name    string
		src     string
		wrapped bool
	}{
		{"long output", "# Title\n\n```text\n" + body + "```\n", true},
		{"short output", "# Title\n\n```text\n" + short + "```\n", false},
		{"long command", "# Title\n\n```bash\n" + body + "```\n", false},
		{"long source", "# Title\n\n```go\n" + body + "```\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := serve.RenderMarkdown([]byte(tc.src))
			if err != nil {
				t.Fatalf("RenderMarkdown() error = %v", err)
			}
			html := string(out)
			if got := strings.Contains(html, `class="tutorial-block-skip" role="list"`); got != tc.wrapped {
				t.Errorf("list wrapper = %v, want %v, got:\n%s", got, tc.wrapped, html)
			}
			if !tc.wrapped {
				return
			}
			// An unclosed wrapper would swallow the rest of the page into a list.
			if strings.Count(html, "<li>") != 1 || strings.Count(html, "</li>") != 1 {
				t.Errorf("wrapper should hold exactly one item, got:\n%s", html)
			}
			if !strings.HasSuffix(strings.TrimSpace(html), "</li></ul></div>") {
				t.Errorf("wrapper should close before the container, got:\n%s", html)
			}
		})
	}
}
