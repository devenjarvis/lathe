// Package jig is Lathe's library of interactive tutorial blocks.
//
// A jig is a purpose-built helper an agent drops into a tutorial part as a fenced
// block, ```jig <kind>, where prose alone teaches less well: commit to a guess
// before the reveal, scrub a slider through a formula, step a trace. The name is
// the workshop sense: a jig holds the work steady so the hands can learn the cut.
//
// The package owns three things, and the reading page gets the rest for free:
//
//   - the registry of kinds (one Go file per kind registers itself in init),
//   - the shared body grammar (body.go) every kind parses its fence with, and
//   - the rendering contract: server-side HTML that teaches on its own with
//     scripts off, plus an optional browser enhancer (assets/<kind>.js) that the
//     runtime (assets/runtime.js) mounts on it.
//
// A source error never breaks a page: Render falls back to a visible note and the
// original fence, and Check reports the same error with a line number so agents
// can fix it before `lathe store` (`lathe jig check`).
//
// Adding a kind is a Go file with a Kind and its Spec, an optional enhancer in
// assets/, a CSS section in internal/serve/styles.css, and a test. See
// docs/jigs.md.
package jig

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"sort"
	"strings"
)

// Kind is one jig in the library: its fence keyword, the text agents read to
// pick and write it (`lathe jig list`/`show`), and its parser.
type Kind struct {
	// Name is the fence keyword: ```jig <Name>. Lowercase, no spaces.
	Name string
	// Label is the reader-facing name on the jig's kind pill ("Guess first").
	Label string
	// DefaultTitle captions the jig when its source gives no title:.
	DefaultTitle string
	// Summary is one line on what the reader does with it.
	Summary string
	// GoodFor names the moments that call for it, so an agent can choose.
	GoodFor string
	// Fields lists the keys Parse reads, besides the shared title:. It feeds the
	// unknown-field error, so keep it in step with Parse.
	Fields []string
	// Doc is the authoring reference agents read (`lathe jig show <name>`):
	// fields, rules, and what makes a good one. Markdown.
	Doc string
	// Example is a complete, valid fence body. Tests render it, so it can't rot.
	Example string
	// Parse turns a body into a renderable Spec. It reads through the Body
	// accessors; whatever it leaves unread is rejected by the framework.
	Parse func(b *Body) (Spec, error)
}

// Spec is a parsed jig, ready to render.
type Spec interface {
	// RenderBody writes the jig's inner HTML (inside the shared frame). It must
	// teach on its own with scripts off; the enhancer only adds interaction.
	RenderBody(w io.Writer, ctx *Context) error
}

// Context is what a Spec gets from the page it renders into.
type Context struct {
	// ID is unique to this jig on the page ("jig-1", "jig-2", …), for ids and
	// aria references inside it.
	ID string
	// Inline renders one line of markdown (code spans, emphasis, links, math)
	// without a wrapping paragraph. Block renders markdown with paragraphs.
	// Both escape raw HTML. The serve package supplies them; when nil, the text
	// is HTML-escaped as-is.
	Inline func(string) template.HTML
	Block  func(string) template.HTML
}

// InlineHTML renders s with ctx.Inline, or escapes it when there is none.
func (c *Context) InlineHTML(s string) template.HTML {
	if c != nil && c.Inline != nil {
		return c.Inline(s)
	}
	return template.HTML(html.EscapeString(s))
}

// BlockHTML renders s with ctx.Block, or escapes it into a paragraph.
func (c *Context) BlockHTML(s string) template.HTML {
	if c != nil && c.Block != nil {
		return c.Block(s)
	}
	return template.HTML("<p>" + html.EscapeString(s) + "</p>")
}

// Sub returns a child id scoped to this jig: ctx.Sub("ask") → "jig-1-ask".
func (c *Context) Sub(part string) string { return c.ID + "-" + part }

var registry = map[string]*Kind{}

// Register adds a kind to the library. Kinds call it from init; a duplicate or
// incomplete kind is a programming error, so it panics at startup.
func Register(k *Kind) {
	if k.Name == "" || k.Parse == nil || k.Label == "" || k.Example == "" {
		panic(fmt.Sprintf("jig: kind %q is missing Name, Label, Parse, or Example", k.Name))
	}
	if strings.ToLower(k.Name) != k.Name || strings.ContainsAny(k.Name, " \t") {
		panic(fmt.Sprintf("jig: kind name %q must be lowercase with no spaces", k.Name))
	}
	if _, dup := registry[k.Name]; dup {
		panic(fmt.Sprintf("jig: kind %q registered twice", k.Name))
	}
	registry[k.Name] = k
}

// Lookup returns the kind registered under name.
func Lookup(name string) (*Kind, bool) {
	k, ok := registry[name]
	return k, ok
}

