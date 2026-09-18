package serve

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/gohugoio/hugo-goldmark-extensions/passthrough"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	goldmarkeast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// TOCEntry is a single h2 heading collected for the in-page table of contents
// rendered in the sidebar. ID matches the auto-heading-id slug, so anchor links
// (#first-section) jump directly to the heading.
type TOCEntry struct {
	ID   string
	Text string
}

// Specifically for overriding the goldmark renderer for custom input checkboxes
// within exercise-related output at the bottom of tutorials.
type exerciseCheckboxRenderer struct{}

// Chroma syntax styles, chosen to harmonize with the warm "paper"/"ember"
// palette: tango's muted browns/olives in light, gruvbox's warm ambers/oranges
// in dark. Only the syntax-token hues come from these — the code-block
// container background is owned by our --code-bg token (see pre.chroma in
// styles.css), so chroma's own background never shows through.
const (
	lightStyle = "tango"
	darkStyle  = "gruvbox"
)

// mermaidBlock matches a fenced code block whose info string is "mermaid".
// Group 1 is the body. Up to three leading spaces of indentation are allowed
// per CommonMark; trailing whitespace on the fence line is tolerated.
var mermaidBlock = regexp.MustCompile("(?ms)^[ \t]{0,3}```[ \t]*mermaid[ \t]*\r?\n(.*?)\r?\n[ \t]{0,3}```[ \t]*$")

// calloutBlock matches a GFM-alert-style blockquote whose first line is
// `> [!TYPE]`. Group 1 is the type, group 2 is the body (still blockquote-
// prefixed; preprocessCallouts strips the `> ` from each body line).
var calloutBlock = regexp.MustCompile(`(?m)^[ \t]{0,3}>[ \t]*\[!(NOTE|TIP|WARNING|HEADS-UP|ASIDE|DESIGN-NOTE|PREDICT|RECALL|UNVERIFIED)\][ \t]*\r?\n((?:[ \t]{0,3}>.*(?:\r?\n|$))*)`)

// calloutLineStrip removes the `>` (and one optional following space) from the
// start of each body line of a callout, leaving the inner markdown.
var calloutLineStrip = regexp.MustCompile(`(?m)^[ \t]{0,3}> ?`)

func RenderMarkdown(src []byte) ([]byte, error) {
	out, _, err := RenderMarkdownWithTOC(src)
	return out, err
}

// RenderMarkdownShift renders markdown with every heading pushed down `levels`
// levels (capped at h6). Embedded fragments use it so their headings nest under
// the page's own heading instead of competing with it — the byline's voice-spec
// disclosure is the only caller today, and it must not inject a second h1 into
// a reading page whose article title already owns that level.
func RenderMarkdownShift(src []byte, levels int) ([]byte, error) {
	out, _, err := renderMarkdown(src, levels)
	return out, err
}

// RenderMarkdownWithTOC renders markdown to HTML and returns a list of h2
// headings for the in-page TOC. parser.WithAutoHeadingID assigns each heading
// a stable id slug; the same slug is captured here for anchor links.
func RenderMarkdownWithTOC(src []byte) ([]byte, []TOCEntry, error) {
	return renderMarkdown(src, 0)
}

