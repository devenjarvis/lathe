# Contributing to Lathe

Thanks for your interest in contributing! Lathe is a Go CLI plus a set of
coding-agent skills that generate, store, serve, verify, and extend hands-on
technical tutorials.

This file covers the practical workflow. For a deeper map of the architecture
and conventions, read [AGENTS.md](AGENTS.md) — it's the orientation doc for both
humans and AI coding agents, and it's the source of truth for how the codebase
is organized.

## Before you start

The v1 scope is deliberately narrow. Before building a substantial feature
that significantly deviates from the current scope or technical architecture,
please [open an issue](https://github.com/devenjarvis/lathe/issues/new) to
discuss it first. This saves you from investing effort in something that may not
fit — and it's the fastest way to get pointed in the right direction. The
"Things to avoid" section of [AGENTS.md](AGENTS.md) explains the boundaries and
why they exist.

Smaller fixes (e.g. typos, docs, bugs, a failing edge case) don't need an issue
first. Just open a pull request. Same for issues that have already been
discussed and accepted as proposals.

## Using AI tools

Lathe is a project about learning *with* LLMs, so it'd be hypocritical to ban
LLM-assisted contributions — they're genuinely welcome. Use whatever coding
agent you prefer, code by hand, or even use lathe to walk you through an
enhancement!

What we ask is that you stand behind what you submit. When you open a PR, you're
vouching for it, so before you do:

- **Understand the change.** You should be able to explain what every line does
  and why. If you can't, you're not ready to submit it yet.
- **Run it.** Actually build it, run `mage check`, and exercise the behavior.
  Don't trust that it works because the model said so.
- **Keep it focused and honest.** No drive-by refactors the model threw in, no
  invented APIs, no plausible-looking code you haven't verified. Review the diff
  as if a stranger wrote it (because, in a sense, one did).

In short: let the LLM help you think, don't let it think for you. Reviewers will
engage with your PR the same way regardless of how it was written, and "the AI
wrote it" isn't an answer to review feedback.

## Prerequisites

- **Go 1.25+** (see `go.mod` for the exact version)
- **[mage](https://magefile.org/)** and **golangci-lint** for the CI gate
  (one-time install below)
- **Access to a supported LLM agent harness** for testing tutorial generation. If you use an agent harness not natively supported today, please consider that for a great first issue!

## Getting set up

**1. Fork the repo** and clone your fork. Replace `YOUR-USERNAME` with your
GitHub username:

```bash
git clone https://github.com/YOUR-USERNAME/lathe
cd lathe
```

**2. Build and test it.** Name the binary something like `lathe-local` so it
doesn't clash with any `lathe` you already have installed:

```bash
go build -o lathe-local  # build the binary (gitignored at the repo root)
go test ./...            # run the tests — make sure they pass before you change anything
```

**3. Install the CI tooling once.** `mage` is the task runner this project uses
(think `make`, but written in Go), and `golangci-lint` is the linter. CI checks
your PR with both, so install them now to catch problems locally:

```bash
go install github.com/magefile/mage@v1.15.0
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b "$(go env GOPATH)/bin" v2.12.2
```

If `mage` isn't found after install, make sure Go's bin directory is on your
`PATH`: add `export PATH="$PATH:$(go env GOPATH)/bin"` to your shell profile.

**4. Create a branch for your change.** Don't work on `main` — branch off it so
your fork's `main` stays clean:

```bash
git checkout -b fix-typo-in-readme
```

Now make your change. The next sections cover the specifics (editing skills, the
checks to run). When you're done, jump to
[Submitting your change](#submitting-your-change).

## Editing skills

Skills live in `.claude/skills/<name>/SKILL.md` and are the human-edited source
of truth. They're also embedded in the binary via a generated mirror under
`internal/skills/data/`.

- **Edit `.claude/skills/`**, never `internal/skills/data/` by hand.
- Run `mage skills` to regenerate the embedded mirror.
- `mage check` fails if the two drift, so don't skip the regen step.

## Before you open a PR

Run the exact command CI runs and make sure it's green:

```bash
mage check
```

This runs gofmt, `go vet`, `golangci-lint`, `go test -race ./...`, the skills
parity check, and `go build` — the same set as
[`.github/workflows/ci.yml`](.github/workflows/ci.yml), so local and CI can't
drift. Use `mage fmt` to auto-fix formatting (`mage check` is read-only on fmt).

If your change touches behavior documented in `AGENTS.md`, `README.md`, or
`docs/`, update those docs in the same PR.

## Submitting your change

Push your branch to your fork and open a pull request:

```bash
git push -u origin fix-typo-in-readme
```

GitHub will print a link to open the PR, or you can click "Compare & pull
request" on your fork's page. Fill in the template that appears. A clear
description of *what* changed and *why* makes review much faster.

Don't worry about how messy your individual commits are: **we squash-merge**, so
every PR becomes a single commit on `main`. That means your **PR title** is what
ends up in the history, not your commit messages.

Give the PR a title in **conventional-commit** format, since the squashed commit
inherits it:

```
feat: add windsurf to skills install targets
fix: prevent path traversal on the static route
docs: clarify the verify handoff flow
chore: bump golangci-lint to v2.12.2
```

- Use `feat:` / `fix:` / `docs:` / `chore:` / `refactor:` to match the existing
  [log](https://github.com/devenjarvis/lathe/commits/main).
- Keep it short and imperative ("add X", not "added X").
- Keep each PR focused on one thing — it's easier to review and to revert.

After you open the PR, CI runs `mage check` automatically. If it goes red, push
fixes to the same branch and the PR updates in place.

## License

By contributing, you agree that your contributions will be licensed under the
[MIT License](LICENSE) that covers this project.
