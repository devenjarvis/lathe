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

## Boundaries

- **Reuse the protocols, don't reinvent them.** Each job type is just "run the matching `/lathe-*` skill, then report." All the real rules (read-only verify, the extend handshake, grounded ask answers) live in those skills and win on any conflict.
- **Always close the job.** `verify`/`extend` → `lathe work done <id>`; `ask` → `lathe work answer <id> --answer -` (which closes it). A job left open ties up the browser until the server's reclaim timeout.
- **Interactive session only.** Never shell out to `-p` / headless to do the work — that's the metered path this whole design avoids.
- **One job at a time.** Finish and report the current job before claiming the next.

## Stop

Stop the loop when the user asks (or close the session). Stopping is safe: the buttons revert to the copy-paste handoff, and any job already claimed but not closed is re-queued by the server after its reclaim timeout.