// PartTitle returns the plain text of the first level-1 heading in a part's
// source, or "" when the part has no h1. The design system names that authored
// heading as the source of truth for a part's title, so the series TOC, the part
// picker, and the prev/next part nav read it from here instead of deriving a
// title from the part's filename.
func PartTitle(src []byte) string {
	doc := goldmark.New().Parser().Parse(text.NewReader(src))
	var title string
	// The walk callback never returns an error, so the Walk result is safe to drop.
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if h, ok := n.(*ast.Heading); ok && h.Level == 1 {
			title = extractInlineText(h, src)
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	return title
}

// renderMarkdown is the shared pipeline: preprocess, parse, then render. A
// positive headingLevels pushes every heading down that many levels (see
// RenderMarkdownShift); 0 renders headings as authored.
func renderMarkdown(src []byte, headingLevels int) ([]byte, []TOCEntry, error) {
	src = preprocessCallouts(src)
	src = preprocessMermaid(src)
	transformers := []util.PrioritizedValue{
		// Tag exercise-section task-list checkboxes (exerciseChecklist) so the
		// node renderer below can emit them as interactive inputs.
		util.Prioritized(exerciseChecklist{}, 100),
	}
	if headingLevels > 0 {
		// After exerciseChecklist: it keys on heading levels, so it must see the
		// document's authored h1/h2 structure.
		transformers = append(transformers, util.Prioritized(headingShift{levels: headingLevels}, 200))
	}
	md := goldmark.New(
		goldmark.WithExtensions(
			// GFM tables — without this, pipe-delimited tables fall through as
			// literal text. styles.css already styles <table>/<th>/<td>.
			extension.Table,
			// SKILL render output format needs to match GitHub Markdown syntax (- [ ])
			extension.TaskList,
			// LaTeX math passes through goldmark untouched — without this,
			// CommonMark backslash-escapes corrupt TeX before it reaches the
			// browser (`\|` -> `|`, `\;` -> `;`). KaTeX auto-render in
			// layout.html picks up the same delimiters client-side. Parsing at
			// the AST level (not a preprocess regex) means $ inside code spans
			// and fenced blocks is naturally left alone.
			passthrough.New(passthrough.Config{
				InlineDelimiters: []passthrough.Delimiters{
					{Open: "$", Close: "$"},
					{Open: `\(`, Close: `\)`},
				},
				BlockDelimiters: []passthrough.Delimiters{
					{Open: "$$", Close: "$$"},
					{Open: `\[`, Close: `\]`},
				},
			}),
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
			parser.WithASTTransformers(transformers...),
		),
		goldmark.WithRendererOptions(
			goldmarkhtml.WithUnsafe(),
			// Override the TaskList extension's checkbox renderer (priority 500).
			// goldmark registers node renderers lowest-number-wins, so 100 takes
			// precedence (see exerciseCheckboxRenderer).
			renderer.WithNodeRenderers(
				util.Prioritized(exerciseCheckboxRenderer{}, 100),
				// Ahead of the highlighting extension's renderer (200): tutorial
				// blocks own the fenced-block wrapper and call Chroma themselves.
				util.Prioritized(&tutorialBlockRenderer{}, 100),
			),
		),
	)
	doc := md.Parser().Parse(text.NewReader(src))
	toc := collectH2TOC(doc, src)
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, doc); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), toc, nil
}

// exerciseCheckboxRenderer replaces goldmark's default task-list checkbox.
// Checkboxes the transformer tagged as exercises render *enabled* with a
// styling/JS hook; every other task checkbox keeps the upstream disabled
// rendering, so task lists elsewhere in a tutorial are unaffected.
func (exerciseCheckboxRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(goldmarkeast.KindTaskCheckBox, renderExerciseCheckbox)
}

func renderExerciseCheckbox(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*goldmarkeast.TaskCheckBox)

	idx, tagged := node.AttributeString("data-exercise-index")
	if !tagged {
		// Outside an exercise section: reproduce the upstream default exactly
		// (disabled, non-XHTML — our renderer config doesn't set XHTML).
		if n.IsChecked {
			_, _ = w.WriteString(`<input checked="" disabled="" type="checkbox"> `)
		} else {
			_, _ = w.WriteString(`<input disabled="" type="checkbox"> `)
		}
		return ast.WalkContinue, nil
	}

	// Exercise checkbox: interactive, carries a stable index for save/restore
	// and an accessible name that matches the on-screen exercise counter.
	exIdx := idx.(int)
	_, _ = fmt.Fprintf(w, `<input type="checkbox" class="exercise-check" data-exercise-index="%d" aria-label="Exercise %d"`, exIdx, exIdx+1)
	if n.IsChecked {
		_, _ = w.WriteString(` checked=""`)
	}
	_, _ = w.WriteString("> ")
	return ast.WalkContinue, nil
}

