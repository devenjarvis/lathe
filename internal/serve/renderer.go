package serve

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"regexp"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
)

const (
	lightStyle = "github"
	darkStyle  = "monokai"
)

// mermaidBlock matches a fenced code block whose info string is "mermaid".
// Group 1 is the body. Up to three leading spaces of indentation are allowed
// per CommonMark; trailing whitespace on the fence line is tolerated.
var mermaidBlock = regexp.MustCompile("(?ms)^[ \t]{0,3}```[ \t]*mermaid[ \t]*\r?\n(.*?)\r?\n[ \t]{0,3}```[ \t]*$")

func RenderMarkdown(src []byte) ([]byte, error) {
	src = preprocessMermaid(src)
	md := goldmark.New(
		goldmark.WithExtensions(
			highlighting.NewHighlighting(
				highlighting.WithStyle(lightStyle),
				highlighting.WithFormatOptions(
					chromahtml.WithClasses(true),
				),
			),
		),
		goldmark.WithRendererOptions(
			goldmarkhtml.WithUnsafe(),
		),
	)
	var buf bytes.Buffer
	if err := md.Convert(src, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

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
		b.WriteString("\n<div class=\"mermaid\">\n")
		b.WriteString(html.EscapeString(string(sub[1])))
		b.WriteString("\n</div>\n")
		return b.Bytes()
	})
}

func HighlightCSS() (template.CSS, error) {
	formatter := chromahtml.New(chromahtml.WithClasses(true))
	var out bytes.Buffer

	light := styles.Get(lightStyle)
	if light == nil {
		return "", fmt.Errorf("chroma style %q not found", lightStyle)
	}
	if err := formatter.WriteCSS(&out, light); err != nil {
		return "", err
	}

	var darkBuf bytes.Buffer
	dark := styles.Get(darkStyle)
	if dark == nil {
		return "", fmt.Errorf("chroma style %q not found", darkStyle)
	}
	if err := formatter.WriteCSS(&darkBuf, dark); err != nil {
		return "", err
	}
	out.WriteString(scopeCSS(darkBuf.String(), `[data-theme="dark"]`))

	return template.CSS(out.String()), nil
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
