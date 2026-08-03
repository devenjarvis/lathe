package anchor

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []Anchor
	}{
		{
			name: "path and lines",
			src:  "```go path=internal/serve/renderer.go lines=264-286\nfunc preprocessCallouts() {}\n```\n",
			want: []Anchor{{Path: "internal/serve/renderer.go", Start: 264, End: 286, Lang: "go", Body: "func preprocessCallouts() {}"}},
		},
		{
			name: "path only",
			src:  "```go path=cmd/store.go\nvar storeCmd = 1\n```\n",
			want: []Anchor{{Path: "cmd/store.go", Lang: "go", Body: "var storeCmd = 1"}},
		},
		{
			name: "single line range",
			src:  "```go path=cmd/store.go lines=12\nvar withVerify bool\n```\n",
			want: []Anchor{{Path: "cmd/store.go", Start: 12, End: 12, Lang: "go", Body: "var withVerify bool"}},
		},
		{
			name: "malformed lines falls back to path only",
			src:  "```go path=cmd/store.go lines=abc\nvar withVerify bool\n```\n",
			want: []Anchor{{Path: "cmd/store.go", Lang: "go", Body: "var withVerify bool"}},
		},
		{
			name: "reversed range falls back to path only",
			src:  "```go path=cmd/store.go lines=30-10\nvar withVerify bool\n```\n",
			want: []Anchor{{Path: "cmd/store.go", Lang: "go", Body: "var withVerify bool"}},
		},
		{
			name: "zero start falls back to path only",
			src:  "```go path=cmd/store.go lines=0-10\nvar withVerify bool\n```\n",
			want: []Anchor{{Path: "cmd/store.go", Lang: "go", Body: "var withVerify bool"}},
		},
		{
			name: "no lang",
			src:  "``` path=Makefile lines=3-4\nall:\n\tgo build\n```\n",
			want: []Anchor{{Path: "Makefile", Start: 3, End: 4, Body: "all:\n\tgo build"}},
		},
		{
			name: "plain fence yields no anchor",
			src:  "```go\nfmt.Println(\"hi\")\n```\n",
			want: nil,
		},
		{
			name: "multiple blocks, only anchored ones counted",
			src: "```go\nplain()\n```\n\ntext\n\n```go path=a.go lines=1-2\nanchored()\n```\n\n" +
				"```sh path=b.sh\nrun\n```\n",
			want: []Anchor{
				{Path: "a.go", Start: 1, End: 2, Lang: "go", Body: "anchored()"},
				{Path: "b.sh", Lang: "sh", Body: "run"},
			},
		},
		{
			name: "four-space indented fence is not an anchor",
			src:  "text\n\n    ```go path=a.go lines=1-2\n    nope()\n    ```\n",
			want: nil,
		},
		{
			// CommonMark dedents an indented fence's content by the fence's own
			// indentation. Rewrite re-emits the body in a zero-indent fence, so
			// the body must arrive already dedented or the rendered block gains
			// leading whitespace on every line.
			name: "three-space indented fence is an anchor, dedented",
			src:  "   ```go path=a.go lines=1-2\n   yes()\n     nested()\n   ```\n",
			want: []Anchor{{Path: "a.go", Start: 1, End: 2, Lang: "go", Body: "yes()\n  nested()"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Parse([]byte(tt.src))
			if len(got) != len(tt.want) {
				t.Fatalf("Parse() returned %d anchors, want %d: %#v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("anchor %d = %#v, want %#v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestRewritePreservesPlainBlocks(t *testing.T) {
	tests := []string{
		"```go\nfmt.Println(\"hi\")\n```\n",
		"Some prose.\n\n```\nno lang here\n```\n\nMore prose.\n",
		"```bash\ncurl -sSfL https://example.com | sh\n```\n",
		"text with no code at all\n",
		"    ```go path=a.go\n    indented\n    ```\n",
	}
	for _, src := range tests {
		got := string(Rewrite([]byte(src)))
		if got != src {
			t.Errorf("Rewrite() modified a plain block.\n got: %q\nwant: %q", got, src)
		}
	}
}

func TestRewriteAnchoredBlock(t *testing.T) {
	src := "```go path=internal/serve/renderer.go lines=264-266\nfunc preprocessCallouts() {}\n```\n"
	got := string(Rewrite([]byte(src)))

	for _, want := range []string{
		`<div class="anchor"`,
		`data-path="internal/serve/renderer.go"`,
		`data-lines="264-266"`,
		`<p class="anchor-path">internal/serve/renderer.go:264-266</p>`,
		"```go\nfunc preprocessCallouts() {}\n```",
		"</div>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Rewrite() output missing %q\ngot:\n%s", want, got)
		}
	}
	// The fence must be blank-line separated from the surrounding raw HTML so
	// CommonMark HTML-block-type-6 re-enables markdown inside the div.
	if !strings.Contains(got, "</p>\n\n```go") {
		t.Errorf("Rewrite() must leave a blank line before the fence\ngot:\n%s", got)
	}
	if !strings.Contains(got, "```\n\n</div>") {
		t.Errorf("Rewrite() must leave a blank line after the fence\ngot:\n%s", got)
	}
	// The info string must not leak the path= attribute to goldmark-highlighting.
	if strings.Contains(got, "```go path=") {
		t.Errorf("Rewrite() leaked path= onto the fence info string\ngot:\n%s", got)
	}
}

func TestRewritePathOnlyBlock(t *testing.T) {
	src := "```go path=cmd/store.go\nvar x = 1\n```\n"
	got := string(Rewrite([]byte(src)))

	if strings.Contains(got, "data-lines") {
		t.Errorf("path-only anchor must emit no data-lines attribute\ngot:\n%s", got)
	}
	if !strings.Contains(got, `<p class="anchor-path">cmd/store.go</p>`) {
		t.Errorf("path-only anchor header should be the bare path\ngot:\n%s", got)
	}
}

func TestRewriteEscapesPath(t *testing.T) {
	src := "```go path=a\"<b>.go lines=1-2\nx()\n```\n"
	got := string(Rewrite([]byte(src)))
	if strings.Contains(got, `<b>`) {
		t.Errorf("Rewrite() must HTML-escape the path\ngot:\n%s", got)
	}
	if !strings.Contains(got, "&lt;b&gt;") {
		t.Errorf("Rewrite() should escape angle brackets in the path\ngot:\n%s", got)
	}
}

func TestParseSingleLineHeader(t *testing.T) {
	got := string(Rewrite([]byte("```go path=a.go lines=7\nx()\n```\n")))
	if !strings.Contains(got, `<p class="anchor-path">a.go:7</p>`) {
		t.Errorf("single-line anchor header should be path:line\ngot:\n%s", got)
	}
	if !strings.Contains(got, `data-lines="7-7"`) {
		t.Errorf("single-line anchor should record a normalized range\ngot:\n%s", got)
	}
}

func TestRewriteDedentsIndentedFence(t *testing.T) {
	// An anchored fence nested in a list item must not render its body with the
	// list indentation baked into every line.
	src := "- item\n\n   ```go path=a.go lines=1-2\n   func x() {}\n   ```\n"
	got := string(Rewrite([]byte(src)))
	if !strings.Contains(got, "```go\nfunc x() {}\n```") {
		t.Errorf("indented fence body was not dedented, got:\n%s", got)
	}
}

func TestDedentStopsAtContentAndHandlesShortLines(t *testing.T) {
	tests := []struct {
		body string
		n    int
		want string
	}{
		{"   a\n   b", 3, "a\nb"},
		{" a\n   b", 3, "a\nb"},       // less indentation than the fence: strip what's there
		{"   a\n\n   b", 3, "a\n\nb"}, // a blank line has nothing to strip
		{"\ta\n\tb", 1, "a\nb"},       // tabs count as one indentation char each
		{"xy\nzw", 3, "xy\nzw"},       // never eats non-whitespace
	}
	for _, tt := range tests {
		if got := string(dedent([]byte(tt.body), tt.n)); got != tt.want {
			t.Errorf("dedent(%q, %d) = %q, want %q", tt.body, tt.n, got, tt.want)
		}
	}
}
