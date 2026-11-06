package jig

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseBodyShapes(t *testing.T) {
	b := ParseBody("fn main()\nask: What now?\n\n- plain\n- [x] marked\n* star\n| a | b |\nnote 1: first\nexplain: one\n  two\n")
	want := []struct {
		kind LineKind
		no   int
	}{{TextLine, 1}, {FieldLine, 2}, {ItemLine, 4}, {ItemLine, 5}, {ItemLine, 6}, {RowLine, 7}, {FieldLine, 8}, {FieldLine, 9}}
	if len(b.Lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %+v", len(b.Lines), len(want), b.Lines)
	}
	for i, w := range want {
		if b.Lines[i].Kind != w.kind || b.Lines[i].No != w.no {
			t.Errorf("line %d = kind %v no %d, want kind %v no %d", i, b.Lines[i].Kind, b.Lines[i].No, w.kind, w.no)
		}
	}
	if b.Lines[3].Checked != true || b.Lines[2].Checked {
		t.Error("only the [x] item should be checked")
	}
	if got := b.Lines[5].Cells; strings.Join(got, ",") != "a,b" {
		t.Errorf("row cells = %q", got)
	}
	if b.Lines[6].Key != "note 1" {
		t.Errorf("numbered key = %q, want \"note 1\"", b.Lines[6].Key)
	}
	if b.Lines[7].Value != "one\ntwo" {
		t.Errorf("continuation = %q, want folded into the field", b.Lines[7].Value)
	}
}

func TestParseBodyDoesNotTreatURLsOrCodeAsFields(t *testing.T) {
	b := ParseBody("https://example.com\nfn f(x: i32)\n")
	for _, l := range b.Lines {
		if l.Kind != TextLine {
			t.Errorf("%q parsed as kind %v, want text", l.Value, l.Kind)
		}
	}
}

func TestFieldRejectsDuplicates(t *testing.T) {
	b := ParseBody("ask: one\nask: two\n")
	if _, _, err := b.Field("ask"); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("duplicate field error = %v, want one naming line 2", err)
	}
}