// exerciseChecklist tags every task-list checkbox that lives inside an exercise
// section with a stable, part-local index, in document order. The renderer reads
// that tag to decide which checkboxes become interactive. Detection keys on the
// heading's auto-generated id slug (assigned during parse, so it's present here).
type exerciseChecklist struct{}

// The class comes from the fence's info string, which is the authoring contract
// AGENTS.md already documents: bash and other shells mean "paste this", text and
// json mean "compare this", and anything else is source to read.
var (
	commandLangs = map[string]bool{
		"bash": true, "sh": true, "zsh": true, "fish": true,
		"powershell": true, "pwsh": true,
	}
	outputLangs = map[string]bool{"text": true, "json": true}
)

// classifyBlock maps an info string to everything the renderer takes from it: the
// container class, the visible caption, the element that carries the text (<samp>
// marks machine output, <code> everything else), and whether the block offers to
// be copied. The caption is a typed label rather than a sentence lifted from the
// surrounding prose: naming the artifact is what the reader needs from the
// renderer, and the prose is already doing the teaching.
//
// There is no caption override, and that is the settled contract rather than a
// gap (lathe-6m7). AGENTS.md already requires visible prose to introduce every
// block, so a source-supplied caption would be a second place to say what that
// prose says, and the reader would hear both. The renderer's job is to name the
// artifact's kind; the author's prose names the particular thing. Reopen only
// with a tutorial that needs a caption the intro prose cannot carry.
func classifyBlock(lang string) (cssClass, caption, tag string, copyable bool) {
	switch {
	case commandLangs[lang]:
		return "tutorial-block-command", "Command · " + lang, "code", true
	case outputLangs[lang]:
		return "tutorial-block-output", "Expected output · " + lang, "samp", false
	case lang == "":
		return "tutorial-block-code", "Code", "code", false
	default:
		return "tutorial-block-code", "Code · " + lang, "code", false
	}
}

// tutorialBlockRenderer groups every fenced block as a named artifact: a labelled
// container, <pre><code> for commands and source, and <pre><samp> for output.
// Command blocks, and only those, get a copy button — output is for comparing,
// and a button on every artifact would add focus stops without adding meaning.
//
// A block long enough that arrowing through it is a chore gets a one-item list
// wrapper. A list is a container every screen reader lets the reader leave with
// one keystroke; a bare <pre> and a <figure> are not, which is what the reader's
// pass with JAWS showed. So the wrapper buys the skip, and the reader also hears
// "list end" at the boundary. Command blocks are left out: their copy button
// already sits at the end, one button-jump away.
//
// A list rather than a blockquote because a blockquote would tell the reader the
// output is someone else's words, and would put machine output into quote
// navigation where quotations from cited sources belong.
//
// The label stays a plain paragraph, read once, in the reader's path. The earlier
// <figure>/<figcaption> pair cost four readings of one command — the figure
// boundary, the caption text, the copy button's description of that same caption,
// and the boundary again on the way out. A figure named by aria-label cuts that
// to two, but it still refuses the skip, so it earns nothing here.
//
// Headings and narration carry the lesson; this only gives the artifact a name.

// longBlockLines is the line count past which a non-command block gets its
// skipping list wrapper. It is a calibration knob: raise it if short blocks start
// sounding chatty, lower it if medium blocks still trap the reader.
const longBlockLines = 15

type tutorialBlockRenderer struct{}

func (r *tutorialBlockRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, r.renderFencedBlock)
}

