# CLAUDE.md

Orientation for Claude Code working in this repo.

## What this is

Lathe is a Go CLI plus a set of Claude Code skills that together generate, store, serve, verify, and extend hands-on technical tutorials. See `README.md` for user-facing docs.

The boundary is strict: **skills generate content; the CLI owns durable state.** All model work — generating, verifying, extending, answering reader questions, and authoring voices — runs in the user's **interactive** Claude Code session via user-invoked skills (`/lathe`, `/lathe-verify`, `/lathe-extend`, `/lathe-ask`, `/lathe-tag`, `/lathe-voice`). The Go binary never spawns `claude` (headless `claude -p` is metered as of 2026-06-15; interactive sessions are not). Don't move generation logic into Go, and don't have skills write to `~/.lathe/` directly — they call `lathe` commands (`lathe store`, `lathe verify-result`, `lathe extend-start`/`extend-commit`, `lathe voice add`) instead. The one skill→CLI **read** path is `lathe voice show` (the active voice spec) — still consistent with the boundary: the CLI stays the sole owner of the voice files and config; the skill only asks for text.

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
  version.go                      lathe version — prints buildinfo.String() (alias for --version)
  skills.go                       lathe skills install/list — write embedded skills to Claude Code / Cursor / Codex
  voice.go                        lathe voice list/show/set-default/add/rm — manage writing voices (parent + subcommands, one file)
