---
name: lathe-verify
description: Verify that a stored Lathe tutorial actually works by following it end to end in a fresh scratch dir, in session. Use when the user invokes /lathe-verify with a slug like "/lathe-verify digital-synth-zig" (the "Verify this tutorial" button in `lathe serve` hands you that command).
---

# Lathe — Verify a Tutorial

Follow a stored tutorial exactly as a reader would, in a throwaway directory, and record whether it actually works. Triggered by `/lathe-verify <slug>`. Isolation is **by instruction** — a fresh `mktemp -d`, under the user's normal interactive permissions. No sandbox-exec, no Docker.

This skill is **read-only with respect to the tutorial**: never edit the parts or the metadata. The only writes are status updates through `lathe verify-result`.

## Protocol

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

## Boundaries

- **Read-only on the tutorial.** Never edit a `part-NN.md`, `metadata.json`, or `verify-result.json` directly — the only state writes are via `lathe verify-result`.
- **No OS sandboxing.** Isolation is the `mktemp -d` scratch dir plus instruction, under the user's normal permission model. Don't reach for sandbox-exec or Docker.
- **Skipped ≠ failed.** A missing toolchain is `skipped`. Reserve `failed` for the tutorial being genuinely broken.
- Status is always set by this skill, never by the handoff button.
