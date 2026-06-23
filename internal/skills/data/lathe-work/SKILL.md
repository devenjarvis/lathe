---
name: lathe-work
description: Run the Lathe worker loop so the web UI's Ask / Verify / Add-a-part buttons drive work directly in this session instead of handing back a command to paste. Use when the user invokes /lathe-work (start it once per session while `lathe serve` is running). Works in any coding agent.
---

# Lathe — Worker Loop

Start a long-running loop that lets the `lathe serve` web UI drive Ask, Verify, and Add-a-part **directly in this session**. With this loop running, the browser buttons enqueue a job and you pick it up here — no copy-paste of a `/lathe-*` command. Triggered by `/lathe-work`.

This is just **long-poll → do the model work → report → repeat**, so it works in *any* supported coding agent. The strict boundary still holds: **the binary never drives a model.** All model work runs here, in your normal interactive session — never via `-p` / headless. Verify runs the tutorial's code under exactly the same trust model as `/lathe-verify` does today (a fresh `mktemp -d`, your normal permissions).

When this loop is **not** running, the buttons fall back to today's copy-paste handoff — so starting it is purely additive.

## Prerequisite

`lathe serve` must be running (the loop talks to it via `~/.lathe/serve.json`). If `lathe work next` reports "no lathe server is running", tell the user to start `lathe serve` in another terminal, then start the loop.

## The loop

Repeat until the user stops you (Ctrl-C, "stop the worker", or closing the session):

1. **Claim the next job:**
   ```bash
   lathe work next
   ```
   This long-polls (~50s) and prints **either** `no task` **or** a single JSON object like:
   ```json
   {"id":"7","type":"verify","slug":"digital-synth-zig","part":"part-02.md","question":"…","guidance":"…","state":"claimed"}
   ```
   - If it prints `no task`, **loop back to step 1 immediately** — that's just an idle long-poll, not an error.
   - Otherwise parse the JSON and note `id`, `type`, `slug`, and (depending on type) `part`/`question`/`guidance`.

2. **Dispatch on `type`**, applying the existing protocol as the source of truth — don't duplicate or paraphrase it, *apply* it:

   - **`verify`** → apply the **`/lathe-verify`** protocol against `slug` exactly as written (it marks the tutorial `verifying`, follows it in a fresh scratch dir, and records the outcome via `lathe verify-result`). When it finishes, close the job:
     ```bash
     lathe work done <id>
     ```

   - **`extend`** → apply the **`/lathe-extend`** protocol against `slug`, passing `guidance` (when present) as the guidance for where the new part should go. It does the full reserve → write → `lathe extend-commit` handshake. When it finishes, close the job:
     ```bash
     lathe work done <id>
     ```

   - **`ask`** → apply the **`/lathe-ask`** protocol against `slug` / `part` / `question`. The one difference from the chat flow: the reader is in the browser, not here, so **return the answer through the CLI** instead of only replying in chat. Pipe your full markdown answer to:
     ```bash
     lathe work answer <id> --answer -
     ```
     (`--answer -` reads the answer from stdin, the same stdin pattern `lathe voice add --file -` uses.) The browser is polling for it and will render it in the reader's Ask drawer. `work answer` closes the job for you — don't also call `work done` for an ask.

3. **Briefly note** in chat what you just handled (e.g. "Verified digital-synth-zig — clean" or "Answered a question on part-02"), then **loop back to step 1.**

## Keeping the loop responsive and its context small

This loop is long-running and each job is independent — nothing carries over from one job to the next — so keep the dispatcher thin and don't let it block or accumulate full job transcripts.

- **Dispatch each job to a fresh sub-context, in the *background* if you can (best).** When you can spawn a sub-task/subagent with its own context window (e.g. Claude Code subagents), run each claimed job there: the sub-task applies the matching `/lathe-*` protocol and **closes the job itself** (`lathe work done` / `lathe work answer`). Two payoffs: the dispatcher's context grows by a sentence per job instead of a whole verify/extend transcript, and — crucially — **if your agent can run the sub-task in the background (e.g. `run_in_background`), do that and immediately go back to `lathe work next` instead of waiting on it.** The sub-task self-reports, so the loop never needs its return value; meanwhile your continued polling keeps worker presence fresh during a long verify/extend (a foreground/blocking dispatch would stop polling and let presence lapse, so a mid-job button click would fall back to copy-paste). Keep the outer loop to just: claim → fire the job into a background sub-context → poll again.
- **If you can't background sub-tasks, process one job at a time** — that's fine, the loop simply isn't concurrent. Foreground each job, report it, then poll again.
- **Lean on auto-compaction and restart periodically as a backstop.** If sub-contexts aren't available at all, your agent's automatic context compaction will keep the loop alive, but it's lossy — so periodically stop and re-run `/lathe-work` to reset. This is safe and lossless: the **queue lives in the server**, so any unclaimed job stays queued and a mid-flight claimed job is re-queued after the reclaim timeout. Nothing is lost by restarting.

## Boundaries

- **Reuse the protocols, don't reinvent them.** Each job type is just "run the matching `/lathe-*` skill, then report." All the real rules (read-only verify, the extend handshake, grounded ask answers) live in those skills and win on any conflict.
- **Always close the job.** `verify`/`extend` → `lathe work done <id>`; `ask` → `lathe work answer <id> --answer -` (which closes it). A job left open ties up the browser until the server's reclaim timeout.
- **Interactive session only.** Never shell out to `-p` / headless to do the work — that's the metered path this whole design avoids.
- **Sequential or concurrent — match your harness.** Without background sub-tasks, finish and report each job before claiming the next. With them, several jobs may be in flight at once; that's fine and keeps presence fresh. Concurrency is safe: the server rejects a second verify/extend on a tutorial that's already verifying/extending, so two jobs can't collide on the same `slug`, and ask jobs are read-only.

## Stop

Stop the loop when the user asks (or close the session). Stopping is safe: the buttons revert to the copy-paste handoff, and any job already claimed but not closed is re-queued by the server after its reclaim timeout.