internal/
  buildinfo/                      Version/Commit/Date vars (ldflags-injected) + Resolve()/String()
  frontmatter/                    Parse()/Strip() — shared name:/description: frontmatter scanner (used by skills + voice)
  skills/                         embedded skills (//go:embed data) + catalog (skills.go), Cursor translation (cursor.go)
  voice/                          embedded voice presets (//go:embed data) + List/Resolve/Add/Remove + fixed guardrail Preamble (voice.go)
  config/                         TutorialsDir(), VoicesDir(), ConfigDir() → ~/.lathe; config.json (ReadConfig/WriteConfig, DefaultVoice/SetDefaultVoice)
  store/
    metadata.go                   Tutorial struct (incl. Repo/RepoBranch + Tool/Tools + Sources + Voice + Model), Status enum, Read/WriteMetadata, RepoDisplay, VerifyResult
    store.go                      Store()/StoreOptions, Delete(), Normalize{Tags,Sources,Repo,Tools,Voice}, copyDir/copyFile, detectParts, SlugToTitle, PromoteIndexToPart
  serve/
    server.go                     net/http handlers (list, tutorial, part, delete); handleList renders a flat newest-first list; list.html does client-side search/filter/sort
    ask.go, verify.go, extend.go  POST endpoints that return a paste-able skill command (handoff.go)
    renderer.go                   goldmark + chroma markdown rendering
    layout.html, list.html        embed.FS page templates
    components.html               shared {{define}} partials (head, badge, themeToggle)
    styles.css                    the design system (tokens + components), embedded & injected inline
    static/mermaid.min.js         embedded diagram renderer; static/fonts/*.woff2 latin-subset fonts
  extend/
    extend.go                     NextPartFilename helper (no model work — that's the skill)
  skills/data/                    generated, tracked, embeddable copy of .claude/skills (parity-gated by `mage skills`)
.claude/skills/
  lathe/SKILL.md                  /lathe generation skill (user-invoked)
  lathe-verify/SKILL.md           /lathe-verify — runs verification interactively
  lathe-extend/SKILL.md           /lathe-extend — writes the next part interactively
  lathe-ask/SKILL.md              /lathe-ask — answers reader questions about a part
  lathe-tag/SKILL.md              /lathe-tag — picks/backfills search tags for stored tutorials
  lathe-voice/SKILL.md            /lathe-voice — authors a custom writing voice, then persists it via lathe voice add
docs/design-system.md            design-system docs (tokens, type scale, components, how-to-add)
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

`magefile.go` defines the targets (`mage fmt|fmtCheck|skills|skillsCheck|vet|lint|test|build`, and `mage check` for all of them; `mage` alone runs `check`). It's stdlib-only and build-tagged (`//go:build mage`), so it adds nothing to `go.mod`. Lint config lives in `.golangci.yml`. One-time tool install:

```bash
go install github.com/magefile/mage@v1.15.0
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b "$(go env GOPATH)/bin" v2.12.2
mage fmt                          # auto-fix formatting; mage check is read-only on fmt
```

## Architecture notes

- **`cmd/serve.go`** registers `--port` on its command's flags but stores it in the package-level `servePort` variable, which `cmd/open.go` also reads. Keep them in sync if you add new commands that need the port.
- **`internal/serve/server.go`** uses Go 1.22+ method-and-pattern routing (`mux.HandleFunc("GET /{slug}/", …)`). `safeTutorialPath` defends against path traversal by checking the joined path stays under `tutorialsDir` — preserve that check on any new route.
- **Handoff model (verify/extend/ask).** The Go binary spawns no `claude` subprocess. The web POST endpoints (`internal/serve/{ask,verify,extend}.go`) validate + conflict-guard, then return `{"command": "/lathe-… <slug> …"}` via `writeHandoff` (`handoff.go`); the templates render a copy-to-clipboard panel. The CLI `lathe verify`/`lathe extend` likewise just print the command. The actual work runs in the user's interactive session via the matching skill, which calls back into the CLI to mutate state: `/lathe-verify` → `lathe verify-result <slug> --status verifying` (in-flight badge) then a terminal `--status verified|failed|skipped [...]`; `/lathe-extend` → `lathe extend-start` (reserves the part, prints its filename, sets `extending`) then `lathe extend-commit`. **Status is set by the skill, never by the web/CLI handoff** — that's deliberate, so an unclicked button can't leave a badge stuck at `verifying`/`extending`.
- **List page is a flat newest-first list (server-side) + searched/filtered/sorted client-side.** `handleList` sorts tutorials newest-first and renders them into one flat `#tutorialList` container (`list.html`). Each card carries `data-*` attributes (title/slug/topic/tags/**repo**/**tools**/status/created/series) plus tag pills and **version chips**; the inline `<script>` in `list.html` does all search, status/type/tag/**version** filtering, and sorting in the browser — sorting reorders the flat container in place. No new server round-trips; stays offline. Metadata-only by design: search never reads part bodies. Keep it progressive — with JS off, every card stays visible (`.hidden` is only ever set by that script). **Repo is a searchable field (`data-repo` + the search box), not a grouping key.** Tags come from `lathe store --tag` / `lathe tag`; repo + versions come from `lathe store --repo`/`--repo-branch`/`--tool` (from `/lathe`). `store.Normalize{Tags,Repo,Tools}` are the one place each vocabulary is canonicalized — **versions are structured (`Tools`), deliberately *not* folded into tags.**
- **HTML templates** are `embed.FS`-bundled (`internal/serve/*.html`) so the binary is self-contained. They use a small `add` funcMap for 1-indexed part numbering. `components.html` is parsed into **both** the layout and list template sets (with `funcMap` attached to both) so its shared partials are available everywhere — see `NewServer`.
- **Design system**: `styles.css` is the single source of truth for all UI styling — light/dark color tokens, `@font-face`, base typography, and every component class. It's `go:embed`'d as `stylesCSS`, exposed to templates as `.CSS`, and injected inline via the `{{define "head"}}` partial (alongside `.HighlightCSS`) so there's no extra request and no FOUC. **Status and callout colors are CSS tokens in `styles.css`, not inline in the templates.** Full docs in `docs/design-system.md`.
- **Fonts** are latin-subset `woff2` (`internal/serve/static/fonts/`), `go:embed`'d and served at flat `/_static/<name>.woff2` (single-segment route + explicit whitelist preserved; `handleStatic` resolves `.woff2` names into the `fonts/` subdir). The UI stays 100% offline.
- **Markdown rendering** uses goldmark with the `tango` (light) / `gruvbox` (dark) Chroma styles, chosen to harmonize with the warm palette; the code-block container background is owned by our `--code-bg` token via `pre.chroma` in `styles.css`, so only syntax-token hues come from Chroma. Tests assert that `<pre>` and a highlight class appear in output (and spot-check `#8f5902`/`#fe8019`), so don't disable highlighting or swap styles without updating `renderer_test.go`.
- **LaTeX math** is passed through goldmark verbatim via the hugo `passthrough` extension (`$…$`, `$$…$$`, `\(…\)`, `\[…\]`) — without it, CommonMark backslash-escapes corrupt TeX before the browser sees it (`\|` → `|`). Typesetting is client-side: KaTeX (`static/katex.min.js` + `katex-auto-render.min.js` + `katex.min.css` + `KaTeX_*.woff2` fonts) is embedded and lazy-loaded by `layout.html` only when the article contains a math delimiter, mirroring the mermaid pattern — fully offline. The vendored `katex.min.css` has its `url(fonts/…)` references **flattened to `url(…)`** so fonts resolve under the flat `/_static/<name>.woff2` route (re-apply `sed 's|url(fonts/|url(|g'` when upgrading KaTeX); the KaTeX font whitelist entries are derived from the embed FS in an `init()` in `server.go`. Parsing is AST-level, so `$` inside code spans/fenced blocks is never treated as math — `renderer_test.go` pins all of this.
- **Versioning (`internal/buildinfo`).** Exported `Version`/`Commit`/`Date` vars are overridden at link time via `-ldflags "-X github.com/devenjarvis/lathe/internal/buildinfo.Version=…"`. `Resolve()` prefers an injected `Version`, else falls back to `runtime/debug.ReadBuildInfo().Main.Version` (so `go install`ed binaries show their module version, not `dev`). `cmd/root.go` sets `rootCmd.Version = Resolve()` and a version template printing `String()` (version + commit/date); `cmd/version.go` is a friendly alias. The ldflags **package path** (`github.com/devenjarvis/lathe/internal/buildinfo`) must match in the two places that inject it — `magefile.go` `Build()` and `.goreleaser.yaml` — or the `-X` is silently ignored. They stamp different var sets on purpose: `Build()` injects `Version`+`Commit` (git-derived, for local builds); `.goreleaser.yaml` also injects `Date` (release builds only — a local build date would just be churn).
- **Embedded skills (`internal/skills`).** `.claude/skills/` stays the human-edited source of truth, but `go:embed` ignores paths starting with `.`, so `mage skills` generates a tracked, embeddable mirror under `internal/skills/data/` and `mage skillsCheck` (in `mage check`) fails if they drift. **Never hand-edit `internal/skills/data/`** — edit `.claude/skills/` and run `mage skills`. `skills.go` embeds `data` and exposes a typed catalog (`All()` parses the `name:`/`description:` frontmatter with no YAML dep); `cursor.go` translates a SKILL.md into a Cursor command (strip frontmatter, prepend a `/<slug>` header). `cmd/skills.go` (`lathe skills install`/`list`) writes them to Claude Code (`./.claude/skills/<name>/SKILL.md`, or `~/…` with `--user`), Cursor (`./.cursor/commands/<slug>.md`), and/or Codex (`./.agents/skills/<name>/SKILL.md`, or `~/.agents/skills/…` with `--user`); it writes only into the chosen agent dirs, never `~/.lathe/`. **Codex uses the same SKILL.md format as Claude Code**, so its skills ship the *raw* bytes verbatim — there's no `codex.go` translation (unlike Cursor), and `--user` is fully supported (no fallback warning).
- **Voices (`internal/voice`).** Writing voice is **selectable** (built-in presets) and **extensible** (user-authored), with a global default + per-`/lathe`-run override recorded on the tutorial (`Tutorial.Voice`). A voice controls **tone and register only** — never accuracy, research, citation, verification, substance, pedagogy, or structure; those are always-on invariants in `lathe/SKILL.md` and win on any conflict. The crux of the design is *partitioning* the old hardcoded `## Voice` block: the tonal half (persona, first-person policy, em-dash cadence, tonal "avoid", tone before/afters) moved into preset spec files; the trust/pedagogy half stayed in the skill. Built-ins are embedded from `internal/voice/data/<name>.md` (`plainspoken.md` = the non-anthropomorphic default; `companion.md` = the original warm voice, verbatim); custom voices live in `~/.lathe/voices/<name>.md`. `voice.go` exposes `List()` (built-ins + custom, built-ins win, no silent shadowing — `Add` rejects built-in name collisions), `Resolve(name)`, `Add`/`Remove`, and a **fixed, non-overridable `Preamble`** that `Wrapped()` prepends to every spec (built-in *and* custom) so a hostile file can't escape the framing: it states the voice is tone-only and that the accuracy/no-fabrication/no-impersonation/LLM-authorship guardrails win. `cmd/voice.go` (`lathe voice list/show/set-default/add/rm`) is the CLI surface; **`lathe voice show` is the skill read path** (default, `<name>`, or `--tutorial <slug>` → the recorded voice, falling back to the default for pre-feature tutorials). The default lives in `~/.lathe/config.json` (`internal/config`), defaulting to `plainspoken`. Guardrail enforcement at *generation* time is by the model reading the wrapped voice + the SKILL.md invariants — **no Go-side content scanning** (that would violate the no-model-in-Go principle). The served reading page discloses authorship in a **byline at the top of the article header** (`.article-byline` in `layout.html` — *not* a `components.html` partial; the old `articleFooter` is gone): "Generated by `<Model>`", where `Tutorial.Model` is the LLM display label (e.g. "Claude Opus 4.8") populated via `lathe store --model` and falling back to a generic "Claude" for pre-feature tutorials, plus "· voice `<name>`" when one is recorded. When the wrapped voice spec is available (`.VoiceSpec`), the voice name expands inline — a `.voice-reveal` `<details>` — to reveal the full spec. The research trail is disclosed separately in the same header via the `.provenance sources` panel ("Researched against N sources" → `Tutorial.Sources` links). Markdown content stays clean.
- **Release pipeline.** Tagging `vX.Y.Z` and pushing triggers `.github/workflows/release.yml`, which runs GoReleaser (`.goreleaser.yaml`): darwin/linux × amd64/arm64 binaries, GitHub Release with checksums + conventional-commit changelog, and a Homebrew **cask** committed to `devenjarvis/homebrew-tap` (needs the `HOMEBREW_TAP_GITHUB_TOKEN` repo secret; a cask ships the pre-built binary, so `brew install` is macOS-only — Linux uses `install.sh`/`go install`). `release.yml` is separate from `ci.yml` — don't merge them. Dry-run with `goreleaser check` / `goreleaser release --snapshot --clean`. `install.sh` (repo root) is the `curl | sh` path: it resolves the latest (or `$LATHE_VERSION`) release, verifies the checksum, and installs `lathe` to `~/.local/bin` (or `/usr/local/bin`).

## Conventions

- One cobra subcommand per file in `cmd/`, registered via `init()` calling `rootCmd.AddCommand(...)`.
- Errors flow up through `RunE`; the root `Execute()` exits non-zero on any error.
- Keep `internal/` packages free of cobra imports — they should be usable from tests directly.
- Skills are markdown files, all checked into `.claude/skills/<name>/SKILL.md` (`lathe`, `lathe-verify`, `lathe-extend`, `lathe-ask`, `lathe-tag`, `lathe-voice`) and user-invoked in an interactive session. They **are** embedded in the binary (via the generated `internal/skills/data/` mirror, so `lathe skills install` works post-`brew`/`go install`) — but the binary still spawns no `claude`: embedding ships the skill *text*, it doesn't run a model. Keep that distinction.
- Status values are an enum (`store.Status`): `unverified` (default after store; renders no badge), `verifying`, `verified`, `failed`, `skipped` (required tool not installed — not a failure), `extending`. New states should be added there and reflected in `cmd/list.go` `statusBadge`, the `{{define "badge"}}` partial in `components.html`, and the `--badge-*` tokens + `.badge.<status>` rule in `styles.css` (see "how to add a new status" in `docs/design-system.md`).

## Things to avoid

- Verification is **opt-in / on-demand**: it runs only when the user asks (`/lathe-verify <slug>` in their session — surfaced by the `lathe verify` command, the `--verify` flag on `lathe store`, or the "Verify this tutorial" web button, all of which just hand off that command). Storing never auto-verifies; the default status is `unverified`. Don't re-introduce a Go-side verifier subprocess. Don't add a `lathe status` *read* command — status is surfaced via `lathe list` and the web UI (the `verify-result`/`extend-*`/`tag` commands are write-only state mutations for skills).
- Don't add tutorial editing or sharing commands without checking with the user — the v1 scope is deliberately narrow. (Deletion is supported via `lathe rm <slug>` and the `×` button on the web list page; both go through `store.Delete` / `safeTutorialPath`.)
- Don't have the verify/extend skills edit `metadata.json` or `verify-result.json` directly — they call `lathe verify-result` / `lathe extend-commit` so the binary stays the sole writer of durable state. The verify skill is read-only with respect to the tutorial markdown.
- Don't add OS-level sandboxing (sandbox-exec, Docker) for verification unless explicitly asked. With no subprocess, isolation is by instruction: the `/lathe-verify` skill builds in a fresh `mktemp -d` scratch dir under the user's normal interactive permission model.

## Commit style

Conventional commits (`feat:`, `fix:`, `chore:`, `refactor:`) — match the existing log. Keep subject lines short and imperative. Tests typically land in the same commit as the code they cover.
