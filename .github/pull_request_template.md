<!--
  This repo squash-merges, so your PR TITLE becomes the commit on `main`.
  Please title it in conventional-commit format, e.g.:
    feat: add windsurf to skills install targets
    fix:  prevent path traversal on the static route
    docs: clarify the verify handoff flow
-->

## What & why

<!-- What does this change do, and why? Link any related issue with "Closes #123". -->

## How to test

<!-- Steps a reviewer can follow to see it working. Delete if not applicable. -->

## Checklist

- [ ] PR title is in conventional-commit format (`feat:`/`fix:`/`docs:`/`chore:`/`refactor:`)
- [ ] `mage check` passes locally
- [ ] Updated docs (`AGENTS.md` / `README.md` / `docs/`) if behavior changed
- [ ] Edited `.claude/skills/` (not `internal/skills/data/`) and ran `mage skills`, if skills changed
