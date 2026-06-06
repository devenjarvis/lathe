---
name: lathe-extend
description: Write the next part of an existing Lathe tutorial, in session. Use when the user invokes /lathe-extend with a slug like "/lathe-extend digital-synth-zig" (the "Add a new part" button in `lathe serve` hands you that command), optionally followed by guidance for where the part should go.
---

# Lathe — Extend a Tutorial

Add the next part to a stored tutorial. Triggered by `/lathe-extend <slug> [guidance…]`. The new part must continue *that* tutorial — its example, its numbers, its voice — not a fresh generic one. Everything about shape, research discipline, and callouts comes from the **`lathe`** skill; read it and apply it. The **voice** is whatever the tutorial was generated in — fetch it from the CLI (see step 1), don't guess or re-pick. This skill only adds what's different about extending: continuity and the `extend-start → write → extend-commit` handshake.

## Protocol

1. **Absorb the existing tutorial.** Read every part in `~/.lathe/tutorials/<slug>/` (`part-NN.md`). You need the load-bearing context before you write a word:
   - The **controlling example** and the **fixed numbers** (sample rate, page size, buffer size) — reuse them exactly; don't invent new ones.
   - The **controlling metaphor**, if any, and whether a prior part already retired it (if retired, don't resurrect it).
   - The **voice**. Fetch the tutorial's recorded voice and apply it for all tonal choices:
     ```bash
     lathe voice show --tutorial <slug>
     ```
     This prints the wrapped voice spec (guardrail preamble + tone guidance). It resolves the voice recorded on the tutorial's metadata, falling back to the configured default for tutorials stored before voices existed. Don't re-pick or drift the voice; structure and substance still defer to the `lathe` skill, which wins on any conflict with the voice.
   - The **level of the reader**.
   - The **repo and pinned tool versions** in `metadata.json` (`repo`, `repo_branch`, `tools`) — the new part *inherits* them. Write against the same versions; don't silently bump to a newer toolchain. (If the reader explicitly wants to move the tutorial to a new version, that's a heads-up that it may be time for a fresh tutorial, not a quiet drift — flag it rather than re-pinning here.)
   - Where the previous part's **"What's next"** pointed — that's your mandate for this part unless the user's guidance redirects it.

2. **Research first.** Same discipline as the `lathe` skill's "Research first" step: actually open 3–8 authoritative sources for whatever this new part introduces, take notes with URLs beside the load-bearing facts, and ground or `[!UNVERIFIED]`-flag every load-bearing claim. New material gets the same scrutiny as Part 1 did. No web access? Say so in one line and write conservatively, flagging the load-bearing unknowns.

3. **Reserve the part.** Run:
   ```bash
   lathe extend-start <slug>
   ```
   It prints a single filename (e.g. `part-02.md`) and sets status `extending`. **Capture that exact filename.** If it errors with "already extending or verifying", stop and tell the user — something is mid-flight; don't force it.

4. **Write the part** to `~/.lathe/tutorials/<slug>/<printed-filename>`.
   - **This is the one allowed content write.** The skill writes the part markdown directly into the tutorial dir at the reserved path — that is required and deliberate, because `lathe extend-commit` `os.Stat`s the file there before registering it. (Future reader: don't "fix" this by routing it through a CLI command. There is no such command; the binary owns *metadata*, the skill writes the *part body*.)
   - Follow the full **Tutorial shape** from the `lathe` skill — hook, `## What you'll build`, prerequisites, specific section titles, `## Checkpoint`, `## What's next`, `## Exercises`, `## Sources`. Each part stands alone and ends with a Checkpoint.
   - Because this is **Part N ≥ 2**, open with a `> [!RECALL]` spaced-retrieval beat: one question on a load-bearing concept from a prior part, phrased so the reader must reconstruct the answer (not just recognize it). See the `lathe` skill's recall before/after.
   - **One file, this part only.** Don't write `index.md`, don't touch earlier parts, don't write more than one part.

5. **Commit the part.** Run:
   ```bash
   lathe extend-commit <slug> <printed-filename> [--source <url> …] --model "<model you are running as>"
   ```
   Pass `--source` once for each source you newly consulted for this part — it folds them into the tutorial's research trail (de-duped). Pass `--model` with the model *you* are running as (e.g. `--model "Claude Opus 4.8"`) so the reading-page byline reflects who wrote the latest part (last-writer-wins; omit it only to leave the existing label untouched). This registers the part, clears the pending marker, and resets status to `unverified`.

6. **Tell the user** it's added: how to view it (`lathe serve`), and that verification is opt-in (`/lathe-verify <slug>` — the "Verify this tutorial" button hands you that command). Then stay in session for follow-ups.

## Boundaries

- The **only durable-state writes** are `lathe extend-start` and `lathe extend-commit`. Never edit `metadata.json` directly.
- Writing the reserved part-content file into the tutorial dir is the sole content write — and it's required (step 4).
- Don't verify, don't write `index.md`, don't write multiple parts, don't edit existing parts.

## Stay in session

You're still their expert guide. Stay available for "make this part harder", "why did we structure it this way", "how'd I do on the checkpoint".
