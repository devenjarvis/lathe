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
three working sections:

- **Stance and register** — persona, point of view, sentence rhythm, how the
  reader is addressed, the first-person policy, humor.
- **Avoid** — the tonal anti-patterns this voice rejects.
- **Calibration — before / after** — two or three ❌/✅ pairs showing the voice's
  tone (not pedagogy; the pedagogy before/afters live in the `lathe` skill).

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
   and `## Calibration — before / after` with two or three ❌/✅ tone pairs.
   Keep it tonal — don't restate accuracy/structure rules; those are invariants.

4. **Show it and confirm.** Print the full draft to the user. Iterate until they
   approve. Don't persist anything unapproved.

5. **Persist via the CLI.** On approval, pipe the spec to:
   ```bash
   lathe voice add <name> --file -
   ```
   (The skill writes no files itself — the CLI owns `~/.lathe/voices/`. `--file -`
   reads the spec from stdin.) If it errors on a built-in collision, pick another
   name and retry.

6. **Tell the user how to use it:**
   - Generate in it now: `/lathe <topic>` and name the voice (*"…in the `<name>`
     voice"*), or make it the default with `lathe voice set-default <name>`.
   - Inspect or remove it later: `lathe voice show <name>` / `lathe voice rm
     <name>`.

## Boundaries

- The **only durable-state write** is `lathe voice add` (and, if the user asks,
  `lathe voice set-default` / `lathe voice rm`). Never write to `~/.lathe/`
  directly.
- Author tone only. If the user wants to change pedagogy, structure, or accuracy
  behavior, that's a change to the `lathe` skill, not a voice — say so.
