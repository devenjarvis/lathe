# Lathe design system — "Editorial textbook"

The reading UI is a local web server for long, hands-on technical tutorials. The
design goal is to make dense technical prose *and* code pleasant to read: a warm,
low-fatigue "paper" palette, serif reading body, characterful display serif
headings, and a single warm accent. Think *Crafting Interpreters* meets a
literary magazine.

Everything here lives in **one source of truth**: `internal/serve/styles.css`
(tokens + components) and `internal/serve/components.html` (shared Go template
partials). Change a value once and every page picks it up.

## How it's wired

- `styles.css` is `go:embed`'d into `server.go` as `stylesCSS`, exposed to
  templates as `.CSS`, and injected inline by the `{{define "head"}}` partial as
  `<style>{{.CSS}}{{.HighlightCSS}}</style>`. Inline injection (like
  `HighlightCSS`) means **no extra request and no flash of unstyled content**.
- The theme (`data-theme="light|dark"`) is set by a pre-render `<script>` in the
  `head` partial *before* first paint, so there's no flash of the wrong theme.
- Fonts are latin-subset `woff2`, `go:embed`'d and served locally from
  `/_static/*.woff2` — the UI is **100% offline**, no external requests. They use
  `font-display: swap` over serif fallbacks, so first paint never blocks on a
  font download.

## Tokens

All colors/spacing/type are CSS custom properties on `:root` (light "paper") and
overridden under `[data-theme="dark"]` (dark "ember"). Components reference the
tokens only — never raw hex.

### Color — surfaces & ink

| Token | Light (paper) | Dark (ember) | Use |
|---|---|---|---|
| `--bg` | `#FBF7F0` | `#16130F` | page background |
| `--surface` | `#FFFDF8` | `#1E1A15` | cards, sidebar, drawer |
| `--surface-sunken` | `#F3ECE0` | `#13100C` | hovers, table headers |
| `--border` | `#E4DAC9` | `#332C23` | hairlines |
| `--border-strong` | `#D6C9B4` | `#463D31` | blockquote rule, emphasis borders |
| `--text` | `#1B1714` | `#E9E1D3` | primary ink |
| `--text-muted` | `#4A4038` | `#C3B7A4` | body prose |
| `--text-subtle` | `#6B5F53` | `#9C8F7C` | nav, secondary |
| `--text-faint` | `#8A7C6C` | `#7C7060` | meta, labels |
| `--accent` | `#B5451F` (rust) | `#E89A5C` (amber) | links, primary buttons, active state |
| `--accent-hover` | `#9A3817` | `#F2AE73` | hover of accent |
| `--accent-soft` | `#F6E7DC` | `#2A2017` | active/selected tints |
| `--code-bg` | `#F3ECE0` | `#1B1712` | code block + inline code |
| `--code-text` | `#2A2018` | `#E5DCCB` | code default ink |

### Color — status badges (`-bg` / `-text`)

Five rendered states (a sixth, `unverified`, renders no badge). Warm-retoned so
nothing screams against the paper.

`--badge-verified-*` (olive green) · `--badge-verifying-*` (amber) ·
`--badge-failed-*` (rust) · `--badge-extending-*` (dusty blue) ·
`--badge-skipped-*` (neutral).

### Color — callouts (`-bg` / `-border` / `-label`)

