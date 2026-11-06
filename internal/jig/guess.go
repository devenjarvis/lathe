package jig

import (
	"html/template"
	"io"
)

// Guess first: the reader commits to an answer before the tutorial reveals it.
// It is the multiple-choice form of a [!PREDICT] callout — worth reaching for
// when the wrong answers are real misconceptions, because locking one in and
// being told why it's wrong is what makes the right answer stick.
//
// With scripts off the options read as a lettered list and the answer waits in
// a <details> the reader opens after deciding. guess.js turns the options into
// pressable buttons with a "Lock it in" step and reveals the verdict in place.

const (
	guessMinOptions = 2
	guessMaxOptions = 6
)

func init() {
	Register(&Kind{
		Name:         "guess",
		Label:        "Guess first",
		DefaultTitle: "Commit to an answer before you check",
		Summary:      "The reader picks an answer and locks it in before the explanation is revealed.",
		GoodFor:      "Any subject. Before a Checkpoint or a surprising output; any question whose wrong answers are common misconceptions.",
		Fields:       []string{"ask", "explain"},
		Doc: `Syntax:

    title:   optional caption (defaults to "Commit to an answer before you check")
    ask:     the question, one line of markdown (required)
    - an option                 2 to 6 options, in the order shown
    - [x] the right option      exactly one is marked [x]
    explain: why the right answer is right, revealed after the reader locks in
             (required; indent continuation lines by two spaces)

Rules:

- Every option must be plausible. Each wrong option should be a mistake a
  reader at this level would really make, not filler.
- The answer must be grounded like any other claim in the part: from a source
  you read, or from output you actually produced. Never guess the answer key.
- explain says *why*, in a sentence or three, and names the misconception the
  tempting wrong option comes from. Don't just restate the right option.
- Keep options short (one line). Inline markdown (` + "`code`" + `, *emphasis*,
  $math$) works in ask, options, and explain.
- Introduce the jig with a sentence of prose, like any code block. Use at most
  one or two per part; a quiz on every section is a quiz, not a tutorial.`,
		Example: `ask: A 95% interval from 40 people is ±4.6. With 160 people, roughly how wide is it?
- About ±4.6, the same
- [x] About ±2.3, half as wide
- About ±1.2, a quarter as wide
explain: Width shrinks with the square root of the sample size. Four times the
  people is $\sqrt{4} = 2$, so the interval halves. It does not quarter.`,
		Parse: parseGuess,
	})
}

type guessSpec struct {
	Ask     string
	Options []guessOption
	Correct int
	Explain string
}

type guessOption struct {
	Text    string
	Correct bool
}

func parseGuess(b *Body) (Spec, error) {
	ask, err := b.Required("ask")
	if err != nil {
		return nil, err
	}
	explain, err := b.Required("explain")
	if err != nil {
		return nil, err
	}
	items := b.Items()
	if len(items) < guessMinOptions || len(items) > guessMaxOptions {
		return nil, Errorf(0, "guess needs %d to %d options written as \"- option\", found %d", guessMinOptions, guessMaxOptions, len(items))
	}
	s := &guessSpec{Ask: ask, Explain: explain, Correct: -1}
	for i, it := range items {
		if it.Value == "" {
			return nil, Errorf(it.No, "empty option")
		}
		if it.Checked {
			if s.Correct >= 0 {
				return nil, Errorf(it.No, "more than one option is marked [x]; mark exactly one")
			}
			s.Correct = i
		}
		s.Options = append(s.Options, guessOption{Text: it.Value, Correct: it.Checked})
	}
	if s.Correct < 0 {
		return nil, Errorf(0, "no option is marked [x]; mark the right one as \"- [x] option\"")
	}
	return s, nil
}

const letters = "ABCDEF"

var guessTmpl = template.Must(template.New("guess").Parse(
	`<p class="jig-guess-ask" id="{{.AskID}}">{{.Ask}}</p>` +
		`<ol class="jig-guess-options" type="A" aria-labelledby="{{.AskID}}">` +
		`{{range .Options}}<li class="jig-guess-option" data-key="{{.Key}}"{{if .Correct}} data-correct=""{{end}}>{{.Text}}</li>{{end}}` +
		`</ol>` +
		`<details class="jig-guess-reveal"><summary>Show the answer</summary>` +
		`<p class="jig-guess-answer"><strong>Answer: {{.AnswerKey}}.</strong> {{.AnswerText}}</p>` +
		`<div class="jig-guess-explain">{{.Explain}}</div></details>`))

func (s *guessSpec) RenderBody(w io.Writer, ctx *Context) error {
	type opt struct {
		Key     string
		Text    template.HTML
		Correct bool
	}
	opts := make([]opt, len(s.Options))
	for i, o := range s.Options {
		opts[i] = opt{Key: letters[i : i+1], Text: ctx.InlineHTML(o.Text), Correct: o.Correct}
	}
	return guessTmpl.Execute(w, map[string]any{
		"AskID":      ctx.Sub("ask"),
		"Ask":        ctx.InlineHTML(s.Ask),
		"Options":    opts,
		"AnswerKey":  letters[s.Correct : s.Correct+1],
		"AnswerText": ctx.InlineHTML(s.Options[s.Correct].Text),
		"Explain":    ctx.BlockHTML(s.Explain),
	})
}
