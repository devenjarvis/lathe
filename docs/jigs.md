# Jigs

A **jig** is an interactive block a tutorial can embed where prose alone teaches
less well: commit to a guess before the reveal, scrub a slider through a
formula, step through a trace. The name is the workshop sense: a jig holds the
work steady so the hands can learn the cut.

Agents write a jig as a fenced block in a part:

````markdown
```jig guess
ask: A 95% interval from 40 people is ±4.6. With 160 people, roughly how wide is it?
- About ±4.6, the same
- [x] About ±2.3, half as wide
- About ±1.2, a quarter as wide
explain: Width shrinks with the square root of the sample size. Four times the
  people is $\sqrt{4} = 2$, so the interval halves. It does not quarter.
```
````

and `lathe serve` renders it as a framed, interactive figure.

## Available jigs

| Kind | Label | What the reader does |
| --- | --- | --- |
| `guess` | Guess first | Picks an answer and locks it in before the explanation is revealed. |

`lathe jig list` is the live catalog; `lathe jig show <kind>` prints a kind's
syntax, rules, and a complete example. Those are what the skills read, so this
table is a convenience, not the source of truth.

Designed but not built yet (see the "Jigs" boards in the UI design canvas):
`plot` (a formula with a slider), `trace` (step through code with a call stack),
`checkpoint` (the project's files as they should be now), `path` (play G-code or
any toolpath), `anatomy` (click through the pieces of a signature), `compare`
(the same idea in a language the reader knows), and `sources` (a part's claims
against its research trail).

## How it fits together

```
part-01.md ── ```jig guess ──▶ internal/serve/renderer.go (fenced-block renderer)
                                   │  lang == "jig"
                                   ▼
                              internal/jig.Render
                                   │  Parse: shared body grammar → kind's Parse → Spec
                                   │  ok:  frame + Spec.RenderBody (works with scripts off)
                                   │  err: error note + the original source
                                   ▼
                  <figure class="jig jig-guess" data-jig="guess">…</figure>
                                   │
   layout.html inlines jig.Script() only when the part has a jig (jig.Present)
                                   ▼
          assets/runtime.js → LatheJig.mountAll → assets/guess.js enhancer
```

- **`internal/jig`** owns the library: the registry (`Register`, `Lookup`,
  `All`), the shared body grammar (`body.go`), the frame and error fallback
  (`Render`), the checker (`Blocks`, `Check`), and the browser bundle
  (`Script`). It knows nothing about HTTP or the page.
- **`internal/serve`** hooks the fence into the markdown renderer
  (`serve/jig.go`), supplies inline/block markdown rendering for jig text, and
  inlines the bundle on parts that need it.
- **`cmd/jig.go`** is the agent surface: `lathe jig list`, `lathe jig show
  <kind>`, and `lathe jig check <file-or-dir>…`.
- **Styles** live in `internal/serve/styles.css` with the rest of the design
  system: the shared `.jig` frame, then one section per kind.

### Principles

- **The server HTML teaches on its own.** With scripts off, a jig must still be
  useful: Guess first renders a lettered list and a `<details>` answer. The
  enhancer only adds interaction. A kind may ship no script at all.
- **One body grammar.** Every kind reads the same four line shapes (below), so
  agents learn the syntax once and each kind only names the keys it reads.
- **Strict parsing, graceful rendering.** An unknown field, a stray line, or a
  missing required field is an error with a line number. On the page the error
  becomes a visible note plus the original source, so a typo never takes the
  page down and the content is never silently dropped. `lathe jig check`
  reports the same errors before `lathe store`.
- **Jig text is data, not markup.** Questions, options, and explanations render
  as markdown (code spans, emphasis, links, `$math$`) but raw HTML is escaped.
- **Small bundle, only when needed.** The runtime plus every enhancer is
  inlined, and only on parts that contain a jig. No extra requests, fully
  offline.

## Body grammar

Each line of a jig body is one of:

| Shape | Example | Read with |
| --- | --- | --- |
| field | `ask: What prints?` (also `note 1: …`) | `b.Field`, `b.Required`, `b.Fields` |
| list item | `- an option`, `- [x] the marked one` | `b.Items()` (`Line.Checked`) |
| row | `\| fn \| keyword \| Declares a function` | `b.Rows()` (`Line.Cells`) |
| text | anything else, e.g. a line of code | `b.Text()` |

A line indented by two or more spaces continues the field or item above it.
Blank lines are ignored. `title:` is shared by every kind and handled by the
framework (falling back to the kind's `DefaultTitle`).

## Adding a jig

1. **`internal/jig/<kind>.go`**: in `init`, `Register(&Kind{…})` with `Name`
   (the fence keyword), `Label`, `DefaultTitle`, `Summary`, `GoodFor`, `Fields`
   (every key `Parse` reads besides `title`), `Doc` (the authoring reference
   agents read — syntax, rules, and what makes a good one), `Example` (a
   complete valid body), and `Parse`.
2. **`Parse(b *Body) (Spec, error)`**: read what you need through the `Body`
   accessors and return `Errorf(line, …)` for problems. Don't worry about
   leftovers; the framework rejects unread lines and undeclared keys.
3. **`Spec.RenderBody(w, ctx)`**: write the inner HTML. Use
   `ctx.InlineHTML`/`ctx.BlockHTML` for author text and `ctx.Sub("x")` for ids.
   Make it work with scripts off. If the enhancer needs structured data the
   markup doesn't carry, write it with `WriteData(w, v)` and read it back with
   `api.data(frame)`.
4. **`internal/jig/assets/<kind>.js`** (optional):
   `window.LatheJig.register('<kind>', function(frame, api){ … })`. Build the
   interactive DOM from the fallback markup, and leave the fallback intact if
   anything it needs is missing. Keep any element a page-wide pass must reach
   (KaTeX typesetting runs after mount) in the document, hidden if necessary.
5. **`internal/serve/styles.css`**: a section for `.jig-<kind>` using design
   tokens only, checked in both themes. Style the enhanced state under
   `[data-jig-ready].jig-<kind>`.
6. **Tests**: `TestEveryKindExampleRenders` already covers the example; add
   parse-error and rendered-markup cases beside it (see `jig_test.go`).

No skill edit is needed: the skills read `lathe jig list`/`show`, so a new kind
is available to agents as soon as it ships. Add it to the table above.