// Every kind's documented example must build: it is what `lathe jig show`
// hands agents to copy.
func TestEveryKindExampleRenders(t *testing.T) {
	if len(All()) == 0 {
		t.Fatal("no jig kinds registered")
	}
	for _, k := range All() {
		t.Run(k.Name, func(t *testing.T) {
			if _, err := Parse(k.Name, k.Example); err != nil {
				t.Fatalf("example does not parse: %v", err)
			}
			var buf bytes.Buffer
			if err := Render(&buf, k.Name, k.Example, &Context{ID: "jig-1"}); err != nil {
				t.Fatal(err)
			}
			out := buf.String()
			if !strings.Contains(out, `data-jig="`+k.Name+`"`) || strings.Contains(out, "jig-error") {
				t.Errorf("example rendered as an error or without its frame:\n%s", out)
			}
			if k.Summary == "" || k.GoodFor == "" || k.Doc == "" || k.DefaultTitle == "" {
				t.Error("kind is missing catalog text agents rely on")
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name, info, body, want string
	}{
		{"no kind", "", "ask: x", "no jig kind"},
		{"unknown kind", "plot", "y: x", `unknown jig kind "plot"`},
		{"missing ask", "guess", "- a\n- [x] b\nexplain: e", `missing "ask:"`},
		{"missing explain", "guess", "ask: q\n- a\n- [x] b", `missing "explain:"`},
		{"typo beats missing", "guess", "ask: q\n- a\n- [x] b\nexplian: e", `line 4: unknown field "explian:"`},
		{"no answer", "guess", "ask: q\n- a\n- b\nexplain: e", "no option is marked [x]"},
		{"two answers", "guess", "ask: q\n- [x] a\n- [x] b\nexplain: e", "line 3: more than one option"},
		{"one option", "guess", "ask: q\n- [x] a\nexplain: e", "2 to 6 options"},
		{"stray text", "guess", "ask: q\n- a\n- [x] b\nexplain: e\nwhat is this", `line 5: unexpected line "what is this"`},
		{"rows", "guess", "ask: q\n- a\n- [x] b\nexplain: e\n| x |", "line 5: guess takes no | rows"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.info, c.body)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestRenderFallsBackToErrorNoteWithSource(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, "guess", "ask: <b>q</b>\n- a\n- b\nexplain: e", &Context{ID: "jig-1"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `class="jig-error"`) || !strings.Contains(out, "no option is marked [x]") {
		t.Errorf("want an error note naming the problem:\n%s", out)
	}
	if !strings.Contains(out, "ask: &lt;b&gt;q&lt;/b&gt;") || strings.Contains(out, "<b>q</b>") {
		t.Errorf("the source should follow, escaped:\n%s", out)
	}
	if strings.Contains(out, "data-jig=") {
		t.Error("a broken jig must not carry the mount hook")
	}
}

func TestGuessRenderFallback(t *testing.T) {
	var buf bytes.Buffer
	body := "title: Square roots\nask: How wide?\n- same\n- [x] half\n- quarter\nexplain: Because sqrt."
	if err := Render(&buf, "guess", body, &Context{ID: "jig-3"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`<figure class="jig jig-guess" id="jig-3" data-jig="guess" aria-labelledby="jig-3-title">`,
		`<span class="jig-kind">Guess first</span>`,
		`<span class="jig-title" id="jig-3-title">Square roots</span>`,
		`<p class="jig-guess-ask" id="jig-3-ask">How wide?</p>`,
		`<ol class="jig-guess-options" type="A" aria-labelledby="jig-3-ask">`,
		`<li class="jig-guess-option" data-key="B" data-correct="">half</li>`,
		`<li class="jig-guess-option" data-key="C">quarter</li>`,
		`<details class="jig-guess-reveal"><summary>Show the answer</summary>`,
		`<strong>Answer: B.</strong> half`,
		`<p>Because sqrt.</p>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s\nin:\n%s", want, out)
		}
	}
}

func TestGuessDefaultTitle(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, "guess", "ask: q\n- a\n- [x] b\nexplain: e", &Context{ID: "jig-1"}); err != nil {
		t.Fatal(err)
	}
	k, _ := Lookup("guess")
	if !strings.Contains(buf.String(), ">"+k.DefaultTitle+"</span>") {
		t.Errorf("want the default title when none is given:\n%s", buf.String())
	}
}

func TestCheckLocatesProblems(t *testing.T) {
	src := "# T\n\n```jig guess\nask: q\n- a\n- [x] b\nexplain: e\n```\n\n```jig guess\nask: q\n- a\n- b\nexplain: e\nbogus: 1\n```\n\n````markdown\n```jig nope\n```\n````\n"
	if got := len(Blocks([]byte(src))); got != 2 {
		t.Errorf("Blocks found %d jigs, want 2 (the one inside a markdown example is not a jig)", got)
	}
	probs := Check([]byte(src))
	if len(probs) != 1 {
		t.Fatalf("got %d problems, want 1: %v", len(probs), probs)
	}
	// The fence opens on line 10; "bogus:" is body line 5, so source line 15.
	if probs[0].Line != 15 || !strings.Contains(probs[0].String(), `15: jig guess: unknown field "bogus:"`) {
		t.Errorf("problem = %s, want it at line 15", probs[0])
	}
}

func TestCheckWholeBlockProblemPointsAtFence(t *testing.T) {
	probs := Check([]byte("intro\n\n```jig guess\nask: q\n- a\n- b\nexplain: e\n```\n"))
	if len(probs) != 1 || probs[0].Line != 3 {
		t.Fatalf("problems = %v, want one at the fence (line 3)", probs)
	}
}

func TestScriptBundlesRuntimeAndEnhancers(t *testing.T) {
	s := Script()
	for _, want := range []string{"window.LatheJig = {", "window.LatheJig.register('guess'", "window.LatheJig.mountAll(document);"} {
		if !strings.Contains(s, want) {
			t.Errorf("bundle missing %q", want)
		}
	}
	if strings.Index(s, "window.LatheJig = {") > strings.Index(s, "register('guess'") {
		t.Error("the runtime must come before the enhancers that register with it")
	}
	if strings.Contains(s, "</script") {
		t.Error("the bundle is inlined in a <script>; it must not contain a closing tag")
	}
}

func TestWriteDataEscapesScriptClose(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteData(&buf, map[string]string{"x": "</script><b>"}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(buf.String(), "</script>") != 1 {
		t.Errorf("payload closed the script early: %s", buf.String())
	}
}

func TestPresent(t *testing.T) {
	if Present([]byte("<p>no jigs</p>")) {
		t.Error("Present on a page without jigs")
	}
	if !Present([]byte(`<figure class="jig jig-guess" data-jig="guess">`)) {
		t.Error("Present missed a jig frame")
	}
}
