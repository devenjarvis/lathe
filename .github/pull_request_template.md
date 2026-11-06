<!--
  This repo squash-merges, so your PR TITLE becomes the commit on `main`.
  Please title it in conventional-commit format, e.g.:
    feat: add windsurf to skills install targets
    fix:  prevent path traversal on the static route
    docs: clarify the verify handoff flow
-->

## What & why

<!-- What does this change do, and why? Link any related issue with "Closes #123". -->

## Visual proof

<!--
  Required for any change a user can see: the web UI (pages, styles, templates,
  browser scripts), rendered tutorial content, or CLI output. Delete this
  section only when nothing user-visible changed, and say so in one line.

  Show the change in the running app, not a mockup:
  - Before/after shots when you change something that already exists.
  - Every state you touched: empty, filled, error, loading, interactive steps.
  - Light AND dark theme, and a narrow (phone) viewport for layout changes.
  - Scripts off too, when the change adds browser behavior with a fallback.
  - For CLI changes, paste the real terminal output in a code block.

  Hosting: commit images to the `pr-media` branch under `pr-<number>/` and
  embed them by commit SHA, e.g.
    <img src="https://github.com/devenjarvis/lathe/blob/<sha>/pr-<number>/<name>.png?raw=true" alt="What the shot shows" width="800">
  Give every image alt text that says what it shows. Never capture real
  credentials or personal data.
-->

## How to test

<!-- Steps a reviewer can follow to see it working. Delete if not applicable. -->

## Checklist

- [ ] PR title is in conventional-commit format (`feat:`/`fix:`/`docs:`/`chore:`/`refactor:`)
- [ ] `mage check` passes locally
- [ ] Added visual proof (light + dark, every state touched) for any user-visible change, or stated that there is none
- [ ] Updated docs (`AGENTS.md` / `README.md` / `docs/`) if behavior changed
- [ ] Edited `.claude/skills/` (not `internal/skills/data/`) and ran `mage skills`, if skills changed
