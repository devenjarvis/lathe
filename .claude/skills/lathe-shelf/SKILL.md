---
name: lathe-shelf
description: Propose how to file the Lathe library on shelves, in session — a shelf for every tutorial ("/lathe-shelf sort") or a two-way split of a shelf that has grown too big ("/lathe-shelf split <shelf-id>"). Use when the user invokes /lathe-shelf (the "Sort my shelves…" and split-banner buttons in `lathe serve` hand you that command). Records proposals only; the reader applies them.
---

# Lathe — Shelve the Library

Shelves are how the reader's library is organized in `lathe serve`: every tutorial sits on exactly one shelf (or on the virtual **Unsorted** shelf). This skill proposes filing; it never moves anything. Lathe shows your proposal to the reader as a before/after list (sort) or a two-column preview (split), and **nothing moves until they click Apply**.

Two modes:

- `/lathe-shelf sort` — propose a shelf for **every** tutorial in the library.
- `/lathe-shelf split <shelf-id>` — propose splitting one shelf into **two** smaller shelves.

**Lathe MCP tools.** When `lathe serve` hosts you for these buttons, your session has Lathe's shelf tools: `lathe_shelf_list`, and `lathe_shelf_propose` (sort) or `lathe_shelf_split_propose` (split). Use them. Without them, run the CLI commands written below; they do the same thing.

## Read the library

1. **The shelves:** `lathe shelf list --json` (or the `lathe_shelf_list` tool) — each shelf's `id`, `name`, one-line `description` and tutorial `count`, in the reader's order, Unsorted last.
2. **The tutorials:** `lathe list` gives each slug and title; each tutorial's `~/.lathe/tutorials/<slug>/metadata.json` has its `topic`, `tags` and `tools`, and its `part-01.md` opens with what it builds. When `lathe serve` hosts you, its prompt already lists every tutorial with these fields and its current shelf — use that and don't re-read files you don't need.

## How to pick shelves (both modes)

- **A shelf is a subject the reader would browse by**, named the way a bookshop names a section: short (1–3 words), a plural noun or a field — *Compilers*, *Audio & DSP*, *Databases*, *Operating systems*, *Web*. Not a language (the library already filters by language and version) unless the reader's shelves are clearly organized that way, and never a single tutorial's title.
- **Reuse the reader's shelves first.** An existing shelf that fits is always better than a new one, even if you'd have named it differently; match its exact name. Read the shelf descriptions: they say what the reader meant the shelf for.
- **Propose a new shelf only for a real cluster** — two or more tutorials that share a subject no existing shelf covers. A lone tutorial with no fitting shelf goes on the closest existing one, or stays Unsorted (propose `Unsorted`) when nothing is close.
- **Aim for shelves of roughly 3–12 tutorials.** A handful of well-filled shelves beats a dozen shelves of one.
- **Leave the reader's own filing alone** unless it is clearly wrong: a tutorial already on a fitting shelf keeps it (propose that same shelf, so the review shows it as unchanged).

## `sort` — a shelf for every tutorial

1. Read the library (above) and decide the shelf set first — existing shelves plus any new ones — then assign every tutorial to one of them.
2. Record one proposal per tutorial, **every** tutorial in the library, including the ones whose shelf doesn't change:
   ```bash
   lathe shelf propose <slug> "<shelf name or id>"
   ```
   or `lathe_shelf_propose` with `slug` and `shelf`. A name no shelf has yet is a proposed new shelf; Lathe creates it only if the reader applies. Proposing again for the same slug replaces your earlier pick.
3. Report briefly: the shelves you used (which are new) and how many tutorials move. Tell the reader to review it in **Settings › Library** (`lathe serve` → Settings → Library), where they can untick any move and apply the rest.

## `split <shelf-id>` — split one shelf in two

The reader's shelf has grown past their split threshold.

1. Read the shelf's tutorials (the ones whose current shelf is `<shelf-id>`) and find the **one cut** that divides them into two coherent subjects, as evenly as the subjects allow (7/6 is better than 12/1; never leave a group empty).
2. Name each half like any shelf (above). The **first group keeps the shelf** — its page, place in the library and links — renamed to the first group's name, so put the larger or more central half first. The second becomes a new shelf right after it (or joins an existing shelf with that exact name, if one fits).
3. Record it once, with every tutorial on the shelf in exactly one group:
   ```bash
   lathe shelf propose-split <shelf-id> --group "Interpreters=lox-tree-walker,forth-in-zig,…" --group "Compilers=llvm-kaleidoscope,…"
   ```
   or `lathe_shelf_split_propose` with `groups: [{name, slugs}, {name, slugs}]`. If it refuses (a slug missing, on another shelf, or in both groups), fix the groups and call it again.
4. Report the split in one line ("Interpreters (7) and Compilers (6)") and say the reader can preview it on the shelf's page before anything moves.

## Boundaries

- **Propose only.** Never run `lathe shelf set`, `lathe shelf new`, or edit `~/.lathe/shelves.json` here — the reader applies proposals in the web UI.
- Don't read or change anything outside `~/.lathe/` for this; no generation, verification or tagging.
