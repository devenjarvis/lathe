---
name: lathe-onboard
description: Generate a hands-on onboarding guide for an existing git repository, anchored to a pinned commit so drift can be detected later. Use when the user invokes /lathe-onboard with a path or no argument, like "/lathe-onboard ~/Code/lathe" or just "/lathe-onboard" in the repo they want a guide for.
---

# Lathe — Onboard to a Codebase

Write the guide you wish someone had handed you on day one of this repo. Triggered by `/lathe-onboard [path]` — with no path, the current working directory.

Everything about **shape, openings, callouts, code rules, pedagogy, and voice** comes from the **`lathe`** skill. Read it and apply it. This skill adds only what is different about onboarding: you research a *repository* instead of the web, you follow a real execution path instead of building a toy, and every code block is anchored to a real file so `lathe drift` can tell the reader when the guide has stopped being true.

## Why this is a different kind of guide

A topic tutorial is verified by following it in a fresh `mktemp -d` and seeing if it runs. That is meaningless for a codebase you cannot recreate. An onboarding guide's analogue is **drift**: does the guide still describe the code?

That works only if the guide is pinned and anchored. You will pin a commit at reconnaissance time and quote code with `path=`/`lines=` anchors, and `lathe drift <slug>` will diff that pin against HEAD from then on. A guide with invented code or unanchored excerpts cannot be drift-checked and is worse than no guide, because it looks checkable and isn't.

## When invoked

1. Resolve the repo: the path the user gave, else `$PWD`. Confirm it is a git working tree.
2. Ask: **"What's your starting point — new to this codebase, new to the language/stack too, or already working in it and want the map?"**
3. Run **Reconnaissance** (below). This is the single most important step; it replaces the web-research step in the `lathe` skill.
4. Propose **the controlling trace** and let the user confirm or redirect it (this is the one thing worth asking about — everything else you settle silently).
5. Run the `lathe` skill's **Pre-flight** in your head, with the substitutions in "Pre-flight, adapted" below.
6. Write **Part 1 only**.
7. Clear the **pre-store gate** below, then `lathe store`.
8. Run `lathe drift <slug>` immediately and confirm every anchor comes back `ok`. If any anchor is `broken` or `changed` on a guide you just wrote against HEAD, an anchor is wrong — fix it before telling the user you're done.

## Reconnaissance (do this before writing a word)

Read the repo the way a careful new hire would, and *run things*. You are grounding every claim in this repository, not in your memory of how projects like this usually work.

**1. Pin the commit.** Do this first — everything you quote is relative to it.

```bash
git -C <repo> rev-parse HEAD              # the pin — this exact SHA goes to --repo-commit
git -C <repo> remote get-url origin       # the repo identity
git -C <repo> branch --show-current       # the branch
git -C <repo> status --porcelain          # a dirty tree means your excerpts may not match the pin
```

If the tree is dirty, say so and ask whether to commit or stash first. Anchoring to a commit while quoting uncommitted lines produces a guide that reports drift the moment it is stored.

**2. Read the orientation documents.** `README`, `AGENTS.md`/`CLAUDE.md`, `CONTRIBUTING`, `docs/`, any ADRs. These tell you what the maintainers *think* is important, which is not always what the code says — note both when they disagree; that gap is often the most useful thing in the guide.

**3. Find the build and test commands, then actually run them.** Look in the CI workflow, the Makefile/magefile/justfile/`package.json` scripts, and the contributing guide. Run them. A "Getting it running" section built from commands you never executed is exactly the section that wastes a new hire's first morning.

```bash
# whatever this repo actually uses — find it, don't assume it
cat .github/workflows/*.yml
```

**4. Locate the entry points.** `main`, the server bootstrap, the CLI root, the request router, the job loop. Note the file and line — you will anchor to them.

**5. Rank the hotspots by churn.** The files that change most are the files a new contributor will touch first:

```bash
git -C <repo> log --format= --name-only | sort | uniq -c | sort -rn | head -30
```

Use this to choose what Part 1 covers and to seed "What's next".

**6. Recover the design rationale from history.** The *why* behind a surprising decision is usually in a commit body or PR description, not in a comment:

```bash
git -C <repo> log --format='%h %s' -- <interesting-path> | head -20
git -C <repo> show --stat --format='%h%n%s%n%n%b' <sha>
```

When you use one, **cite it by SHA** in the prose (`` `1e7c9f6` `` — the commit that introduced it) and list it in `## Sources`.

**7. Note what you could not figure out.** An honest "no evidence for this in the repo" beats a confident guess. See "The hard invariant".

## The controlling trace (replaces the controlling example)