func (r *tutorialBlockRenderer) renderFencedBlock(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n, ok := node.(*ast.FencedCodeBlock)
	if !ok {
		return ast.WalkContinue, nil
	}

	lang := strings.ToLower(string(n.Language(src)))
	class, caption, tag, copyable := classifyBlock(lang)
	text := string(n.Lines().Value(src))
	skipWrapper := !copyable && strings.Count(text, "\n") >= longBlockLines

	_, _ = fmt.Fprintf(w, `<div class="tutorial-block %s">`, class)
	if skipWrapper {
		// role="list" keeps the list in the accessibility tree once list-style:none
		// drops the markers.
		_, _ = fmt.Fprint(w, `<ul class="tutorial-block-skip" role="list"><li>`)
	}
	_, _ = fmt.Fprintf(w, `<p class="tutorial-block-label">%s</p>`, html.EscapeString(caption))

	writeTutorialBlockBody(w, lang, text, tag)

	// The button renders after the code, so a linear read hears the command before
	// the control that copies it, and so the block's one non-code element sits at
	// the end where it also marks the block's boundary. It reads the text it copies
	// from the code element in its own container (see the copy handler in
	// layout.html), so the markup carries no ids. It is not described by the label:
	// a description would read the label out a second time for every block.
	if copyable {
		_, _ = fmt.Fprint(w, `<button type="button" class="btn btn-ghost btn-sm copy-command">Copy command</button>`)
	}
	if skipWrapper {
		_, _ = fmt.Fprint(w, `</li></ul>`)
	}
	_, _ = w.WriteString("</div>")
	return ast.WalkContinue, nil
}

// blockPreWrapper swaps Chroma's <code> for <samp> on output blocks.
type blockPreWrapper struct{ tag string }

func (p blockPreWrapper) Start(code bool, styleAttr string) string {
	if !code {
		return "<pre tabindex=\"0\"" + styleAttr + ">"
	}
	return "<pre tabindex=\"0\"" + styleAttr + "><" + p.tag + ">"
}

func (p blockPreWrapper) End(code bool) string {
	if !code {
		return "</pre>"
	}
	return "</" + p.tag + "></pre>"
}

// writeTutorialBlockBody writes the highlighted block. It drives Chroma directly
// so the wrapper above can choose the element that carries the text. A fence
// Chroma has no lexer for (bare, or an unknown language) is plain text: the
// fallback lexer keeps one code path for every block, escaping the text the same
// way a highlighted block escapes it.
func writeTutorialBlockBody(w util.BufWriter, lang, text, tag string) {
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, text)
	if err != nil {
		// A lexer that chokes still owes the reader its text, escaped.
		if iterator, err = lexers.Fallback.Tokenise(nil, text); err != nil {
			return
		}
	}
	formatter := chromahtml.New(
		chromahtml.WithClasses(true),
		chromahtml.WithPreWrapper(blockPreWrapper{tag: tag}),
	)
	_ = formatter.Format(w, styles.Get(lightStyle), iterator)
}

// headingShift pushes every heading in the document down by a fixed number of
// levels, capped at h6. It exists for fragments rendered *into* a page that
// already has a heading hierarchy: a voice spec's own "# Name" / "## Section"
// headings would otherwise land ahead of the article's h1 and break the page
// outline (one h1 per page, no skipped levels).
type headingShift struct{ levels int }

func (h headingShift) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	// The walk callback never returns an error, so the Walk result is safe to drop.
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if heading, ok := n.(*ast.Heading); ok {
				heading.Level = min(heading.Level+h.levels, 6)
			}
		}
		return ast.WalkContinue, nil
	})
}

func (exerciseChecklist) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	idx := 0
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		h, ok := n.(*ast.Heading)
		if !ok || h.Level != 2 {
			continue
		}
		idAttr, ok := h.AttributeString("id") // same pattern as collectH2TOC
		if !ok {
			continue
		}
		idBytes, _ := idAttr.([]byte)
		if !isExerciseHeading(string(idBytes)) {
			continue
		}
		// Walk the section body forward until the next h1/h2 boundary.
		for s := h.NextSibling(); s != nil; s = s.NextSibling() {
			if hh, ok := s.(*ast.Heading); ok && hh.Level <= 2 {
				break
			}
			_ = ast.Walk(s, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
				if entering {
					if cb, ok := c.(*goldmarkeast.TaskCheckBox); ok {
						cb.SetAttributeString("data-exercise-index", idx)
						idx++
					}
				}
				return ast.WalkContinue, nil
			})
		}
	}
}

