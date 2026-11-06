package jig

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The body grammar every jig shares. A jig's fence body is a short list of
// lines, each one of four shapes:
//
//	key: value        a field ("ask: …", "note 1: …"); repeatable
//	- text            a list item
//	- [x] text        a marked list item (a right answer, a selected default)
//	| a | b | c       a row of cells (Anatomy's token table, Sources' claims)
//
// Anything else non-blank is a text line (Anatomy's first line is the code it
// dissects). A line indented by two or more spaces continues the field or item
// above it, so a long explanation can wrap. Blank lines are ignored.
//
// One grammar for the whole library keeps the agents' job small: learn it once,
// then each jig only names which keys it reads. It also lets the framework, not
// each jig, reject what a jig did not ask for — a misspelled key is an error with
// a line number, never silently dropped content.

// LineKind says which of the four shapes a body line took.
type LineKind int

const (
	FieldLine LineKind = iota
	ItemLine
	RowLine
	TextLine
)

// Line is one logical line of a jig body (continuations already folded in).
type Line struct {
	// No is the 1-based line number within the fence body.
	No      int
	Kind    LineKind
	Key     string   // FieldLine
	Value   string   // FieldLine value, ItemLine text, TextLine text
	Checked bool     // ItemLine written as "- [x]"
	Cells   []string // RowLine, trimmed
	used    bool
}

// Body is a parsed jig body. A jig's Parse reads what it needs through the
// accessors, which mark lines as used; anything left unread is reported by
// Unused so the framework can refuse it.
type Body struct {
	Lines []*Line
}

var (
	fieldRe = regexp.MustCompile(`^([a-z][a-z0-9_-]*(?: [0-9]+)?):(?:[ \t]+(.*))?$`)
	itemRe  = regexp.MustCompile(`^[-*][ \t]+(?:\[([ xX])\][ \t]+)?(.*)$`)
)

// ParseBody splits a fence body into lines. It never fails: shape errors are the
// jig's to report, because only the jig knows what it expected.
func ParseBody(src string) *Body {
	b := &Body{}
	var last *Line
	for i, raw := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		no := i + 1
		if strings.TrimSpace(raw) == "" {
			continue
		}
		indented := strings.HasPrefix(raw, "  ") || strings.HasPrefix(raw, "\t")
		if indented && last != nil && (last.Kind == FieldLine || last.Kind == ItemLine) {
			if last.Value == "" {
				last.Value = strings.TrimSpace(raw)
			} else {
				last.Value += "\n" + strings.TrimSpace(raw)
			}
			continue
		}
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimLeft(line, " \t")
		var l *Line
		switch {
		case strings.HasPrefix(trimmed, "|"):
			l = &Line{No: no, Kind: RowLine, Cells: splitRow(trimmed)}
		case itemRe.MatchString(trimmed):
			m := itemRe.FindStringSubmatch(trimmed)
			l = &Line{No: no, Kind: ItemLine, Value: strings.TrimSpace(m[2]), Checked: m[1] == "x" || m[1] == "X"}
		case fieldRe.MatchString(trimmed):
			m := fieldRe.FindStringSubmatch(trimmed)
			l = &Line{No: no, Kind: FieldLine, Key: m[1], Value: strings.TrimSpace(m[2])}
		default:
			l = &Line{No: no, Kind: TextLine, Value: line}
		}
		b.Lines = append(b.Lines, l)
		last = l
	}
	return b
}

func splitRow(s string) []string {
	s = strings.TrimPrefix(s, "|")
	s = strings.TrimSuffix(s, "|")
	parts := strings.Split(s, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(p)
	}
	return cells
}

// Field returns the single value of key, marking it used. ok is false when the
// key is absent. A key given more than once is an error: a jig that wants
// repeats reads them with Fields.
func (b *Body) Field(key string) (value string, ok bool, err error) {
	var found *Line
	for _, l := range b.Lines {
		if l.Kind != FieldLine || l.Key != key {
			continue
		}
		if found != nil {
			return "", false, Errorf(l.No, "%q is given more than once (first on line %d)", key, found.No)
		}
		found = l
	}
	if found == nil {
		return "", false, nil
	}
	found.used = true
	return found.Value, true, nil
}

// Required is Field for a key the jig cannot do without.
func (b *Body) Required(key string) (string, error) {
	v, ok, err := b.Field(key)
	if err != nil {
		return "", err
	}
	if !ok || strings.TrimSpace(v) == "" {
		return "", Errorf(0, "missing %q", key+":")
	}
	return v, nil
}

// Fields returns every line for key, in order, marking them used.
func (b *Body) Fields(key string) []*Line {
	var out []*Line
	for _, l := range b.Lines {
		if l.Kind == FieldLine && l.Key == key {
			l.used = true
			out = append(out, l)
		}
	}
	return out
}

// Items returns every list item, in order, marking them used.
func (b *Body) Items() []*Line { return b.take(ItemLine) }

// Rows returns every pipe row, in order, marking them used.
func (b *Body) Rows() []*Line { return b.take(RowLine) }

// Text returns every free text line, in order, marking them used.
func (b *Body) Text() []*Line { return b.take(TextLine) }

func (b *Body) take(kind LineKind) []*Line {
	var out []*Line
	for _, l := range b.Lines {
		if l.Kind == kind {
			l.used = true
			out = append(out, l)
		}
	}
	return out
}

// Unused reports the first line no accessor read, as an error naming what the
// jig does accept. nil means the jig consumed the whole body.
func (b *Body) Unused(k *Kind) error {
	for _, l := range b.Lines {
		if l.used {
			continue
		}
		switch l.Kind {
		case FieldLine:
			return Errorf(l.No, "unknown field %q; %s accepts %s", l.Key+":", k.Name, k.fieldList())
		case ItemLine:
			return Errorf(l.No, "%s takes no list items", k.Name)
		case RowLine:
			return Errorf(l.No, "%s takes no | rows", k.Name)
		default:
			return Errorf(l.No, "unexpected line %q; %s accepts %s", l.Value, k.Name, k.fieldList())
		}
	}
	return nil
}

// checkKeys rejects any field key the kind doesn't declare.
func (b *Body) checkKeys(k *Kind) error {
	allowed := map[string]bool{"title": true}
	for _, f := range k.Fields {
		allowed[f] = true
	}
	for _, l := range b.Lines {
		if l.Kind == FieldLine && !allowed[l.Key] {
			return Errorf(l.No, "unknown field %q; %s accepts %s", l.Key+":", k.Name, k.fieldList())
		}
	}
	return nil
}

func (k *Kind) fieldList() string {
	keys := append([]string{"title"}, k.Fields...)
	sort.Strings(keys)
	for i, key := range keys {
		keys[i] = key + ":"
	}
	return strings.Join(keys, ", ")
}

// Error is a problem in a jig's source. Line is 1-based within the fence body,
// or 0 when the problem is the block as a whole (a missing field).
type Error struct {
	Line int
	Msg  string
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
	}
	return e.Msg
}

// Errorf builds an *Error at a body line (0 for the whole block).
func Errorf(line int, format string, args ...any) *Error {
	return &Error{Line: line, Msg: fmt.Sprintf(format, args...)}
}
