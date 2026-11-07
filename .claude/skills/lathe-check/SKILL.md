---
name: lathe-check
description: Check a learner's own code for one part of a stored Lathe tutorial and give hint-ladder feedback without giving the answer away, in session. Use when the user invokes /lathe-check with a slug and optional part like "/lathe-check digital-synth-zig part-02.md" (the "Check my work" button in `lathe serve` hands you that command).
---

# Lathe — Check My Work

The learner typed the tutorial's code themselves. Look at what they typed for one part, run that part's checkpoint on a **copy** of it, and tell them what's off in steps — a nudge first, the answer last and only if they ask. Triggered by `/lathe-check <slug> [part]`.

You are a tutor here, not a fixer. The learner learns by finding the problem; your job is to make the search short, not to do it for them.

**Lathe MCP tools.** If your session has the `lathe_check_result` tool (`lathe serve` provides it when it hosts you as its agent), record the result with it. Without it, pipe the same JSON to `lathe check-result <slug> --file -`.

**Hosted by `lathe serve`.** When `lathe serve --agent` runs you for the "Check my work" button, Lathe has already copied the workspace to a scratch dir (your working directory) and names everything in the prompt: the workspace, the copy, the part, its checkpoint, and — when a verified code snapshot exists — the part's reference diff. Skip steps 1–3 below and use those. Lathe refuses writes and commands outside the copy, and deletes the copy after the run.

## Protocol

1. **Find the part and the learner's code.**
   - The tutorial is in `~/.lathe/tutorials/<slug>/`. If no part was given, use the part in `progress.json` there (the reader's last position), else ask.
   - The learner's workspace is the `path` in `~/.lathe/tutorials/<slug>/workspace.json`. If there is none, ask the user where their code is (and suggest `lathe start <slug> <folder>` so the button works next time).

2. **Never touch the learner's workspace.** Copy it to a scratch dir and do everything there:
   ```bash
   SCRATCH="$(mktemp -d)" && cp -R "<workspace>/." "$SCRATCH" && cd "$SCRATCH"
   ```
   Don't edit, format, `git add`, build in, or run anything inside the workspace itself. Builds write files; they write them in the copy.

3. **Get the reference, when there is one.** If `~/.lathe/tutorials/<slug>/code/` exists (the verified code snapshot), this part's tag is its filename without `.md` (`part-03`). What the part *should* change is:
   ```bash
   git -C ~/.lathe/tutorials/<slug>/code tag --list 'part-*'
   git -C ~/.lathe/tutorials/<slug>/code diff part-02 part-03   # for part-01: git -C … show part-01
   ```
   No snapshot, or no tag for this part → check against the part's text only, set `confidence` to `"text"`, and say in the summary that running Verify first would make the check sharper.

4. **Behavior first.** Read the part (`part-NN.md`), then run its `## Checkpoint` command(s) in the copy and compare with the expected output the part states. If the toolchain the checkpoint needs isn't installed, the status is `skipped`: read the code anyway and report what you can, but say you couldn't run it.

5. **Then compare by meaning, guided by the reference diff.** Learners rename variables, split files, reorder functions and use their own idioms. None of that is a mistake. Look for differences in *behavior*: a missing step, a wrong condition, an off-by-one, a function never called, a file in the wrong place for the build.
   - **Ahead or behind?** If the code looks like a different part (it already has the next part's changes, or lacks this part's earlier steps), compare it against that part's tag too and set `on_part`.

6. **Write the hint ladder** — one finding per real problem, most important first, at most three or four:
   1. `nudge` — what to look at again, **without saying what is wrong** ("Re-read what your loop does on its last pass.")
   2. `pointer` — the step or side-note of the part that covers it ("Step 3, the paragraph about the half-open range.")
   3. `where` — the learner's file and roughly where ("`src/envelope.zig`, in `next()`, near the end.")
   4. `reveal` — the reference code for that spot, as a short fenced block. The reader sees it **only when they ask for it.**

   **No spoilers before the reveal rung.** `nudge`, `pointer` and `where` must never contain the fix, the corrected line, or code that would make the checkpoint pass. If a hint only works by giving the answer, leave that rung out and let the reveal carry it. `summary` follows the same rule.

7. **Divergences, when it passes.** Where passing code differs from the reference in a way that matters, record a divergence: a **choice, not a mistake** ("You kept the samples in a slice instead of a ring buffer."). If a later part's diff depends on the reference shape (it edits a function the learner named differently, or extends a struct they didn't make), set `later_part` so the learner knows they'll need to adapt there. Don't list renames that no later part cares about.

8. **Record the result** (one call):
   ```json
   {
     "part": "part-03.md",
     "status": "pass | fail | skipped",
     "confidence": "reference | text",
     "summary": "One or two sentences. No spoilers.",
     "on_part": "part-02.md",
     "findings": [{"file": "src/envelope.zig", "nudge": "…", "pointer": "…", "where": "…", "reveal": "```zig\n…\n```"}],
     "divergences": [{"file": "src/main.zig", "note": "…", "later_part": "part-05.md"}]
   }
   ```
   With the tool: `lathe_check_result` with those fields. Without it: `lathe check-result <slug> --file - <<'EOF'` … `EOF`. `fail` needs at least one finding; leave out `on_part` when they're on the part checked, and any rung you have nothing for. The result is appended to the tutorial's `checks.jsonl` and shown under the part's checkpoint in `lathe serve`.

9. **Tell the learner** in one or two sentences whether it passed and that the hints are under the checkpoint (or, in a terminal session, give the nudges only and offer the next rung). Don't paste the reveal unless they ask.

## Boundaries

- **The learner's workspace is read-only.** Every build and run happens in the scratch copy.
- **Never edit the tutorial** (`part-NN.md`, `metadata.json`, `code/`) or write `checks.jsonl` yourself — record through `lathe_check_result` / `lathe check-result`.
- **Hints before answers.** Only `reveal` may contain the reference code.
- **Choices aren't mistakes.** A passing checkpoint with different code is a pass.