// isExerciseHeading is the single fragile input, check for them explicitly.
// This can be extended later to include things like "bonus", "challenges", etc.
func isExerciseHeading(id string) bool {
	return strings.Contains(id, "exercise")
}

// collectH2TOC walks the parsed AST and returns one TOCEntry per <h2>. h1, h3,
// and deeper levels are skipped — h1 is the page title, h3+ would clutter the
// sidebar. Heading text is the concatenation of inline ast.Text segments, so
// formatting like `code` or *emphasis* contributes plain text only.
func collectH2TOC(doc ast.Node, src []byte) []TOCEntry {
	var toc []TOCEntry
	// The walk callback never returns an error, so the Walk result is safe to drop.
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		h, ok := n.(*ast.Heading)
		if !ok || h.Level != 2 {
			return ast.WalkContinue, nil
		}
		// AutoHeadingID assigns an id to every non-empty heading; a missing id
		// means the heading text was empty (`##` with no content). Skip — there
		// is nothing to label the entry with anyway.
		idAttr, ok := h.AttributeString("id")
		if !ok {
			return ast.WalkContinue, nil
		}
		idBytes, _ := idAttr.([]byte)
		toc = append(toc, TOCEntry{
			ID:   string(idBytes),
			Text: extractInlineText(h, src),
		})
		return ast.WalkSkipChildren, nil
	})
	return toc
}

