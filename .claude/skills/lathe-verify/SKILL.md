---
name: lathe-verify
description: Verify that a stored Lathe tutorial actually works — by following a topic tutorial end to end in a fresh scratch dir, or by drift-checking an onboarding guide against its repository. Use when the user invokes /lathe-verify with a slug like "/lathe-verify digital-synth-zig" (the "Verify this tutorial" button in `lathe serve` hands you that command).
---

# Lathe — Verify a Tutorial

Check that a stored guide is still true, and record the outcome. Triggered by `/lathe-verify <slug>`. What "still true" means depends on the kind of guide:

- A **topic tutorial** is verified by following it exactly as a reader would, in a throwaway directory. Isolation is **by instruction** — a fresh `mktemp -d`, under the user's normal interactive permissions. No sandbox-exec, no Docker.
- An **onboarding guide** describes a codebase you cannot recreate, so it is verified by **drift**: does it still describe the code?

This skill is **read-only with respect to the tutorial**: never edit the parts or the metadata. The only writes are status updates through `lathe verify-result`.

## First: which kind of guide is this?

Read `~/.lathe/tutorials/<slug>/metadata.json` and check `kind` before anything else.

- **`"onboarding"`** → this is a guide to an existing codebase. There is nothing to build in a scratch dir; you cannot recreate someone's repository. Use the **Onboarding protocol** below.
- **Anything else** (including a missing `kind`, which every pre-feature tutorial has) → a topic tutorial. Use the **Tutorial protocol** below.

## Tutorial protocol

1. **Mark it in-flight first:**
   ```bash
   lathe verify-result <slug> --status verifying
   ```
   This sets the spinner badge in the web UI. If it errors with "cannot verify while it is extending", stop — a part is mid-flight; don't verify on top of it.

2. **Make a fresh scratch dir and work there:**
   ```bash
   cd "$(mktemp -d)"
   ```
   Everything the tutorial tells the reader to create happens here, not in the user's project. (Status is set by *this skill*, never by the web/CLI button — so an unclicked button can never strand a tutorial at `verifying`.)

3. **Follow each part in order.** Read `~/.lathe/tutorials/<slug>/part-NN.md` from `part-01` up. Install the prerequisites, create the files and paste the code blocks exactly as written, in order, then run the `## Checkpoint` command and compare against the stated expected output.
   - **Skip the pedagogical and provenance callouts** — `> [!PREDICT]`, `> [!RECALL]`, and `> [!UNVERIFIED]` are not verifiable steps. They prompt the reader or flag uncertainty; there's nothing to execute.
   - Treat the Checkpoint commands and code blocks as the executable surface.

4. **Record the terminal result** with the matching command:
   - **Everything works** → `lathe verify-result <slug> --status verified`
   - **A required tool isn't installed** → `lathe verify-result <slug> --status skipped`
     This is **not a failure** — it's the ⚠️ Skipped badge, meaning "couldn't run it here," not "the tutorial is wrong." Use it whenever the toolchain the tutorial needs (compiler, runtime, SDK) is missing locally.
   - **Something genuinely breaks** (wrong output, code doesn't compile, a step contradicts itself) →
     ```bash
     lathe verify-result <slug> --status failed \
       --part <part-NN.md> \
       --failed-step <1-indexed step number within that part> \
       --error "<the error message or mismatched output>"
     ```

5. **Report to the user** what happened — verified clean, skipped (and which tool was missing), or where exactly it failed.

## Onboarding protocol (`kind: onboarding`)

For an onboarding guide, "does it still work" means "does it still describe the code". **Do not `mktemp -d`, do not build anything, do not try to reconstruct the repo.** The mechanical half of this is a pure git computation the binary does for you; your job is the judgement half — whether the *prose* around a changed region is still true.

1. **Mark it in-flight:**
   ```bash
   lathe verify-result <slug> --status verifying
   ```
   Same conflict guard as above: if it errors because the guide is extending, stop.

2. **Run the drift check.** From inside the repository if you can (that is the resolution path that always works), else pass the path:
   ```bash
   lathe drift <slug>          # or: lathe drift <slug> --repo-path <dir>
   ```
   - It exits **non-zero with "unknown"** when the pinned commit isn't in the repo — a shallow clone, a rebased branch, a fresh clone that GC'd it. That is not a failure of the guide. Record `--status skipped` with an `--error` naming the reason, tell the user which checkout to run it from, and stop.
   - `moved` and `renamed` anchors are **not drift**. The guide still describes the code; only line numbers or paths shifted. Note them in your report and move on.
   - `changed` and `broken` anchors are what you actually judge.

3. **Judge the prose against the changed regions.** For each `changed` or `broken` anchor, open the file at HEAD (`lathe drift <slug> --json` gives you the current line numbers) and read what the code does *now*. Then read the paragraphs around that excerpt in the part. Ask one question: **does the surrounding prose still tell the truth?**
   - A renamed local variable, a reformatted signature, an added comment — the excerpt is stale but the explanation still holds. That is a **pass**.
   - A changed control flow, a removed branch the prose walks through, a deleted file the guide sends the reader to — the explanation is now wrong. That is a **failure**.

4. **Record the terminal result:**
   - **The guide still describes the code** →
     ```bash
     lathe verify-result <slug> --status verified --repo-commit "$(git -C <repo> rev-parse HEAD)"
     ```
     `--repo-commit` **re-pins the guide to HEAD**, so the next drift check measures from here. This command and `lathe store` are the only two places a guide is ever re-pinned. Pass it only when you actually confirmed the prose at HEAD — re-pinning a guide you didn't check erases real drift.
   - **The prose is now wrong somewhere** →
     ```bash
     lathe verify-result <slug> --status failed \
       --part <part-NN.md> \
       --error "<which anchor, and what the code does now that the prose says it doesn't>"
     ```
     **Do not re-pin a failed verify** — leave the pin where it is so the drift record stays meaningful.
   - **The pin is unreachable** → `--status skipped` with an `--error` explaining which repo/commit was missing.

5. **Report to the user**: the counts (`3 ok, 2 moved, 1 changed`), what you judged about the changed regions, and whether you re-pinned.

## Boundaries

- **Read-only on the tutorial.** Never edit a `part-NN.md`, `metadata.json`, `verify-result.json`, or `drift.json` directly — the only state writes are via `lathe verify-result` (and `lathe drift`, which owns `drift.json`).
- **Read-only on the repository too**, for onboarding guides. Never commit, branch, stash, or edit the codebase you are verifying against.
- **No OS sandboxing.** Isolation is the `mktemp -d` scratch dir plus instruction, under the user's normal permission model. Don't reach for sandbox-exec or Docker.
- **Skipped ≠ failed.** A missing toolchain — or an unreachable pinned commit — is `skipped`. Reserve `failed` for the guide being genuinely wrong.
- **`--repo-commit` only on a confirming verify.** Never re-pin alongside `failed` or `skipped`.
- Status is always set by this skill, never by the handoff button.
