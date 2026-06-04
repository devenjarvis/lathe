# CLAUDE.md

Orientation for Claude Code working in this repo.

## What this is

Lathe is a Go CLI plus a set of Claude Code skills that together generate, store, serve, verify, and extend hands-on technical tutorials. See `README.md` for user-facing docs and `docs/superpowers/specs/2026-05-03-lathe-design.md` for the design spec.

The boundary is strict: **skills generate content; the CLI owns durable state.** All model work — generating, verifying, extending, and answering reader questions — runs in the user's **interactive** Claude Code session via user-invoked skills (`/lathe`, `/lathe-verify`, `/lathe-extend`, `/lathe-ask`). The Go binary never spawns `claude` (headless `claude -p` is metered as of 2026-06-15; interactive sessions are not). Don't move generation logic into Go, and don't have skills write to `~/.lathe/tutorials/` directly — they call `lathe` commands (`lathe store`, `lathe verify-result`, `lathe extend-start`/`extend-commit`) instead.

## Layout

```
main.go                           cobra entrypoint
cmd/
  root.go                         rootCmd ("lathe")
  list.go, open.go, rm.go, serve.go, store.go    one subcommand per file
  verify.go, extend.go            print the /lathe-verify, /lathe-extend handoff command
  verify-result.go                lathe verify-result — skill records verify status/result
  extend-start.go, extend-commit.go    lathe extend-{start,commit} — skill reserves/records a part
  tag.go                          lathe tag — skill sets/adds/removes a tutorial's search tags
internal/
  config/                         TutorialsDir() → ~/.lathe/tutorials
  store/
    metadata.go                   Tutorial struct, Status enum, Read/WriteMetadata, VerifyResult
    store.go                      Store(), Delete(), copyDir/copyFile, detectParts, SlugToTitle, PromoteIndexToPart
  serve/
    server.go                     net/http handlers (list, tutorial, part, delete); list.html does client-side search/filter/sort
    ask.go, verify.go, extend.go  POST endpoints that return a paste-able skill command (handoff.go)
    renderer.go                   goldmark + chroma markdown rendering
    layout.html, list.html        embed.FS page templates
    components.html               shared {{define}} partials (head, badge, themeToggle)
    styles.css                    the design system (tokens + components), embedded & injected inline
    static/mermaid.min.js         embedded diagram renderer; static/fonts/*.woff2 latin-subset fonts
  extend/
    extend.go                     NextPartFilename helper (no model work — that's the skill)
.claude/skills/
  lathe/SKILL.md                  /lathe generation skill (user-invoked)
  lathe-verify/SKILL.md           /lathe-verify — runs verification interactively
  lathe-extend/SKILL.md           /lathe-extend — writes the next part interactively
  lathe-ask/SKILL.md              /lathe-ask — answers reader questions about a part
  lathe-tag/SKILL.md              /lathe-tag — picks/backfills search tags for stored tutorials
docs/design-system.md            design-system docs (tokens, type scale, components, how-to-add)
docs/superpowers/                 specs/ and plans/
```

## Build, test, run

```bash
go build -o lathe                 # build the binary
go test ./...                     # run all tests
go vet ./...                      # static checks
```

Tests are plain `go test` (no top-level runner script). The `/lathe` (`lathe`) binary built from this repo is gitignored at the repo root.

### CI gate — run before opening a PR

CI (`.github/workflows/ci.yml`) runs `mage check` on every PR and push to `main`: gofmt, `go vet`, `golangci-lint`, `go test -race ./...`, and `go build`. **Before opening or updating a PR, run `mage check` and make sure it's green** — don't push work that leaves CI red. `mage check` is the exact command CI runs, so local and CI cannot drift.

`magefile.go` defines the targets (`mage fmt|fmtCheck|vet|lint|test|build`, and `mage check` for all of them; `mage` alone runs `check`). It's stdlib-only and build-tagged (`//go:build mage`), so it adds nothing to `go.mod`. Lint config lives in `.golangci.yml`. One-time tool install:

```bash
go install github.com/magefile/mage@v1.15.0
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b "$(go env GOPATH)/bin" v2.12.2
mage fmt                          # auto-fix formatting; mage check is read-only on fmt
```

## Architecture notes

