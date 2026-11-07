---
name: lathe-voice
description: Author a custom writing voice for Lathe tutorials, in session, then persist it via the CLI. Use when the user invokes /lathe-voice (optionally with a name like "/lathe-voice terse") to craft a new tone/register preset that /lathe can generate in.
---

# Lathe — Author a Voice

Help the user craft a **custom writing voice** — a tone/register preset that
`/lathe` and `/lathe-extend` can generate tutorials in — then persist it through
the CLI. Triggered by `/lathe-voice [name]`.

A voice controls **tone and register only**. It never changes accuracy,
research, citation, verification, substance, pedagogy, or structure — those are
fixed invariants in the `lathe` skill, and every voice is wrapped at read time
with a preamble that says so. You are authoring *how the prose sounds*, nothing
more.

## What a voice is (and what it is not)

Look at the built-ins for the exact shape before you draft — they are the
template:

```bash
lathe voice show plainspoken     # the honest, non-anthropomorphic default
lathe voice show companion       # the warm, first-person original
lathe voice list                 # everything currently available
```

A voice spec is a small markdown file with `name:`/`description:` frontmatter and
three working sections, plus a sample:

- **Stance and register** — persona, point of view, sentence rhythm, how the
  reader is addressed, the first-person policy, humor.
- **Avoid** — the tonal anti-patterns this voice rejects.
- **Calibration — before / after** — two or three ❌/✅ pairs showing the voice's
  tone (not pedagogy; the pedagogy before/afters live in the `lathe` skill).
- **Sample** (`## Sample`, last) — one paragraph of about 100 words that
  explains a **ring buffer** in this voice. Every voice explains the same thing,
  so the reader can compare voices side by side in Settings › Voices ("Sounds
  like"). Write plain prose: no headings, lists, code or markdown emphasis.

The sample is for comparing voices only. `lathe voice show` leaves the
`## Sample` section out of the spec it prints, so it never reaches generation —
which is also why you won't see one when you run `lathe voice show` on a
built-in. It must still follow the voice and the guardrail below: tone only, no
invented credentials or experience presented as fact.

## Refuse to author these (deception guardrail)

Decline, and explain why, if the request is to make a voice that:

- **Impersonates a real, named person** ("write exactly like <specific living
  author/engineer>", or as a named public figure) or implies their endorsement.
  A register *inspired by* a tradition is fine ("plain, in the spirit of good
  systems writing"); writing *as* a specific real person is not.
- **Fabricates credentials or authority** — a voice that claims real-world
  experience, institutions, or qualifications the author doesn't have, or that
  presents invented anecdotes as things that actually happened. (A voice may use
  first person *as a stance*, like `companion`, but it must not manufacture a
  fake résumé.)
- **Denies LLM authorship** or is built to pass the tutorial off as
  human-written.
- **Is coercive or deceptive** — manipulative urgency, dark patterns, dishonesty
  toward the reader.

These aren't negotiable: the wrap preamble and the `lathe` invariants would
override such instructions at generation time anyway, so a voice that depends on
them is dead on arrival. Steer the user to an honest version of what they want.

## Protocol

1. **Pick the name.** Use the `/lathe-voice <name>` argument if given; otherwise
   ask for a short, lowercase slug (e.g. `terse`, `socratic`, `field-notes`). It
   must not collide with a built-in (`plainspoken`, `companion`) — `lathe voice
   add` will reject that, so check `lathe voice list` first.

2. **Interview the user.** Ask, briefly (one round, grouped — don't interrogate):
   - **Register:** formal ↔ casual? dense ↔ spacious? dry ↔ warm?
   - **Person:** first person ("I"), collaborative ("we"), or impersonal? If
     first person, is it a *stance* (opinions) or are they hoping for fabricated
     experience? (If the latter, redirect per the guardrail.)
   - **Humor:** none, dry/sparing, or playful? Never at the reader's expense.
   - **Anti-patterns:** what should this voice never sound like? (Pull concrete
     words/phrases to feed the Avoid list.)

3. **Draft the spec.** Write it in the built-in structure: frontmatter (`name`,
   a one-line `description`), `# <Title>`, `## Stance and register`, `## Avoid`,
   `## Calibration — before / after` with two or three ❌/✅ tone pairs, and
   last `## Sample`: the ring buffer paragraph, in this voice. Keep it tonal —
   don't restate accuracy/structure rules; those are invariants.

4. **Show it and confirm.** Print the full draft to the user. Iterate until they
   approve. Don't persist anything unapproved.

   **Refining an existing voice.** When the name is already a custom voice (the
   reader ran `/lathe-voice <name>` from Settings › Voices' "Edit with agent",
   or on a built-in they duplicated), start from `lathe voice show <name>`
   (drop the guardrail preamble above its `---` line, which Lathe adds back;
   its description is in `lathe voice list`, and the frontmatter goes back on) and
   ask what to change instead of interviewing from scratch. Write a fresh
   `## Sample` for the revised voice, since `show` doesn't print the old one.
   Saving under the same name replaces the voice.

5. **Persist via the CLI.** On approval, pipe the spec to:
   ```bash
   lathe voice add <name> --file -
   ```
   (The skill writes no files itself — the CLI owns `~/.lathe/voices/`. `--file -`
   reads the spec from stdin.) If your session has the `lathe_voice_add` MCP tool
   (from `lathe mcp`), call it with `name` and the full `spec` instead. If either
   errors on a built-in collision, pick another name and retry.

6. **Tell the user how to use it:**
   - Generate in it now: `/lathe <topic>` and name the voice (*"…in the `<name>`
     voice"*), or make it the default with `lathe voice set-default <name>`.
   - Inspect or remove it later: `lathe voice show <name>` / `lathe voice rm
     <name>`.

## Hosted: Settings › Voices › New voice

When Lathe hosts you for its **New voice** page, the reader has already filled
in a brief (name, how it should sound, a voice to start from, maybe writing
they like) and watches a preview instead of a chat. The protocol changes so:

- **No interview.** The brief replaces step 2. Ask only if it is genuinely
  unclear, as one short question that ends your turn.
- **Every draft goes to `lathe_voice_draft`** (the whole spec, `## Sample`
  last) instead of being printed (step 4). The page shows its sample as the
  preview and the full spec on request. End your turn with one short sentence
  about the draft; the reader's next message asks for a change (record a new
  draft each time) or tells you they clicked **Save voice**.
- **Only the reader saves.** Call `lathe_voice_add` only after Lathe says they
  clicked Save voice. In that session it takes no spec: it saves the draft they
  approved, exactly as drafted, under the name Lathe reserved. Don't use
  `lathe voice add`.
- **No files or commands.** The session has none; read another voice with
  the `lathe_voice_show` tool instead of `lathe voice show`.
- The deception guardrail above still applies: record no draft for a request it
  refuses; say why and offer the honest version.

## Boundaries

- The **only durable-state write** is `lathe voice add` (and, if the user asks,
  `lathe voice set-default` / `lathe voice rm`). Never write to `~/.lathe/`
  directly.
- Author tone only. If the user wants to change pedagogy, structure, or accuracy
  behavior, that's a change to the `lathe` skill, not a voice — say so.
