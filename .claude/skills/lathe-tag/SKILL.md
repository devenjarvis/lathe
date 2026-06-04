---
name: lathe-tag
description: Pick or backfill the search tags on a stored Lathe tutorial, in session. Use when the user invokes /lathe-tag with a slug like "/lathe-tag digital-synth-zig" to choose good tags for the `lathe serve` search and tag filters, or to backfill tags on a tutorial that has none.
---

# Lathe — Tag a Tutorial

Choose the tags that make a stored tutorial findable in `lathe serve`'s search and tag filters. Triggered by `/lathe-tag <slug>`.

## Protocol

1. **Read the tutorial** at `~/.lathe/tutorials/<slug>/` to understand what it actually teaches.

2. **Pick 2–5 lowercase, reusable tags.** Same vocabulary guidance as the `lathe` skill's tag step: cover, where they apply —
   - the **language/runtime** — `zig`, `rust`, `go`;
   - the **domain** — `audio`, `compilers`, `databases`;
   - the **core technique** — `parsing`, `dsp`, `concurrency`.

   Prefer short, reusable tags that will group naturally with other tutorials over hyper-specific one-offs. Don't pre-lowercase or de-dupe defensively — `store.NormalizeTags` is the one place that canonicalizes (trim, lowercase, de-dupe), so just pass sensible tags and let it normalize.

3. **Persist them.** To set the whole tag list (the usual case — choosing or replacing tags):
   ```bash
   lathe tag <slug> --set zig --set audio --set dsp
   ```
   For incremental edits to an existing set, use `--add` / `--remove` instead:
   ```bash
   lathe tag <slug> --add embedded
   lathe tag <slug> --remove misc
   ```
   `--set` replaces the entire list; `--add`/`--remove` edit it in place. All three are repeatable and accept comma-separated values.

4. **Report the resulting tag set** to the user (the command prints it).

## Boundaries

- Only touches tags, and only via `lathe tag`. Never edit `metadata.json` directly.
- No generation, verification, or extension — just tags.