Eight color groups (the `warning` and `headsup` classes share the warn triple):
`note` (dusty blue), `tip` (olive green), `warn` (amber), `design` (violet-gray),
`aside` (transparent, italic), `predict` (berry), `recall` (orange),
`unverified` (rust — for `[!UNVERIFIED]`, a load-bearing claim the generator
couldn't ground in a source).

### Typography

| Role | Family stack | Notes |
|---|---|---|
| Display (headings) | `--font-display` = `'Fraunces', 'Iowan Old Style', Georgia, serif` | variable woff2, `font-optical-sizing: auto`, tight leading (~1.18), `-0.01em` tracking |
| Body (prose) | `--font-body` = `'Newsreader', 'Iowan Old Style', Charter, Georgia, serif` | roman + italic woff2; emphasis uses real Newsreader italic |
| Code | `--font-mono` = `'JetBrains Mono', 'Fira Code', ui-monospace, monospace` | regular + bold |

Reading measure is `--measure: 66ch`; the reading column (`main`) is bound to it,
body is **1.125rem / 1.7** line-height. Type scale (fluid `clamp`): h1 ~2.1rem,
h2 ~1.5rem, h3 ~1.2rem, body 1.125rem, small/meta ~0.85rem. Links are accent-
colored with a thin offset underline.

### Spacing / radius / shadow / motion

`--space-1..8` (0.25rem → 4rem) · `--radius-sm|md|lg` (4/8/12px) ·
`--shadow-sm|md` · `--transition` (120ms ease-out).

## Component catalog

Each component is a class in `styles.css`. Markup it expects:

| Component | Class / selector | Markup source |
|---|---|---|
| Button | `.btn` + `.btn-primary`/`.btn-ghost`/`.btn-danger` (+ `.btn-pill`/`.btn-sm`) | layout.html action buttons |
| Status badge | `.badge.<status>` | `{{template "badge" <Status>}}` (components.html) |
| Theme toggle | `.theme-toggle[data-theme-toggle]` | `{{template "themeToggle" .}}` |
| Sidebar nav | `nav`, `.back-link`, `.toc-label`, `ul.toc` | layout.html |
| Series TOC | `.series-toc`, `.current-row`, `.part-num` | layout.html |
| Part nav | `.part-nav`, `.prev`/`.next`, `.label`, `.title` | layout.html |
| Verify / extend | `.verify-section`, `.extend-section`, `#verifyForm`, `#extendForm` | layout.html |
| Author byline | `.article-byline`, `.voice-reveal`, `.voice-reveal-body` (+ `code`) | layout.html (under the `.article-header` `<h2>`) |
| Ask drawer | `#askDrawer`, `.ask-bubble`, `.ask-question`, `.ask-answer` | layout.html |
| Progress bar | `#progressBar` | layout.html |
| List cards | `body.list`, `.tutorial`, `.meta`, `.delete-btn`, `.empty` | list.html |
| Callouts | `.callout.callout-<type>`, `.callout-label` | emitted by `renderer.go` |
| Code blocks | `pre`, `pre.chroma`, `code` | goldmark + chroma |

The `head`, `badge`, and `themeToggle` partials are parsed into **both** the
layout and list template sets (see `NewServer` in `server.go`).

The **author byline** (`.article-byline`) sits directly under the title as a
quiet metadata line (`--text-subtle`, no new colors): it always names the LLM
model that authored the tutorial ("Generated by `<model>`", falling back to
"Claude" when none was recorded) and appends "· voice `<name>`" when the tutorial
recorded one. When the voice resolves to a spec, the name becomes a clickable
`.voice-reveal` (an inline `<details>` mirroring the Sources disclosure) that
expands the voice spec body — rendered from markdown into a quiet sunken
`.voice-reveal-body` panel with toned-down typography; otherwise the name renders
as plain `<code>`. Markdown content is never touched — this is the only
authorship-disclosure surface, and LLM authorship is always disclosed.

### Buttons

There is one button primitive, `.btn`, plus variants — never restyle a button
ad-hoc. Compose a class list in the markup:

- **Variants:** `.btn-primary` (accent fill — submits, Ask), `.btn-ghost`
  (outline — secondary), `.btn-danger` (failed-tint — Stop).
- **Modifiers:** `.btn-pill` (fully rounded — the floating/dock Ask buttons),
  `.btn-sm` (compact — the ask-drawer controls).

Accent buttons get their text color from `--btn-on-accent` (paper white in light,
near-black in dark) so the label stays legible on the accent fill in both themes.
Examples: `class="btn btn-primary"` (verify/extend submit),
`class="btn btn-primary btn-pill"` (Ask), `class="btn btn-danger btn-sm"` (Stop).
The prev/next **part-nav** links are intentionally *not* `.btn` — they're
two-line nav cards (label + title) sized to content and pinned to each edge, not
full-width bars.

### Syntax highlighting

Code-block syntax hues come from Chroma styles **tango** (light) and **gruvbox**
(dark), chosen to harmonize with the paper/ember palette (`lightStyle` /
`darkStyle` consts in `renderer.go`). The block *container* (background, border,
overflow fade) is owned by our `--code-bg` token: `HighlightCSS` in `renderer.go`
strips Chroma's own PreWrapper `background-color`, so only its token colors show
through.

**Both palettes are theme-scoped.** `HighlightCSS` prefixes the light rules with
`:root:not([data-theme="dark"])` and the dark rules with `[data-theme="dark"]`.
This matters: if the light rules stayed global, any token the (less exhaustive)
dark style leaves undefined would fall through to the light color — often
near-black — and be **unreadable** on the dark code background. Scoping makes an
undefined token fall back to its own style's readable default foreground instead.
`renderer_test.go` spot-checks `#8f5902` (tango) and `#fe8019` (gruvbox); swapping
either style means updating those assertions.

## Light / dark behavior

`[data-theme="dark"]` re-declares every token, so all components recolor from one
place. The pre-render script in the `head` partial reads `localStorage`
(`lathe-theme`) or the OS `prefers-color-scheme` and sets the attribute before
paint. The `themeToggle` JS flips it and persists the choice; mermaid diagrams
and the syntax CSS both react (the dark Chroma rules are scoped under
`[data-theme="dark"] .chroma`).

## How to add a new status or callout

These map 1:1 to the files CLAUDE.md already lists — add a token, then reference
it from a component class:

**New status badge:**
1. Add the `store.Status` value (`internal/store/metadata.go`).
2. Add `--badge-<name>-bg` / `--badge-<name>-text` to **both** `:root` and
   `[data-theme="dark"]` in `styles.css`, plus a `.badge.<name>` rule.
3. Add the case to the `{{define "badge"}}` partial in `components.html` and keep
   the emoji+word wording in sync with `cmd/list.go` `statusBadge`.

**New callout type:**
1. Add the marker to the `calloutBlock` regexp and `calloutLabel` in
   `renderer.go` (it emits `class="callout callout-<kind>"`).
2. Add `--callout-<name>-bg` / `-border` / `-label` to both token blocks in
   `styles.css`, plus `.callout-<name>` + `.callout-<name> .callout-label` rules.

## Fonts: re-fetching / updating

The bundled `woff2` are latin subsets pulled from the Google Fonts CSS2 API
(served pre-subset per `unicode-range`). To refresh, fetch the API CSS with a
modern-browser User-Agent, grab the `latin` `woff2` URL from each `@font-face`
block, and overwrite the files in `internal/serve/static/fonts/`. They're served
flat at `/_static/<name>.woff2` (single-segment route + explicit whitelist in
`server.go`), even though they live in the `fonts/` subdirectory on disk.