- **`cmd/serve.go`** registers `--port` on its command's flags but stores it in the package-level `servePort` variable, which `cmd/open.go` also reads. Keep them in sync if you add new commands that need the port.
- **`internal/serve/server.go`** uses Go 1.22+ method-and-pattern routing (`mux.HandleFunc("GET /{slug}/", …)`). `safeTutorialPath` defends against path traversal by checking the joined path stays under `tutorialsDir` — preserve that check on any new route.
- **Handoff model (verify/extend/ask).** The Go binary spawns no `claude` subprocess. The web POST endpoints (`internal/serve/{ask,verify,extend}.go`) validate + conflict-guard, then return `{"command": "/lathe-… <slug> …"}` via `writeHandoff` (`handoff.go`); the templates render a copy-to-clipboard panel. The CLI `lathe verify`/`lathe extend` likewise just print the command. The actual work runs in the user's interactive session via the matching skill, which calls back into the CLI to mutate state: `/lathe-verify` → `lathe verify-result <slug> --status verifying` (in-flight badge) then a terminal `--status verified|failed|skipped [...]`; `/lathe-extend` → `lathe extend-start` (reserves the part, prints its filename, sets `extending`) then `lathe extend-commit`. **Status is set by the skill, never by the web/CLI handoff** — that's deliberate, so an unclicked button can't leave a badge stuck at `verifying`/`extending`.
- **List-page search/filter/sort is client-side.** `handleList` renders every card with `data-*` attributes (title/slug/topic/tags/status/created/series) plus tag pills; the inline `<script>` in `list.html` does all search, tag/status/type filtering, and sorting in the browser — no new server round-trips, and it stays offline. The server is unchanged beyond the `Tags` field flowing through. Metadata-only by design: search never reads part bodies. Keep it progressive — with JS off, every card stays visible (`.hidden` is only ever set by that script). Tags are populated by `lathe store --tag` (from `/lathe`) and `lathe tag` (from `/lathe-tag`); `store.NormalizeTags` trims/lowercases/de-dupes and is the one place that vocabulary is canonicalized.
- **HTML templates** are `embed.FS`-bundled (`internal/serve/*.html`) so the binary is self-contained. They use a small `add` funcMap for 1-indexed part numbering. `components.html` is parsed into **both** the layout and list template sets (with `funcMap` attached to both) so its shared partials are available everywhere — see `NewServer`.
- **Design system**: `styles.css` is the single source of truth for all UI styling — light/dark color tokens, `@font-face`, base typography, and every component class. It's `go:embed`'d as `stylesCSS`, exposed to templates as `.CSS`, and injected inline via the `{{define "head"}}` partial (alongside `.HighlightCSS`) so there's no extra request and no FOUC. **Status and callout colors are CSS tokens in `styles.css`, not inline in the templates.** Full docs in `docs/design-system.md`.
- **Fonts** are latin-subset `woff2` (`internal/serve/static/fonts/`), `go:embed`'d and served at flat `/_static/<name>.woff2` (single-segment route + explicit whitelist preserved; `handleStatic` resolves `.woff2` names into the `fonts/` subdir). The UI stays 100% offline.
- **Markdown rendering** uses goldmark with the `tango` (light) / `gruvbox` (dark) Chroma styles, chosen to harmonize with the warm palette; the code-block container background is owned by our `--code-bg` token via `pre.chroma` in `styles.css`, so only syntax-token hues come from Chroma. Tests assert that `<pre>` and a highlight class appear in output (and spot-check `#8f5902`/`#fe8019`), so don't disable highlighting or swap styles without updating `renderer_test.go`.

## Conventions

- One cobra subcommand per file in `cmd/`, registered via `init()` calling `rootCmd.AddCommand(...)`.
- Errors flow up through `RunE`; the root `Execute()` exits non-zero on any error.
- Keep `internal/` packages free of cobra imports — they should be usable from tests directly.
- Skills are markdown files, all checked into `.claude/skills/<name>/SKILL.md` (`lathe`, `lathe-verify`, `lathe-extend`, `lathe-ask`, `lathe-tag`) and user-invoked in an interactive session. None are embedded in the binary — the binary spawns no `claude`.
- Status values are an enum (`store.Status`): `unverified` (default after store; renders no badge), `verifying`, `verified`, `failed`, `skipped` (required tool not installed — not a failure), `extending`. New states should be added there and reflected in `cmd/list.go` `statusBadge`, the `{{define "badge"}}` partial in `components.html`, and the `--badge-*` tokens + `.badge.<status>` rule in `styles.css` (see "how to add a new status" in `docs/design-system.md`).

## Things to avoid

- Verification is **opt-in / on-demand**: it runs only when the user asks (`/lathe-verify <slug>` in their session — surfaced by the `lathe verify` command, the `--verify` flag on `lathe store`, or the "Verify this tutorial" web button, all of which just hand off that command). Storing never auto-verifies; the default status is `unverified`. Don't re-introduce a Go-side verifier subprocess. Don't add a `lathe status` *read* command — status is surfaced via `lathe list` and the web UI (the `verify-result`/`extend-*`/`tag` commands are write-only state mutations for skills).
- Don't add tutorial editing or sharing commands without checking with the user — the v1 scope is deliberately narrow. (Deletion is supported via `lathe rm <slug>` and the `×` button on the web list page; both go through `store.Delete` / `safeTutorialPath`.)
- Don't have the verify/extend skills edit `metadata.json` or `verify-result.json` directly — they call `lathe verify-result` / `lathe extend-commit` so the binary stays the sole writer of durable state. The verify skill is read-only with respect to the tutorial markdown.
- Don't add OS-level sandboxing (sandbox-exec, Docker) for verification unless explicitly asked. With no subprocess, isolation is by instruction: the `/lathe-verify` skill builds in a fresh `mktemp -d` scratch dir under the user's normal interactive permission model.

## Commit style

Conventional commits (`feat:`, `fix:`, `chore:`, `refactor:`) — match the existing log. Keep subject lines short and imperative. Tests typically land in the same commit as the code they cover.