// All returns every registered kind, sorted by name.
func All() []*Kind {
	out := make([]*Kind, 0, len(registry))
	for _, k := range registry {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func names() string {
	var n []string
	for _, k := range All() {
		n = append(n, k.Name)
	}
	return strings.Join(n, ", ")
}

// Parsed is a validated jig: its kind, title, and spec.
type Parsed struct {
	Kind  *Kind
	Title string
	Spec  Spec
}

// Parse validates a jig fence. info is the fence's info string after "jig"
// (the kind name, e.g. "guess"); body is the fence content.
func Parse(info, body string) (*Parsed, error) {
	name := strings.ToLower(strings.TrimSpace(info))
	if f := strings.Fields(name); len(f) > 0 {
		name = f[0]
	} else {
		return nil, Errorf(0, "no jig kind: write ```jig <kind>, one of: %s", names())
	}
	k, ok := Lookup(name)
	if !ok {
		return nil, Errorf(0, "unknown jig kind %q; available: %s", name, names())
	}
	b := ParseBody(body)
	// Unknown keys first: a misspelled "explian:" should be reported as the
	// typo it is, not as the "missing explain:" it causes.
	if err := b.checkKeys(k); err != nil {
		return nil, err
	}
	title, _, err := b.Field("title")
	if err != nil {
		return nil, err
	}
	spec, err := k.Parse(b)
	if err != nil {
		return nil, err
	}
	if err := b.Unused(k); err != nil {
		return nil, err
	}
	if strings.TrimSpace(title) == "" {
		title = k.DefaultTitle
	}
	return &Parsed{Kind: k, Title: title, Spec: spec}, nil
}

// Render writes a jig fence as HTML: the shared frame around the kind's body
// when the source is valid, or a visible error note followed by the original
// source when it is not. It only fails when w does.
func Render(w io.Writer, info, body string, ctx *Context) error {
	p, err := Parse(info, body)
	if err != nil {
		return renderError(w, info, body, err)
	}
	// Render into a buffer so a Spec that fails halfway can't leave half a
	// frame on the page.
	var inner bytes.Buffer
	if err := p.Spec.RenderBody(&inner, ctx); err != nil {
		return renderError(w, info, body, err)
	}
	return frameTmpl.Execute(w, frameData{
		ID:    ctx.ID,
		Name:  p.Kind.Name,
		Label: p.Kind.Label,
		Title: ctx.InlineHTML(p.Title),
		Body:  template.HTML(inner.String()),
	})
}

type frameData struct {
	ID, Name, Label string
	Title, Body     template.HTML
}

// The shared frame. A figure named by its caption: the kind pill says what sort
// of thing this is, the title says what it is about. data-jig is the hook the
// runtime mounts enhancers on.
var frameTmpl = template.Must(template.New("frame").Parse(
	`<figure class="jig jig-{{.Name}}" id="{{.ID}}" data-jig="{{.Name}}" aria-labelledby="{{.ID}}-title">` +
		`<figcaption class="jig-head"><span class="jig-kind">{{.Label}}</span> <span class="jig-title" id="{{.ID}}-title">{{.Title}}</span></figcaption>` +
		`<div class="jig-body">{{.Body}}</div></figure>` + "\n"))

var errorTmpl = template.Must(template.New("error").Parse(
	`<div class="jig-error" role="note"><p class="jig-error-label">This interactive block could not be built</p>` +
		`<p class="jig-error-msg">{{.Msg}}</p><pre tabindex="0"><code>{{.Src}}</code></pre></div>` + "\n"))

// renderError keeps the reader's content: the note says what went wrong (the
// author fixes it with `lathe jig check`) and the source follows, so a broken
// guess still shows its question and options.
func renderError(w io.Writer, info, body string, err error) error {
	src := "```jig " + strings.TrimSpace(info) + "\n" + strings.TrimRight(body, "\n") + "\n```"
	return errorTmpl.Execute(w, struct{ Msg, Src string }{err.Error(), src})
}

//go:embed assets/*.js
var assets embed.FS

// Script returns the browser bundle for every registered kind: the runtime,
// each kind's enhancer (assets/<name>.js, optional — a kind whose HTML needs no
// behavior ships none), then the mount call. The reading page inlines it only
// when the part contains a jig (see Present).
func Script() string {
	var b strings.Builder
	rt, err := assets.ReadFile("assets/runtime.js")
	if err != nil {
		panic("jig: runtime.js missing from embed")
	}
	b.Write(rt)
	for _, k := range All() {
		if js, err := assets.ReadFile("assets/" + k.Name + ".js"); err == nil {
			b.WriteString("\n")
			b.Write(js)
		}
	}
	b.WriteString("\nwindow.LatheJig.mountAll(document);\n")
	return b.String()
}

// Present reports whether rendered HTML contains a jig frame, so the page only
// pays for the bundle when it has something to mount.
func Present(renderedHTML []byte) bool {
	return bytes.Contains(renderedHTML, []byte(`data-jig="`))
}

// WriteData embeds v as JSON in a <script type="application/json"> inside the
// jig, for enhancers that need structured data the fallback HTML doesn't carry
// in a convenient shape (a plot's formula, a trace's frames). The runtime reads
// it back with api.data(frame). json.Marshal escapes <, > and &, so the payload
// can't close the script element early.
func WriteData(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, `<script type="application/json" class="jig-data">%s</script>`, data)
	return err
}