The `lathe` skill says: pick one concrete artifact and stay with it. Onboarding's version is **one real end-to-end path through this codebase**, named concretely in the repo's own terms:

- *"What happens when you click **Verify** in `lathe serve`"*
- *"What happens between `POST /checkout` and a row landing in `orders`"*
- *"What happens when the nightly reconciliation job finds a mismatch"*

Not *"the architecture"*. A named path the reader can follow with their finger, and then step through themselves.

Pick one that (a) crosses at least three subsystems, (b) touches the hotspots you ranked, and (c) the reader will plausibly have to change in their first month. Propose it to the user in one sentence before you write; they know which path matters.

Part 1 walks that trace **hop by hop**, each hop a named section with an anchored excerpt at the seam. The map diagram earlier in the part is the same trace, drawn.

## The hard invariant: every code block is real

**Every fenced block in an onboarding part is one of exactly two things:**

1. **A verbatim excerpt from this repo, anchored.** Copy the lines out of the file — do not retype them, do not tidy them, do not "simplify for clarity". Tag the fence with the repo-relative path and the line range:

   ````markdown
   ```go path=internal/serve/server.go lines=76-84
   mux.HandleFunc("POST /-/verify/{slug}", s.handleVerify)
   ```
   ````

   Format: `<lang> path=<repo-relative-path> [lines=<start>-<end>]`. The path is relative to the repo root, never absolute and never `./`-prefixed. Omit `lines=` only when you are pointing at a file as a whole rather than quoting it; a block with `lines=` is checked far more precisely, so prefer a range whenever you are quoting.

   Lathe renders these with a `path:line` header and, crucially, `lathe drift` classifies each one against the pin. Get the line numbers right — count them, don't estimate.

2. **A command the reader runs**, in a plain unanchored fence, exactly as it would be typed:

   ````markdown
   ```bash
   mage check
   ```
   ````

**There is no third category.** No illustrative pseudo-code, no "something like this", no reconstructed-from-memory function bodies. If you want to show a shape the repo doesn't contain, describe it in prose.

If you cannot ground a claim in a file you read or a command you ran, use `> [!UNVERIFIED]` and reframe it as what it is — *"I found no evidence for this in the repo; check with whoever owns `internal/queue`"* — not as a hedge on a claim you're actually asserting.

## Pre-flight, adapted

Run the `lathe` skill's Pre-flight, with these substitutions:

