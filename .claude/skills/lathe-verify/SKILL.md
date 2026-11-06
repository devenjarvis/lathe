---
name: lathe-verify
description: Verify that a stored Lathe tutorial actually works by following it end to end in a fresh scratch dir, in session. Use when the user invokes /lathe-verify with a slug like "/lathe-verify digital-synth-zig" (the "Verify this tutorial" button in `lathe serve` hands you that command).
---

# Lathe — Verify a Tutorial

Follow a stored tutorial exactly as a reader would, in a throwaway directory, and record whether it actually works. Triggered by `/lathe-verify <slug>`. Isolation is **by instruction** — a fresh `mktemp -d`, under the user's normal interactive permissions. No sandbox-exec, no Docker.

This skill is **read-only with respect to the tutorial**: never edit the parts or the metadata. The only writes are status updates through `lathe verify-result`, which also saves the reconstructed code as a per-part git history (the **code snapshot**) so the reader can check out or diff the code at any part afterwards.

**Opt-out:** if the user invoked `/lathe-verify <slug> --no-save`, skip every snapshot step below (no `git init`, no commits or tags, no `--code`). Everything else is unchanged.

## Protocol

1. **Mark it in-flight first:**
   ```bash
   lathe verify-result <slug> --status verifying
   ```
   This sets the spinner badge in the web UI. If it errors with "cannot verify while it is extending", stop — a part is mid-flight; don't verify on top of it.

2. **Make a fresh scratch dir and work there:**
   ```bash
   cd "$(mktemp -d)" && SCRATCH="$PWD"
   ```
   Everything the tutorial tells the reader to create happens here, not in the user's project. (Status is set by *this skill*, never by the web/CLI button — so an unclicked button can never strand a tutorial at `verifying`.)

   Then start the snapshot repo at the scratch root, ignoring dependency and build-output dirs so only source is kept:
   ```bash
   git init --quiet
   printf '%s\n' node_modules/ target/ .zig-cache/ zig-cache/ zig-out/ __pycache__/ .venv/ venv/ .gradle/ .DS_Store > .git/info/exclude
   ```
   Add any other dependency/build-output dir the tutorial's toolchain produces to `.git/info/exclude` as you meet it. (Use `.git/info/exclude`, not a `.gitignore`, so the snapshot never contains a file the tutorial didn't ask for.)

3. **Follow each part in order.** Read `~/.lathe/tutorials/<slug>/part-NN.md` from `part-01` up. Install the prerequisites, create the files and paste the code blocks exactly as written, in order, then run the `## Checkpoint` command and compare against the stated expected output.
   - **Skip the pedagogical and provenance callouts** — `> [!PREDICT]`, `> [!RECALL]`, and `> [!UNVERIFIED]` are not verifiable steps. They prompt the reader or flag uncertainty; there's nothing to execute.
   - Treat the Checkpoint commands and code blocks as the executable surface.
   - **Reader exercises** ("Now you write one" and similar) still have to be written for later checkpoints to pass. Write a minimal, correct solution so the snapshot is the complete, runnable code at that part.
   - **After a part's checkpoint passes, snapshot it** (from `$SCRATCH`, tag = the part's filename without `.md`):
     ```bash
     git -C "$SCRATCH" add -A
     git -C "$SCRATCH" -c user.name=lathe -c user.email=lathe@localhost commit --quiet --allow-empty -m "part-NN: <part title>"
     git -C "$SCRATCH" tag part-NN
     ```
     Do this only for parts that pass, so every tag marks code that worked. If the tutorial itself runs `git init` at the scratch root (it manages its own repo there), don't fight it: skip the snapshot for this run and tell the user why.

4. **Record the terminal result** with the matching command:
   Pass `--code "$SCRATCH"` with `verified` and `failed` (when at least one part was tagged) so the CLI saves the snapshot into the tutorial; never copy it into `~/.lathe/` yourself. Leave it off for `skipped`, for `--no-save`, and when no part was tagged.
   - **Everything works** → `lathe verify-result <slug> --status verified --code "$SCRATCH"`
   - **A required tool isn't installed** → `lathe verify-result <slug> --status skipped`
     This is **not a failure** — it's the ⚠️ Skipped badge, meaning "couldn't run it here," not "the tutorial is wrong." Use it whenever the toolchain the tutorial needs (compiler, runtime, SDK) is missing locally.
   - **Something genuinely breaks** (wrong output, code doesn't compile, a step contradicts itself) →
     ```bash
     lathe verify-result <slug> --status failed \
       --part <part-NN.md> \
       --failed-step <1-indexed step number within that part> \
       --error "<the error message or mismatched output>" \
       --code "$SCRATCH"
     ```
     The snapshot then holds the parts that passed, up to the one before the failure.
   - If `verify-result` reports that the status was recorded but the snapshot couldn't be saved, the result still stands; tell the user why the code wasn't kept.

5. **Report to the user** what happened — verified clean, skipped (and which tool was missing), or where exactly it failed — and, when a snapshot was saved, which parts it covers (e.g. `git -C ~/.lathe/tutorials/<slug>/code diff part-03 part-04`).

## Boundaries

- **Read-only on the tutorial.** Never edit a `part-NN.md`, `metadata.json`, `verify-result.json`, or the `code/` snapshot directly — the only state writes are via `lathe verify-result`.
- **No OS sandboxing.** Isolation is the `mktemp -d` scratch dir plus instruction, under the user's normal permission model. Don't reach for sandbox-exec or Docker.
- **Skipped ≠ failed.** A missing toolchain is `skipped`. Reserve `failed` for the tutorial being genuinely broken.
- Status is always set by this skill, never by the handoff button.