// extractInlineText concatenates the inline text of all ast.Text descendants of
// n, in document order. Code spans and emphasis nodes contain ast.Text children
// so this captures their visible text without HTML markup.
func extractInlineText(n ast.Node, src []byte) string {
	var b strings.Builder
	// The walk callback never returns an error, so the Walk result is safe to drop.
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if t, ok := c.(*ast.Text); ok {
			b.Write(t.Segment.Value(src))
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// preprocessCallouts rewrites GFM-alert-style blockquotes (lines starting with
// `> [!TYPE]`) into raw <aside> HTML blocks. The body is left as markdown,
// separated by blank lines so goldmark's CommonMark HTML-block-type-6 rules
// re-enable markdown rendering inside.
func preprocessCallouts(src []byte) []byte {
	return calloutBlock.ReplaceAllFunc(src, func(match []byte) []byte {
		sub := calloutBlock.FindSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		kind := strings.ToLower(strings.ReplaceAll(string(sub[1]), "-", ""))
		label := calloutLabel(string(sub[1]))
		body := calloutLineStrip.ReplaceAll(sub[2], nil)
		var b bytes.Buffer
		b.WriteString("\n<aside class=\"callout callout-")
		b.WriteString(kind)
		b.WriteString("\">\n<p class=\"callout-label\">")
		b.WriteString(label)
		b.WriteString("</p>\n\n")
		b.Write(body)
		if !bytes.HasSuffix(body, []byte("\n")) {
			b.WriteByte('\n')
		}
		b.WriteString("\n</aside>\n\n")
		return b.Bytes()
	})
}

func calloutLabel(kind string) string {
	switch kind {
	case "DESIGN-NOTE":
		return "Design note"
	case "HEADS-UP":
		return "Heads up"
	case "NOTE":
		return "Note"
	case "TIP":
		return "Tip"
	case "WARNING":
		return "Warning"
	case "ASIDE":
		return "Aside"
	case "PREDICT":
		return "Predict"
	case "RECALL":
		return "Recall"
	case "UNVERIFIED":
		return "Unverified"
	}
	return kind
}

// diagramUnavailableNote is what a reader sees where a diagram could not be
// drawn, whether scripts are off or a render failed. It travels on the block as
// data-note and inside its <noscript>, so layout.html's loader has no copy of the
// sentence to keep in step.
const diagramUnavailableNote = "This diagram could not be drawn in this browser. The text around it covers the same ground."

// preprocessMermaid rewrites ```mermaid fenced blocks into raw HTML divs that
// the browser-side mermaid library renders into SVG. The body is HTML-escaped
// so labels containing < > & survive intact; the browser un-escapes them when
// mermaid reads textContent.
func preprocessMermaid(src []byte) []byte {
	return mermaidBlock.ReplaceAllFunc(src, func(match []byte) []byte {
		sub := mermaidBlock.FindSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		var b bytes.Buffer
		b.WriteString("\n<div class=\"mermaid\" data-note=\"" + html.EscapeString(diagramUnavailableNote) + "\">\n")
		b.WriteString(html.EscapeString(string(sub[1])))
		b.WriteString("\n</div>\n")
		// Without scripting the block would show its own source, which is not a
		// figure. The prose around every diagram already repeats its content (the
		// diagram contract in AGENTS.md), so name the absence instead.
		b.WriteString(`<noscript><p class="diagram-unavailable">` + diagramUnavailableNote + `</p></noscript>` + "\n")
		return b.Bytes()
	})
}

func HighlightCSS() (template.CSS, error) {
	formatter := chromahtml.New(chromahtml.WithClasses(true))

	light := styles.Get(lightStyle)
	if light == nil {
		return "", fmt.Errorf("chroma style %q not found", lightStyle)
	}
	var lightBuf bytes.Buffer
	if err := formatter.WriteCSS(&lightBuf, light); err != nil {
		return "", err
	}

	dark := styles.Get(darkStyle)
	if dark == nil {
		return "", fmt.Errorf("chroma style %q not found", darkStyle)
	}
	var darkBuf bytes.Buffer
	if err := formatter.WriteCSS(&darkBuf, dark); err != nil {
		return "", err
	}

	// Scope EACH palette to its own theme. The light rules must be scoped too:
	// if they stay global, any token the (less exhaustive) dark style leaves
	// undefined falls through to the light color — often near-black — and is
	// unreadable on the dark code background. Scoping makes undefined tokens
	// fall back to the style's own readable default foreground instead.
	//
	// stripWrapperBackground drops chroma's PreWrapper background so the warm
	// --code-bg token (styles.css) owns the container in both themes; only the
	// syntax-token hues come through.
	var out strings.Builder
	out.WriteString(stripWrapperBackground(scopeCSS(lightBuf.String(), `:root:not([data-theme="dark"])`)))
	out.WriteString(stripWrapperBackground(scopeCSS(darkBuf.String(), `[data-theme="dark"]`)))

	return template.CSS(out.String()), nil
}

// wrapperBackground matches a single `background-color: …;` declaration, used to
// strip chroma's container background from the PreWrapper (.chroma) and .bg
// rules so our --code-bg token controls the code-block background instead.
var wrapperBackground = regexp.MustCompile(`background-color:[^;}]*;?`)

func stripWrapperBackground(css string) string {
	var b strings.Builder
	for _, line := range strings.Split(css, "\n") {
		if strings.Contains(line, ".chroma {") || strings.Contains(line, ".bg {") {
			line = wrapperBackground.ReplaceAllString(line, "")
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// scopeCSS prefixes every CSS rule in src with the given selector. It assumes
// the chroma WriteCSS layout: one rule per line, each starting with a
// "/* ... */" comment followed by selector and declaration block.
func scopeCSS(src, prefix string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if line == "" {
			b.WriteByte('\n')
			continue
		}
		end := strings.LastIndex(line, "*/")
		if end == -1 {
			b.WriteString(line)
			b.WriteByte('\n')
			continue
		}
		b.WriteString(line[:end+2])
		b.WriteByte(' ')
		b.WriteString(prefix)
		b.WriteString(line[end+2:])
		b.WriteByte('\n')
	}
	return b.String()
}