- **The controlling example** → the controlling trace, above.
- **Specific numbers** → real numbers from this repo: the port it serves on, the poll window, the buffer size, the number of skills, the actual test count. Read them out of the code, don't invent them.
- **Research and sources** → the reconnaissance notes: file paths with line numbers, commit SHAs, and the docs in `docs/`. Your `## Sources` cites *this repository* — files and commits — plus any genuinely external references (an RFC the code implements, a library's docs).
- **Exercises** → real tasks in *this* repo (see the shape below).
- **The closing send-off** → what the reader should be able to change on their own by the end.

Fetch the voice exactly as the `lathe` skill says (`lathe voice show`), and record it at store time.

## The onboarding part shape

Follow the `lathe` skill's Tutorial shape, with these onboarding-specific sections in this order. Section *titles* are still specific to this repo — never `## Step 1: Setup`.

```
# [Title — name the codebase and the trace]

[Hook — 2 to 4 paragraphs. See the lathe skill's "Openings". A concrete scene
from this repo works well: the confusing thing a new contributor hits first.]

## What you'll be able to do

One paragraph. The concrete capability — "trace a Verify click from the browser
to the badge on the list page, and know which file to open when it breaks."

## Getting it running

The prerequisites and the exact commands you actually ran during reconnaissance.
Ends with a `## Checkpoint` that is a **real command in this repo** with its real
expected output — the build, the test suite, the server starting.

## The map

One mermaid diagram of the trace. Cap at ~10 nodes. One sentence before it
telling the reader what to look at first. This is the one diagram in the part.

## [Hop 1 — named for what this hop does, in the repo's own vocabulary]
## [Hop 2 — ...]
## [Hop 3 — ...]

Each hop: why this piece exists, the anchored excerpt at the seam, and what it
hands to the next hop. Design notes at the end of a hop, never mid-step — this
is where a recovered commit rationale earns its keep.

## Checkpoint

A command that proves the reader followed the trace — run the test that covers
it, or start the server and hit the endpoint. Real command, real expected output,
real likely errors.

## What's next

The subsystem you'd cover in Part 2, taken from the hotspot ranking.

## Exercises

Real tasks in this repo, each with a runnable check.

## Sources

Files, commits (by SHA), and docs from this repo, plus any external references.
```

**Exercises must be real tasks in this repository**, each with a way to tell you did it right:

- ❌ *"Explore the queue package."*
- ✅ *"Add a `--json` flag to `lathe list` that prints the tutorial metadata as JSON. `go test ./cmd/...` should still pass, and `lathe list --json | jq .` should parse."*
- ✅ *"Find the one place `NormalizeRepo` is called with an ssh-style remote and add a test case for a remote with a port. `mage check` proves it."*

Aim for one that is a five-minute read-only scavenger hunt, two that are small real changes, and one that is genuinely open.

## Output files

Write to `/tmp/lathe-<slug>/`, exactly as the `lathe` skill says. Slug names the repo and the guide, e.g. `/tmp/lathe-lathe-onboarding/` → slug `lathe-onboarding`. Always `part-01.md`, one file, never `index.md`, never multiple parts.

**Never write into `~/.lathe/` directly** — `lathe store` is the only way content enters the library.

## After writing

> [!HEADS-UP]
> **STOP — pre-store gate. Fill this in before you run `lathe store`.** An
> onboarding guide stored without the repo triple **cannot be drift-checked at
> all**, and `lathe store` will reject it. Every store must state a concrete
> value for the first four, and a value or a justified opt-out for the rest:
>
> - **`--kind onboarding`** → always, for every guide from this skill. Without it
>   the guide is filed as a topic tutorial and `/lathe-verify` will try to build
>   it in a scratch dir.
> - **`--repo <origin-url>`** → the origin you read in reconnaissance.
> - **`--repo-commit <sha>`** → the **full SHA** you pinned with `git rev-parse
>   HEAD`, from the same reconnaissance pass your excerpts came from. Not a
>   branch name, not a short SHA, not "HEAD".
> - **`--repo-path <dir>`** → the absolute path to the checkout you read. This is
>   a hint for later drift runs, not the source of truth.
> - **`--repo-branch <branch>`** → the branch you read.
> - **Tags (`--tag`)** → 2–5 lowercase tags; include `onboarding` plus the
>   language and domain.
> - **Versions (`--tool name:version`)** → the toolchain versions the repo
>   actually builds with, read from `go.mod`/`package.json`/CI, not guessed.
> - **Sources (`--source`)** → external URLs only (a spec the code implements,
>   a library's docs). Repo files and commits belong in `## Sources`, not here.
>   Opt-out with a stated reason: *"grounded entirely in the repo"*.
> - **Voice (`--voice`)** → the voice you wrote in, from `lathe voice show`.
> - **Model (`--model`)** → the model *you* are running as, e.g.
>   `--model "Claude Opus 4.8"`.

Run:

```bash
lathe store /tmp/lathe-<slug> \
  --kind onboarding \
  --repo <origin-url> --repo-branch <branch> \
  --repo-commit <full-sha> --repo-path <absolute-path-to-checkout> \
  --tag onboarding --tag <lang> --tag <domain> \
  --tool <name>:<version> \
  --voice <name> \
  --model "<model you are running as>"
```

Then **prove the anchors resolve**:

```bash
lathe drift <slug>
```

Every anchor should be `ok` — you pinned HEAD and quoted from HEAD, so anything
else is a bug in *your* excerpt, not drift. A `broken` anchor means a wrong path;
a `changed` one means a wrong line range or a dirty tree. Fix the part, re-store,
and re-check before you report success.

Then tell the user:

- "**Guide saved and pinned at `<short-sha>`.** Run `lathe serve` to open it at http://localhost:4242."
- "Every code block is anchored to a real file. Run `lathe drift <slug>` (or click **Check for drift** on the page) whenever you want to know if the guide has fallen behind the code — it needs no agent running."
- "This is Part 1. `/lathe-extend <slug>` adds the next subsystem — it inherits the same pin."
- "`/lathe-verify <slug>` re-checks the guide against HEAD and re-pins it when the prose still holds."

## Boundaries

- **Write exactly one file**, `part-01.md`, into `/tmp/lathe-<slug>/`. Never touch `~/.lathe/`.
- **Never modify the repository you are documenting.** Reconnaissance is read-only plus running the project's own build and test commands. No commits, no branches, no edits, no `git stash` without asking.
- **Never invent code.** See "The hard invariant".
- **`--repo-commit` at store time is the initial pin.** Re-pinning afterwards belongs to `/lathe-verify` alone.

## Stay in session

You're their guide to this codebase now. Stay available for *"where would I add a new endpoint"*, *"what else should I read before touching the queue"*, *"walk me through the second hop again"*.
