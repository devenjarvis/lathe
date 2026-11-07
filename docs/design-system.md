# Lathe design system — "Type-in listing"

Lathe is for learning by building: the reader types the code in themselves. The
UI borrows from the 80s computer books and magazines where people learned
exactly that way, by typing a program listing in from the page. Warm paper, navy
ink, chunky rounded outlines, a yellow masthead, and three working colors with
fixed jobs:

- **Red means "do this"**: commands to run, step numbers, the main button.
- **Teal means "check this"**: expected output and labels.
- **Yellow means "you are here"**: the masthead, the current part, the current
  section, selected things.

Code you write sits on **green-bar printout paper** with tractor-feed holes.
Dark mode is the same book read off the screen at night: deep navy, cream text,
and the yellow kept as the bright accent.

Everything lives in **one source of truth**: `internal/serve/styles.css`
(tokens + components) and `internal/serve/components.html` (shared Go template
partials). Change a value once and every page picks it up.

## How it's wired

- `styles.css` is `go:embed`'d into `server.go` as `stylesCSS`, exposed to
  templates as `.CSS`, and injected inline by the `{{define "head"}}` partial as
  `<style>{{.CSS}}{{.HighlightCSS}}</style>`. Inline injection (like
  `HighlightCSS`) means **no extra request and no flash of unstyled content**.
- The theme (`data-theme="light|dark"`) is set by a pre-render `<script>` in the
  `head` partial *before* first paint, so there's no flash of the wrong theme.
- The one font is a latin-subset variable `woff2`, `go:embed`'d and served
  locally from `/_static/recursive.woff2` — the UI is **100% offline**, no
  external requests. It uses `font-display: swap` over system fallbacks.
- `HighlightCSS` (renderer.go) emits the light Chroma palette, the dark one, and
  the dark one again scoped to `.tutorial-block-command` in light mode, because
  command blocks are a navy panel in both themes.

## Tokens

All colors/spacing/type are CSS custom properties on `:root` (light "paper")
and overridden under `[data-theme="dark"]` (dark "night"). Components reference
the tokens only — never raw hex — with one deliberate exception: the masthead,
dock and Ask header are yellow with navy ink in both themes, so their ink is
the literal `#1C2140`. Every text token clears WCAG AA (4.5:1) on `--bg`,
`--surface` and `--accent-soft` in its theme; white on `--red` is 4.66:1.

### Color — surfaces & ink

| Token | Light (paper) | Dark (night) | Use |
|---|---|---|---|
| `--bg` | `#FFFBF0` | `#14182E` | page background |
| `--surface` | `#FFFFFF` | `#1D2240` | About box, cards, drawer bubbles |
| `--surface-sunken` | `#F6EEDA` | `#0F1226` | chips, label pills |
| `--border` | `#E9DFC6` | `#343A62` | hairlines, progress tracks |
| `--border-strong` | `#C9BC98` | `#565C8A` | tag outlines, dashed panels |
| `--rule` | `#1C2140` | `#DCD4BA` | the chunky 3px outline (`--line`) |
| `--text` | `#1C2140` | `#F2EAD3` | primary ink |
| `--text-muted` | `#33385A` | `#D9D2BC` | body prose |
| `--text-subtle` | `#4F4A3A` | `#C8C1A6` | secondary |
| `--text-faint` | `#6B6550` | `#A9A38B` | meta |
| `--link` | `#C0331C` | `#FF8C75` | links |
| `--red` | `#D63B22` | `#D63B22` | "do this": commands, step discs, primary buttons |
| `--teal` / `--teal-fill` | `#17706D` / `#1E7C79` | `#6FD0C9` / `#1E7C79` | "check this": output, labels |
| `--yellow` | `#F5C33B` | `#F5C33B` | "you are here": masthead, current, selected |
| `--accent-soft` | `#FFE7A3` | `#3B3317` | hovers, pale highlights |
| `--cmd-bg` / `--cmd-text` | `#1C2140` / `#FFF4D6` | `#0B0E21` / `#FFF4D6` | command panel |
| `--code-bg` / `--code-band` | `#FFFFFF` / `#E2F0DF` | `#1D2240` / `#17291F` | green-bar listing |
| `--cover-1..4` | red, teal, yellow, navy | same, navy darkened | bookshelf covers |

