// Package anchor parses and rewrites "anchored" fenced code blocks — blocks
// whose info string names the repository file the excerpt was taken from:
//
//	```go path=internal/serve/renderer.go lines=264-286
//	…
//	```
//
// An anchor is the durable link between a sentence of prose and the code it
// describes. `lathe drift` reads those links back out of a stored part and asks
// git whether the code still looks the way the guide says it does.
//
// The package is deliberately pure text in, text out: no git, no filesystem, no
// goldmark. Rewrite runs as a renderer preprocess step alongside
// preprocessCallouts/preprocessMermaid, because putting `path=` on the info
// string and letting it reach goldmark-highlighting would collide with that
// extension's own info-string attribute parsing.
package anchor

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// Anchor is one anchored code block: the repo-relative file it came from, the
// 1-indexed line range it covers (zero when the block declared no lines=), the
// fence language, and the block body verbatim.
type Anchor struct {
	Path  string
	Start int
	End   int
	Lang  string
	Body  string
}

// HasRange reports whether the anchor declared a lines= range. A path-only
// anchor can only ever be ok, renamed, or broken — there is no range to move.
func (a Anchor) HasRange() bool {
	return a.Start > 0 && a.End >= a.Start
}

// Label is the human-facing "path:line" header text for the anchor.
func (a Anchor) Label() string {
	switch {
	case !a.HasRange():
		return a.Path
	case a.Start == a.End:
		return fmt.Sprintf("%s:%d", a.Path, a.Start)
	default:
		return fmt.Sprintf("%s:%d-%d", a.Path, a.Start, a.End)
	}
}

// fencedBlock matches a fenced code block, capturing its indentation (group 1),
// info string (group 2), and body (group 3). Up to three leading spaces of
// indentation are allowed per CommonMark, mirroring mermaidBlock/calloutBlock in
// internal/serve/renderer.go. Every fenced block matches; the `path=` filter
// lives in parseInfo so a plain block is still consumed here (and therefore can
// never be mis-paired with a later anchored block's closing fence).
var fencedBlock = regexp.MustCompile("(?ms)^([ \t]{0,3})```([^\r\n`]*)\r?\n(.*?)\r?\n[ \t]{0,3}```[ \t]*$")

// Parse returns every anchored block in src, in document order. Blocks with no
// `path=` in their info string are not anchors and are skipped.
func Parse(src []byte) []Anchor {
	var out []Anchor
	for _, m := range fencedBlock.FindAllSubmatch(src, -1) {
		a, ok := parseInfo(string(m[2]))
		if !ok {
			continue
		}
		a.Body = string(dedent(m[3], len(m[1])))
		out = append(out, a)
	}
	return out
}

// dedent strips up to n leading spaces/tabs from each line, undoing the
// indentation a fence's own opening line carried. CommonMark says the content of
// an indented fence is dedented by the fence's indentation; Rewrite re-emits the
// body inside a zero-indent fence, so without this every line of an indented
// anchored block would render with that indentation baked in.
func dedent(body []byte, n int) []byte {
	if n == 0 {
		return body
	}
	lines := bytes.Split(body, []byte("\n"))
	for i, line := range lines {
		trimmed := 0
		for trimmed < n && trimmed < len(line) && (line[trimmed] == ' ' || line[trimmed] == '\t') {
			trimmed++
		}
		lines[i] = line[trimmed:]
	}
	return bytes.Join(lines, []byte("\n"))
}

// Rewrite replaces every anchored block with a raw-HTML container carrying the
// anchor metadata as data attributes plus a visible path:line header, wrapping a
// plain ```<lang> fence so goldmark-highlighting still highlights the body. The
// container and the fence are blank-line separated so CommonMark's
// HTML-block-type-6 rules re-enable markdown rendering inside the div.
//
// A block with no `path=` is returned byte-for-byte unchanged.
func Rewrite(src []byte) []byte {
	return fencedBlock.ReplaceAllFunc(src, func(match []byte) []byte {
		sub := fencedBlock.FindSubmatch(match)
		if len(sub) < 4 {
			return match
		}
		a, ok := parseInfo(string(sub[2]))
		if !ok {
			return match
		}
		body := dedent(sub[3], len(sub[1]))

		var b bytes.Buffer
		b.WriteString("\n<div class=\"anchor\" data-path=\"")
		b.WriteString(html.EscapeString(a.Path))
		b.WriteString("\"")
		if a.HasRange() {
			fmt.Fprintf(&b, " data-lines=\"%d-%d\"", a.Start, a.End)
		}
		b.WriteString(">\n<p class=\"anchor-path\">")
		b.WriteString(html.EscapeString(a.Label()))
		b.WriteString("</p>\n\n```")
		b.WriteString(a.Lang)
		b.WriteByte('\n')
		b.Write(body)
		if !bytes.HasSuffix(body, []byte("\n")) {
			b.WriteByte('\n')
		}
		b.WriteString("```\n\n</div>\n\n")
		return b.Bytes()
	})
}

// parseInfo pulls the anchor out of a fence info string. The shape is
// "<lang> path=<repo-relative-path> [lines=<start>[-<end>]]"; a leading bare
// word is the language. A malformed lines= value degrades to a path-only anchor
// rather than dropping the anchor entirely — the file link is still useful, and
// silently discarding it would hide the block from drift checking.
func parseInfo(info string) (Anchor, bool) {
	var a Anchor
	for i, f := range strings.Fields(info) {
		switch {
		case i == 0 && !strings.Contains(f, "="):
			a.Lang = f
		case strings.HasPrefix(f, "path="):
			a.Path = strings.TrimPrefix(f, "path=")
		case strings.HasPrefix(f, "lines="):
			if start, end, ok := parseLines(strings.TrimPrefix(f, "lines=")); ok {
				a.Start, a.End = start, end
			}
		}
	}
	if a.Path == "" {
		return Anchor{}, false
	}
	return a, true
}

// parseLines accepts "12" (a single line) or "12-30" (an inclusive range).
// Anything else — non-numeric, zero or negative, or reversed — is rejected.
func parseLines(raw string) (start, end int, ok bool) {
	lo, hi, hasRange := strings.Cut(raw, "-")
	start, err := strconv.Atoi(lo)
	if err != nil || start < 1 {
		return 0, 0, false
	}
	if !hasRange {
		return start, start, true
	}
	end, err = strconv.Atoi(hi)
	if err != nil || end < start {
		return 0, 0, false
	}
	return start, end, true
}
