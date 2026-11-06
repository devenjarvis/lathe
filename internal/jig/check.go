package jig

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// Block is a ```jig fence found in a markdown source.
type Block struct {
	// Line is the 1-based line of the opening fence in the source.
	Line int
	// Info is the info string after "jig" (the kind, e.g. "guess").
	Info string
	Body string
}

// Problem is a jig that will not build, located in its source file.
type Problem struct {
	// Line is the 1-based source line the problem points at: the offending body
	// line when the error names one, else the opening fence.
	Line int
	Kind string
	Err  error
}

func (p Problem) String() string {
	kind := p.Kind
	if kind == "" {
		kind = "(no kind)"
	}
	return fmt.Sprintf("%d: jig %s: %s", p.Line, kind, errMsg(p.Err))
}

func errMsg(err error) string {
	if e, ok := err.(*Error); ok {
		return e.Msg
	}
	return err.Error()
}

// Blocks finds every jig fence in a markdown source. It parses with goldmark, as
// the reading page does, so a "```jig" line inside another code block or an
// indented example is not mistaken for a jig.
func Blocks(src []byte) []Block {
	doc := goldmark.New().Parser().Parse(text.NewReader(src))
	var out []Block
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		f, ok := n.(*ast.FencedCodeBlock)
		if !ok || f.Info == nil {
			return ast.WalkContinue, nil
		}
		info := string(f.Info.Segment.Value(src))
		lang, rest, _ := strings.Cut(strings.TrimSpace(info), " ")
		if strings.ToLower(lang) != "jig" {
			return ast.WalkContinue, nil
		}
		out = append(out, Block{
			Line: 1 + bytes.Count(src[:fenceStart(src, f.Info.Segment.Start)], []byte("\n")),
			Info: strings.TrimSpace(rest),
			Body: string(f.Lines().Value(src)),
		})
		return ast.WalkSkipChildren, nil
	})
	return out
}

// fenceStart walks back from the info string to the start of its line.
func fenceStart(src []byte, i int) int {
	for i > 0 && src[i-1] != '\n' {
		i--
	}
	return i
}

// Check validates every jig in a markdown source and returns one Problem per
// jig that would render as an error note.
func Check(src []byte) []Problem {
	var out []Problem
	for _, b := range Blocks(src) {
		if _, err := Parse(b.Info, b.Body); err != nil {
			line := b.Line
			if e, ok := err.(*Error); ok && e.Line > 0 {
				line = b.Line + e.Line
			}
			kind, _, _ := strings.Cut(b.Info, " ")
			out = append(out, Problem{Line: line, Kind: kind, Err: err})
		}
	}
	return out
}