### Color — status badges and callouts

Badges are a colored dot plus a label (`--badge-<status>-text` colors the dot;
`-bg` tints the danger button and error toast). Six rendered states (`writing`,
a reservation the hosted agent is writing a new tutorial into, shares
`extending`'s teal);
`unverified` renders no badge — the About box says "Not verified" with a
**Verify it** link instead.

Callouts are rounded tinted panels with a Casual label and no left rule:
`note` (teal), `tip` (green), `warn`/`headsup` (yellow), `design` and `recall`
(periwinkle), `predict` (yellow with a yellow outline, the "guess first"
prompt), `aside` (thin rule, italic), `unverified` (red, dashed outline).

### Typography

One family, **Recursive** (SIL OFL), doing every job through its axes. The
bundled file keeps `wght` 300–1000, `CASL` 0–1 and `MONO` 0–1; slant and cursive
are pinned to keep it small, so italics are synthesized.

| Role | Token | Setting |
|---|---|---|
| Prose + UI | `--font-sans` / `--font-body` | Linear (`CASL 0`), weight ~420, body 1.125rem / 1.65 |
| Display | `--font-display` + `--casual` | Casual (`CASL 1`), weight 850–1000, tight tracking |
| Code | `--font-mono` + `--mono` | `MONO 1`, `CASL 0.5` |

The part's `h1` is the loudest type (≈3.75rem, weight 950). Each top-level `h2`
in the part body is a **step** and gets a red numbered disc (CSS counter);
the Exercises heading does not. Measure is `--measure: 68ch`.

### Spacing / radius / shadow / motion

`--space-1..8` (0.25rem → 4rem) · `--shadow-sm|md` · `--transition` (120ms
ease-out) · `--line` (3px, the chunky outline).

Radius: `--radius-sm` 6px (chips, inline code, checkboxes), `--radius-md` 10px
(code blocks, tables, menus), `--radius-lg` 16px (the About box, callouts,
covers, the exercise card). Every button is a pill.

## Component catalog

Each component is a class in `styles.css`. Markup it expects:

| Component | Class / selector | Markup source |
|---|---|---|
| Button | `.btn` + `.btn-primary`/`.btn-ghost`/`.btn-danger` (+ `.btn-pill`/`.btn-sm`) | layout.html action buttons |
| Status badge | `.badge.<status>` | `{{template "badge" <Status>}}` (components.html) |
| Theme toggle | `.theme-toggle[data-theme-toggle]` | `{{template "themeToggle" .}}` |
| Masthead | `#topBar`, `.back-link`, `.crumb`, `.part-picker`, `.part-menu` | layout.html |
| Sidebar nav | `#sidebar`, `.back-link`, `.toc-label`, `ul.toc` | layout.html |
| Series TOC | `.series-toc`, `.current-row`, `.part-num` | layout.html |
| Part nav | `.part-nav`, `.prev`/`.next`, `.label`, `.title` | layout.html |
| Verify / extend | `.verify-section`, `.extend-section`, `#verifyForm`, `#extendForm` | layout.html |
| About box | `.article-header`, `.article-title`, `dl.title-block` with one `div` per cell (`.tb-part`, `.tb-tools`, `.article-byline`, `.tb-voice`, `.tb-status`) and full-width `.tb-row`s; `.tb-big`, `.status-none`, `.status-jump`; `#jobLine.tb-job[data-state]` (`.tb-job-dot`, `.tb-job-text`, `.tb-job-link`) | layout.html + `statusHeader` (components.html) |
| Voice reveal | `.voice-reveal`, `.voice-reveal-body` | layout.html (in the Voice cell) |
| Section index | `#tocRail`, `.label`, `.tick`, `a.active` | layout.html |
| Update notice | `.update-notice[data-kind="download"\|"homebrew"\|"restart"]` (red dot, `.update-notice-text`, `.update-notice-sep`, `.update-notice-run` with the brew `code`, `.update-notice-act` — the red "do this" action — and `.update-notice-close` ×; navy outline on `#topBar`, where it is capped so the title keeps room and a Homebrew notice keeps only its Copy button, named by `.update-notice-narrow`; ≤760px drops `.update-notice-state` and the command, and in `#topBar` everything but the action, whose `aria-label` carries the version); desktop app only | components.html `updateNotice` |
| Agent panel | `#agentPanel.agent-panel` (docked ≥1100px via `html[data-agent-dock="open"\|"collapsed"]` and `--dock-w`; overlay dialog below, bottom sheet ≤700px; `html[data-agent-popout]` for the pop-out window), `.agent-panel-head` with one `.agent-chip` (+`.agent-dot`, `[data-state]`) and `.agent-state[data-state]` beside it, `.icon-btn`; `.agent-menu` (`role="menu"`; `.agent-chip-menu`, `.agent-action-menu`) of `.agent-menu-item`s (`.agent-menu-row`, `.agent-menu-label`, `.agent-menu-key`/`.agent-kbd` key hints, `.agent-menu-desc`, `.agent-menu-info` read-only rows, `[aria-disabled="true"]` with the reason as its description, `.agent-menu-sep`); `.agent-context` strip (`.agent-place`, `.agent-ws-btn`, `.agent-workspace-form`); `.agent-session`, `.agent-suggestion` (+`-text`/`-act`/`-close`), `.agent-composer` with `.agent-plus`; `.agent-rail` (collapsed), `.agent-toggle` (masthead, `.agent-toggle-key`), `.place-saved` (masthead, `[data-state="error"]`); desktop app only: a "Choose folder…" `btn btn-ghost btn-sm` in `.agent-workspace-actions` | layout.html |
| Agent panel setup | `.agent-setup`, `.agent-choices`, `.agent-choice` (+`-text`/`-detail`), `.agent-others`, `.agent-field` (+ `.agent-field-row`, desktop app only: the field plus a "Choose folder…" `btn btn-ghost btn-sm`), `.agent-hint`, `.agent-error`, `.agent-fallback` (+`-cmd`) | layout.html |
| Agent session log | `#askLog`, `.ask-bubble`, `.ask-question`, `.ask-answer`, `.ask-streaming` (caret), `.ask-note`/`.ask-note-error`, `.agent-isolation` | layout.html |
| Agent job cards | `.job-card[data-job][data-state="running"\|"done"\|"failed"\|"hints"\|"stopped"\|"error"\|"queued"\|"handoff"]` (`[data-waiting]` while a permission prompt is open) in `#askLog`: `.job-card-head` (`.job-card-top` with `.job-card-kicker`, `.job-card-time`, `.job-card-dismiss`; `.job-card-title`), `.job-segs`/`.job-seg[data-status]`, `.job-plan`/`.job-plan-entry[data-status]`, `.job-tail` (`.job-tail-line`, `-out`, `-blocked`), `.job-card-summary`, `.job-card-error`, `.job-card-actions` (`.job-card-link`, `.job-card-next`, Stop), `.job-card-cmd`, `.job-card-details` (`.job-report`, `.job-step-list`/`.job-step`); `.job-flash` on the article target of a card's next step | layout.html |
| Permission prompt | `.perm-card` (yellow and navy in both themes; `.resolved` dashed, `[data-choice]`): `.perm-kicker`, `.perm-ask`, `.perm-cmd`, `.perm-where`, `.perm-options` (Allow once `.perm-key` ↩, Allow for this job, Deny esc), `.perm-error` | layout.html |
| Split banner | `.split-banner` (yellow and navy in both themes, like `.perm-card`): `.split-banner-row` (`.split-banner-text`, `.split-banner-actions`: Preview split / Not now), `.split-preview` (a `:target` panel, `.is-open` with scripts) with `.split-cols` of `.split-col` (`.split-col-note`), `.split-preview-actions` | list.html |
| Shelf job | `.shelf-job` (`[data-state="running"]` pulses the `.agent-dot`): `.shelf-job-line` (`.shelf-job-text`, Stop), `.shelf-job-perms` (`.perm-card`s), `.shelf-job-cmd` (the command to paste), `.shelf-job-error`, `.shelf-job-note` | components.html (`shelfJobs`) |
| Sort review | `.sort-review`: `.sort-review-summary`, `.sort-table` (the global table card; a checkbox per move, an unticked row strikes `.sort-to`), `.sort-from`, `.sort-same` (folded unchanged rows), `.sort-done` | settings.html |
| Reading progress bar | `#progressBar` | layout.html |
| Progress toast | `.progress-status`, `#progressStatus` (`aria-hidden`) | layout.html |
| Announced status region | `#statusAnnouncer` (`role="status"`, shared by the progress and copy flows) | layout.html |
| Visually hidden utility | `.visually-hidden` | styles.css |
| Bookshelf covers | `body.list`, `.tutorial-list`, `.tutorial` (+ `::before` cover), `.tutorial-body` (`display: contents`), `.meta`, `.tutorial-link`, `.chips` (wraps `.versions` + `.tags`), `.tutorial-actions`, `.cover-menu` / `.cover-menu-btn` (the ⋯ menu; `.menu`, `.menu-item`, `.menu-sub`, `.menu-sep`, `.menu-danger`, the floating `.cover-popup`), `.empty` | list.html (`cover` partial) |
| Library | `.library` (sidebar + `.library-main`), `.library-nav` > `details.library-nav-panel` (`.library-nav-summary` narrow only), `.nav-list`, `.nav-link` (`[aria-current="page"]` = yellow), `.nav-count`, `.nav-heading`, `.nav-attention`, `.new-shelf` (+`-toggle`/`-form`/`-submit`/`-error`); Home: `.home-search`, `.shelf`, `.shelf-head`, `.shelf-count`, `.see-all`, `.shelf-row` (horizontal shelf), `.shelf-empty`, `.new-cover` (a link to the New tutorial sheet; +`-plus`/`-title`/`-hint`), `.more-tile` + `.more-count`; grid views: `.view-head` (`.view-title`, `.shelf-desc`), `.grid-options` > `.seg-links` (`.seg-label`, `.seg` links, `[aria-current="true"]` = selected), `.card-group` + `.card-group-head`; shelf page: `.shelf-menu` (+`-panel`/`-error`), `.shelf-edit`, `.shelf-delete`; drag: `.drop-target` (yellow), `.drag-ghost` (+`.over`), `.is-dragging`; Trash: `.trash-list`, `.trash-item` (+`-info`/`-title`/`-meta`/`-note`/`-actions`); `.toast` (+`-msg`/`-undo`/`-key`/`-close`) | list.html |
| New tutorial sheet | `.new-open` (+`-plus`/`-label`, the title-bar button), `dialog.new-sheet` (drops from the top edge; `.new-sheet-page` on `/-/new`), `.new-sheet-form`, `.new-sheet-head`/`-title`/`-esc`, `.new-sheet-body`, `.new-field`, `.new-label`/`.new-label-sm`, `.new-hint`, `.new-topic`, `.new-ideas` + `.new-idea` chips, `.new-select`/`.new-input`/`.new-mono`, `.new-seg` + `.new-seg-opt` (the level segmented control, yellow when picked), `.new-voice-list` + `.new-voice` (`-head`/`-name`/`-default`/`-sample`; red dot and yellow offset shadow when picked), `details.new-more` (`.new-more-sum`), `.new-result` (`-msg`, `-cmd`), `.new-sheet-foot` (`.new-agent`, `.new-actions`, `.new-key`) | newsheet.html |
| List card progress | `.tutorial-progress`, `.tutorial-progress-track`, `.tutorial-progress-segments`, `.tutorial-progress-label` | list.html |
| Callouts | `.callout.callout-<type>`, `.callout-label` | emitted by `renderer.go` |
| Tutorial blocks | `.tutorial-block`, `.tutorial-block-command`/`-output`/`-code`, `.tutorial-block-label`, `.copy-command` | emitted by `renderer.go` |
| Code blocks | `pre`, `pre.chroma`, `code`, `samp` | goldmark + chroma |
| Jigs | `.jig.jig-<kind>[data-jig]`, `.jig-head`, `.jig-kind`, `.jig-title`, `.jig-body`, `.jig-error`; per kind e.g. `.jig-guess-*`, enhanced state under `[data-jig-ready]` | emitted by `internal/jig` (see [jigs.md](jigs.md)) |

The `head`, `badge`, and `themeToggle` partials are parsed into **both** the
layout and list template sets (see `NewServer` in `server.go`).

### The About box (article header)

The article header is the "before you start" box from a type-in book: the
tutorial name in Casual on top, then a `<dl class="title-block">` of
`div`-wrapped `dt`/`dd` cells (teal labels), so a screen reader hears each label
with its value. Cells, in order:

- **Part** — "N of M", series only, number set large.
- **You'll need** (`.tb-tools`) — the tutorial's recorded tools and versions as
  mono chips; only when some were recorded.
- **Generated by** (`.article-byline`) — always present: the recorded model or
  "an LLM". This is the only LLM-authorship disclosure surface.
- **Voice** — only when one was recorded; a `.voice-reveal` disclosure whose
  spec opens as a panel under the whole box.
- **Verification** (`#badgeRegion`, re-rendered live by `/-/status`) — the
  `statusHeader` partial: "Verified <date>" in the badge, or "Not verified" with
  a **Verify it** link to `#verifyRegion`, or "Failed" with **See what failed**.
  Under it, `#jobLine` is the one-line status of the newest agent job ("Verifying
  the tutorial · step 2 of 5 See progress"); it opens the agent panel to that
  job's card, and the cell spans two columns while it shows.

Full-width `.tb-row`s (dotted rule above) follow for the `[!UNVERIFIED]` count
and the "Researched against N sources" disclosure.

### Tutorial blocks, series TOC, and the section index

Each block kind looks like what the reader does with it:

- **Command** (`.tutorial-block-command`) — "type this": red label pill, a navy
  panel in both themes (with the dark syntax palette), Copy on the label row.
- **Expected output** (`.tutorial-block-output`) — "you should see": teal label
  pill, dashed teal outline, no fill. A reference, not an instruction.
- **Code** (`.tutorial-block-code`) — "write this": the listing on green-bar
  paper, bands every three lines, tractor-feed holes down both edges (all CSS
  backgrounds on the `<pre>`, scrolling with the content).

Step discs, the series TOC and the exercises are real sequences, so they carry
numbers. The current part (series TOC, part menu), the current section (wide
index, drawer) and selected filters are yellow.

`#tocRail` takes three forms by width: at ≥1240px a permanent "On this page"
index in the right gutter; from 900–1239px the tick rail that expands on hover
or focus; below 900px hidden, with the hamburger drawer (`#sidebar`) instead.
The scroll-spy is position-based (last `h2[id]` above a line 30% down the
viewport, else Top) and marks both the rail and the drawer.

### Jigs

Interactive blocks (`internal/jig`, documented in [jigs.md](jigs.md)) share one
frame: a `--line` outline, the yellow masthead strip as the header, a navy kind
pill (`.jig-kind`, "Guess first") and a Casual title. Each kind styles only its
`.jig-body`. Guess first sits on `--accent-soft` paper; its verdict colors are
the `--jig-right-*` / `--jig-wrong-*` tokens. A jig whose source doesn't parse
renders as `.jig-error`, a dashed panel in the unverified colors with the
source underneath.

### List page — the bookshelf

Each tutorial is a **book cover**: a colored top (red, teal, yellow, navy,
fixed per tutorial from a hash of its slug, `data-cover` / `coverFor`, so a
tutorial keeps its color on Home, a shelf page, All tutorials and the ⌘K
palette) with its size and date and the title in big Casual type,
and a paper strip below with version + tag chips, saved progress, the status
badge and the delete button. It is built with CSS grid over the existing
markup: `.tutorial-body` is `display: contents`, so `.meta`, `.tutorial-link`,
`.chips` and `.tutorial-progress` are grid items of the card, and `.tutorial::before`
paints the cover behind the first two rows. With `subgrid`, covers in a row
share row tracks, so their colored tops line up. The whole cover is one
stretched link; the delete button sits above it and, on pointer devices, stays
hidden until the cover is hovered or holds focus.

The page is a **library**: a sidebar (`.library-nav`, a `<details>` open by
default that collapses to a one-line "Library · <view>" disclosure below
900px) beside the shelves. **Home** stacks shelves: each `.shelf-row` is a
horizontal row of fixed 12rem covers that scrolls (snapping) when it is longer
than the column, with the covers still on a shared subgrid. The first row opens
with `.new-cover`, a dashed blank cover; a shelf with more than four covers ends
in `.more-tile`, a striped stack of spines. Chips are hidden on Home rows (they
are there for the All grid's filters).

### Block labels and captions

A tutorial block's visible label comes from the fence's info string and nothing
else: `bash` reads `Command · bash`, `text` and `json` read `Expected output ·
<lang>`, any other language reads `Code · <lang>`, and a bare fence reads `Code`.
There is no author-supplied caption syntax, and that is the settled contract
rather than a gap (lathe-6m7). `AGENTS.md` already requires visible prose to
introduce every block, so a caption in the source would be a second place to say
what that prose says and the reader would hear both. The renderer names the
artifact's kind; the author's prose names the particular thing. Reopen it with a
tutorial that needs a caption the intro prose cannot carry.

## Light / dark behavior

`[data-theme="dark"]` re-declares every token, so all components recolor from one
place. The pre-render script in the `head` partial reads `localStorage`
(`lathe-theme`) or the OS `prefers-color-scheme` and sets the attribute before
paint. The `themeToggle` JS flips it and persists the choice; mermaid diagrams
and the syntax CSS both react (the dark Chroma rules are scoped under
`[data-theme="dark"] .chroma`, and again under
`:root:not([data-theme="dark"]) .tutorial-block-command` for the navy command
panel in light mode).

## Status messaging and announcements

The reading page is quiet by default: failures speak, actions the reader took
speak, and unsolicited successes stay silent. A background progress save happens
every few seconds while someone reads, so announcing each one turns the page into
a running commentary that interrupts the thing the reader came for. In WCAG
terms, a status that keeps updating on its own needs a way to be stopped or
hidden — [2.2.2 Pause, Stop, Hide](https://www.w3.org/WAI/WCAG22/Understanding/pause-stop-hide.html),
Level A — and the simplest mechanism is for it never to announce at all.

Two elements split the job, and the split is the point:

- `#progressStatus` is the visible toast. It keeps every message, timing, and
  error style it has always had, is `aria-hidden="true"`, and carries no live
  semantics: it is a visual cue, not an announcement.
- `#statusAnnouncer` is the page's one `role="status"` element with
  `aria-atomic="true"`, hidden with `.visually-hidden`. Only this element speaks,
  so the progress and copy flows report through one region instead of growing a
  live region each.

What it speaks:

| Event | Announced |
|---|---|
| Background save start, and background save success | nothing |
| Background save failure | `Progress not saved: <error>. Lathe tries again as you read.` — first failure only, then quiet until saving works again (the masthead note reads "Place not saved" and saves again when pressed) |
| A save that succeeds after a failure | `Progress saved` |
| Manual save success | `Progress saved` |
| Manual save failure | `Progress save failed: <error>` |
| Copying a command block | `Command copied to clipboard.`, or the manual-copy fallback |

Two carries are deliberate. A manual save marks both progress buttons
`aria-busy="true"` instead of disabling them, because a disabled control cannot
hold focus: the browser drops focus back to the document and announces
"Unavailable" for each copy of the button, which tells the reader nothing about
their save. The message also lands on the control the reader pressed, whose label
flashes "Saved" the way the copy buttons flash "Copied", since a name change on
the focused control is spoken even when the live region is missed.

Every announcement clears the region before writing its message, because setting
the same string a second time is not a change and would not be announced —
[4.1.3 Status Messages](https://www.w3.org/WAI/WCAG22/Understanding/status-messages.html),
Level AA.

A live region has to stay in the accessibility tree, so these surfaces use
`.visually-hidden`, which clips them, rather than `display: none`, which would
silence them completely.

The mirror image of that rule is `[hidden]{display:none !important}` in the base
layer: a component that sets its own `display` (the live nudge is `display:flex`)
otherwise beats the browser's `[hidden]` rule, so an element the page believes it
has dismissed stays visible and keeps announcing itself to a screen reader.
`#savedProgressMarker` had a local patch for this before the base rule existed.

When adding a status surface, decide which channel it belongs to first: visible
only, or announced as well. If it announces, add it to `#statusAnnouncer` (or a
region like it) instead of making another element live, and ask whether the event
is something the reader asked for. Unsolicited successes are what make a page
noisy.

## Heading ownership

A reading page has exactly one `h1`, and it is the part title authored in the
tutorial body's first `#` heading. The article header shows the tutorial name as
`.article-title`, styled like a title but carrying no heading level: the name
already appears in the top bar and the sidebar, and two `h1`s split the outline a
reader navigates by. The body heading is the source of truth, because demoting it
would either break the levels below it or make the renderer rewrite authored
markdown, and hoisting the part title into the chrome would put the page's only
`h1` outside the article it names.

## Naming and repetition

A screen reader speaks a string every time the page puts it in the reader's path,
and the accessibility tree multiplies it. A labelled container announces its
boundary, the label is read as its own node, and a control described by the same
text reads it a further time. Tutorial blocks were `<figure>`/`<figcaption>`,
which cost four readings of `Command · bash` before a reader reached one command:
the figure boundary, the caption text, the copy button's description of that same
caption, and the boundary again on the way out. Note the caption did not *name*
the figure — `<figure>` takes no name from its content, and no screen reader
treats a caption as the figure's accessible name (Roselli, "Brief Note on Figure
and Figcaption Support"; MDN interactive-examples #2563 reports the same
duplicate read). `renderer.go` carries the note.

In browse mode no element gives a static block a spoken *end*. Block boundaries
come only from roles that announce themselves — quote, figure, list, table,
heading — and every one of them costs the reader a reading of the text it wraps.
A long block needs that boundary anyway: a reader who wants out of a hundred lines
should not have to arrow through them. So a non-command block longer than
`longBlockLines` lines gets a one-item list wrapper. A list is the container every
screen reader lets the reader leave with one keystroke — the reader who tested
this could jump past a list and a blockquote with JAWS `>` and could not jump past
a bare `<pre>` or a `<figure>`. Command blocks are excluded: the copy button at
their end already answers the same question.

The wrapper is a list rather than a blockquote because a blockquote would tell the
reader the output is someone else's words, and would put machine output into quote
navigation, where quotations from cited sources belong. It shows no visible
marker, so the markup carries `role="list"` to keep the list in the accessibility
tree.

A figure was tried and dropped. A `<figure aria-label>` announces its name on
entry and exit, but JAWS repeats the name on the way out and still refuses the
skip, so it cost a second reading per block and bought nothing. The label stays a
plain paragraph, read once, in the reader's path.

Reach for a boundary cue only when the block is a stop a reader would jump to;
otherwise let structure the reader already navigates (headings, list items) and
the author's prose carry the end.

Give a thing one name in one place and let adjacency do the associating. Reach for
a container role only when the thing is a destination a reader would navigate to —
code blocks, install snippets, and demo panels are not. Do not point
`aria-describedby` at the text that already names the thing.

## How to add a new status or callout

These map 1:1 to the files AGENTS.md already lists — add a token, then reference
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

The one bundled font, `recursive.woff2`, comes from the
`@fontsource-variable/recursive` npm package's latin "full" file (all five
axes), cut down with fontTools so only the axes the UI uses stay variable:

```bash
npm pack @fontsource-variable/recursive && tar xzf fontsource-variable-recursive-*.tgz
python3 -c "from fontTools.ttLib import TTFont; from fontTools.varLib import instancer; \
t=instancer.instantiateVariableFont(TTFont('package/files/recursive-latin-full-normal.woff2'), {'CRSV':0,'slnt':0}); \
t.flavor='woff2'; t.save('recursive.woff2')"   # needs: pip install fonttools brotli
```

Copy the result to `internal/serve/static/fonts/`. It's served flat at
`/_static/recursive.woff2` (single-segment route + explicit whitelist in
`server.go`), even though it lives in the `fonts/` subdirectory on disk.
