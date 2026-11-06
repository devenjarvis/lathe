package serve_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devenjarvis/lathe/internal/serve"
	"github.com/devenjarvis/lathe/internal/store"
)

const guessFence = "```jig guess\n" +
	"ask: What does `len(xs)` return for $n$ items?\n" +
	"- $n - 1$\n" +
	"- [x] $n$\n" +
	"- <img src=x onerror=alert(1)>\n" +
	"explain: It counts items, *all* of them: $a<b$ stays text.\n" +
	"```\n"

func TestRenderMarkdownJigGuess(t *testing.T) {
	out, err := serve.RenderMarkdown([]byte("# T\n\nPredict first:\n\n" + guessFence))
	if err != nil {
		t.Fatal(err)
	}
	html := string(out)
	for _, want := range []string{
		`<figure class="jig jig-guess" id="jig-1" data-jig="guess"`,
		// Inline markdown in jig text: code spans render, math passes through
		// for KaTeX to typeset.
		`What does <code>len(xs)</code> return for $n$ items?`,
		`data-correct="">$n$</li>`,
		`<em>all</em>`,
		// Math is escaped in jig text (KaTeX reads textContent, so this is
		// still a<b to it), and so is raw HTML.
		`$a&lt;b$`,
		`&lt;img src=x onerror=alert(1)&gt;`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s\nin:\n%s", want, html)
		}
	}
	if strings.Contains(html, "<img") {
		t.Error("raw HTML in jig text must not reach the page")
	}
	if strings.Contains(html, "tutorial-block") {
		t.Error("a jig fence must not also render as a code block")
	}
}

func TestRenderMarkdownJigIdsArePageUnique(t *testing.T) {
	out, err := serve.RenderMarkdown([]byte(guessFence + "\ntext\n\n" + guessFence))
	if err != nil {
		t.Fatal(err)
	}
	html := string(out)
	if !strings.Contains(html, `id="jig-1"`) || !strings.Contains(html, `id="jig-2"`) || !strings.Contains(html, `id="jig-2-ask"`) {
		t.Errorf("want jig-1 and jig-2 with scoped child ids:\n%s", html)
	}
}

func TestRenderMarkdownBrokenJigKeepsContent(t *testing.T) {
	out, err := serve.RenderMarkdown([]byte("before\n\n```jig guess\nask: q\n- a\n- b\nexplain: e\n```\n\nafter\n"))
	if err != nil {
		t.Fatal(err)
	}
	html := string(out)
	if !strings.Contains(html, `class="jig-error"`) || !strings.Contains(html, "ask: q") {
		t.Errorf("a broken jig should render a note plus its source:\n%s", html)
	}
	if !strings.Contains(html, "<p>after</p>") {
		t.Error("the page should continue after a broken jig")
	}
}

func partPage(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	tutDir := filepath.Join(dir, "jigs")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{Slug: "jigs", Title: "Jigs", Status: store.StatusUnverified, Parts: []string{"part-01.md"}}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	serve.NewServer(dir).Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/jigs/part-01.md", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET part = %d", w.Code)
	}
	return w.Body.String()
}

func TestPartPageInlinesJigScriptOnlyWithJigs(t *testing.T) {
	with := partPage(t, "# With\n\n"+guessFence)
	if !strings.Contains(with, "window.LatheJig.register('guess'") || !strings.Contains(with, "window.LatheJig.mountAll(document);") {
		t.Error("a part with a jig should inline the jig bundle")
	}
	without := partPage(t, "# Without\n\nJust prose.\n")
	if strings.Contains(without, "LatheJig") {
		t.Error("a part without jigs should ship no jig code")
	}
}
