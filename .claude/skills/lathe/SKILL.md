---
name: lathe
description: Generate hands-on technical tutorials for any topic on demand. Use when the user invokes /lathe with a topic like "/lathe build a digital synth in Zig" or "/lathe how to build a compiler in Rust".
---

# Lathe — Tutorial Generator

Generate hands-on technical tutorials for any topic on demand. The bar is the writing of Robert Nystrom (Crafting Interpreters), Sam Who, Julia Evans, Bartosz Ciechanowski. Match it.

## When invoked

The user says something like `/lathe build a digital synth in Zig` or `/lathe how to build a compiler in Rust`. Extract the topic from their message.

1. Ask: **"What's your experience level going in — beginner, some familiarity, or experienced in adjacent areas?"**
2. If the topic is genuinely ambiguous (language? scale? embedded vs. server?), ask **one** clarifying question. Otherwise skip.
3. **Pin the repo and toolchain versions** (see "Pin the repo and versions" below) — do this *before* you research or write, so the tutorial is rooted in the exact versions the reader will use.
4. **Research the topic first** (see below) — this is the single most important step for accuracy. Lathe exists to teach things with little training material behind them, which is exactly where recalled "knowledge" is most likely to be invented. Don't skip it.
5. Run the **Pre-flight** in your head — silently. Don't ask the user to approve the choices.
6. Write Part 1.
7. Hand off to the CLI — clear the **pre-store gate** in "After writing" (state repo, versions, tags, sources or a justified opt-out) *before* running `lathe store`.

## Pin the repo and versions (before research)

Lathe is for **programming tutorials**, and a tutorial is only as trustworthy as the versions it's rooted in. Settle two things before you research or write, then pass them to `lathe store` (see "After writing"). Both flow into `lathe serve`: tutorials are grouped by repo, and the versions show as chips with their own filter — so an old tutorial written against a stale toolchain is identifiable at a glance.

**1. The repo (auto-detect, then confirm).** If this session is inside a git repo the tutorial is *for*, capture it. Detect it:

```bash
git -C "$PWD" remote get-url origin 2>/dev/null   # the remote (preferred grouping key)
git -C "$PWD" branch --show-current 2>/dev/null    # the branch
```

Show the reader what you found — e.g. *"This looks like it's for **devenjarvis/lathe** (branch `main`) — group it there?"* — and let them confirm, correct, or say it's a standalone tutorial with no repo. If there's no remote, no git repo, or the reader says it's standalone, skip the repo (it lands in the "No repo" group). `lathe store` canonicalizes whatever URL you pass to `host/org/repo`, so the raw `origin` URL is fine.

**2. The toolchain versions (detect, then confirm).** Figure out which languages/tools the tutorial will lean on, probe the versions actually installed, and **confirm the target with the reader before you write** — getting this wrong means the whole tutorial teaches against the wrong version. Probe whatever's relevant:

```bash
zig version            # → 0.13.0
go version             # → go1.22.3
node --version         # → v20.11.0
rustc --version        # → 1.75.0
```

Propose them — *"I'll write this against **Zig 0.13.0** and **LLVM 18** — sound right, or do you want a different target?"* — and adjust to whatever the reader says (they may want an older version on purpose). These pinned versions are now a constraint on your prose: cite version-specific behavior accordingly, and if a fact is version-sensitive, anchor it to the pinned version.

## Research first (do this before drafting)

Before you write a single sentence, **go read**. Use your web search and fetch tools to find and *actually open* 3–8 authoritative sources for this topic — official docs, specs/RFCs, primary papers, source code, well-regarded deep-dives. Don't reconstruct them from memory.

- **Read for the load-bearing facts** you'll commit to in the tutorial: exact API/function signatures, default values, flag names, version numbers, sample rates, page sizes, semantic guarantees, error messages, historical claims. Take notes with the URL beside each fact — deep-link to the section or anchor, not the homepage.
- **Ground the prose in what you read, not what you recall.** When a source contradicts your memory, the source wins. When you can't find a source for a load-bearing claim, that's not a green light to assert it confidently — mark it with `[!UNVERIFIED]` (see Callouts) and tell the reader what to check.
- **Keep the list of URLs you consulted.** You'll cite the load-bearing ones inline, list them in each part's `## Sources`, and pass them to `lathe store --source` (see "After writing") so the research trail is recorded as provenance.
- **No web tools in this session?** Say so to the user in one line ("Heads up — I don't have web access here, so I'm working from training knowledge; I've marked the load-bearing claims I couldn't confirm"). Then write more conservatively: claim fewer exact numbers, prefer "check your version's docs" over a guessed default, and `[!UNVERIFIED]`-flag the load-bearing unknowns (still only the load-bearing ones — don't paper the page with caveats).

## Always write Part 1 only

Every `/lathe` invocation produces exactly one file: `part-01.md`. Never write multiple parts in one shot and never write `index.md`. Readers add more parts via the "Add a new part" button in `lathe serve`, which gives them full control over pacing and direction.

## Pre-flight (private — do not ask the user)

Before writing a single sentence, settle these in your head. They are constraints on your prose, not user-facing artifacts.

- **Research and sources.** You've already done the **Research first** step above — by now you've actually read 3–8 authoritative sources and have URLs beside the load-bearing facts. Hold those notes in front of you as you write: cite the load-bearing ones inline, list them in `## Sources`, and `[!UNVERIFIED]`-mark anything you couldn't confirm. A load-bearing claim — a number, a default, a semantic guarantee, a historical fact — is either grounded in a source you read or flagged; it is never asserted from recall as if settled.
- **The controlling example.** Pick one concrete artifact and stay with it. Crafting Interpreters has Lox. You might have *"a 4-voice subtractive synth playing a sustained A minor triad"* or *"a key-value store called `pebble` that survives `kill -9`"*. Don't switch examples mid-tutorial.
- **Specific numbers.** Sample rate, buffer size, page size, table cardinality, latency budget — whatever the domain offers. Numbers are how you earn the reader's trust. Decide them now so they're consistent across parts.
- **One controlling metaphor (optional but powerful).** A mountain. A factory line. A kitchen. If you adopt one, deploy it across at least three section transitions, then *explicitly retire it* with a wink (Nystrom: *"Henceforth, I promise to tone down the whole mountain metaphor thing."*). Don't mix metaphors silently.
- **The closing send-off.** What do you want the reader ready to do *beyond* what you taught? Sketch the "go climb your own mountain" beat now so the body builds toward it.
- **3–5 exercises.** Each specific enough that a motivated reader could start it in 30 seconds.

## Tutorial shape

Every tutorial (or series part) follows this shape, but section *titles* must be specific to the domain. Never `## Step 1: Setup`. Title the thing the section makes: `## A scanner that recognises one-character tokens`.

```
# [Title]

[Hook — 2 to 4 paragraphs. See "Openings".]

## What you'll build

One paragraph. The concrete end state, named with the controlling example.

## Prerequisites

Bullets: tools to install, what the reader should already roughly know.

## [Specific section title — name what this section makes]

Why this exists. Then code, in small blocks, each with an insertion point.
Aside or design note where it earns its keep.

## [...]

## Checkpoint

> [!PREDICT]
> Before you run this: what output do you expect to see?

**Run this to verify your work so far:**
\`\`\`bash
<the exact command>
\`\`\`

Expected output:
\`\`\`
<what they should see>
\`\`\`

**Likely errors:**
- If you see `<exact error text>`, you probably <short causal explanation, e.g. "skipped the import in §2">.
- If you see `<exact error text>`, you probably <short causal explanation>.

## What's next

One paragraph naming the unanswered question a future part will answer. Include in every part — it invites the reader to continue.

## Exercises

1. <specific>
2. <specific>
3. <specific>

## Sources

1. [Title](url) — one sentence on why this source matters for the topic.
2. ...

(Numbered list. Only sources cited inline. Each entry: `[Title](url) — one sentence`. Group by primary docs / papers / deep-dives if more than ~5 entries.)
```

Every part opens with *"By the end of this part, you'll have [specific, concrete thing]"* and closes with a Checkpoint. Every part stands alone with its own `## Exercises` and `## Sources` — since any part may become the last one the reader sees, each must be independently complete.

## Openings

The first sentence has one job: prove this won't be another "in this tutorial we will" page. Pick one of:

- **Concrete scene.** *"It's 3 a.m., production just went vertical, and the only graph still climbing is p99 latency."* Earn the rest by being equally specific from sentence two onward.
- **A claim worth fact-checking.** *"A modern CPU runs roughly a billion arithmetic operations in the time it takes to read one byte from main memory."* Then make that fact matter to what you're building.
- **Epigraph.** A short, attributed quote that frames the chapter. Use sparingly — once per series at most.
- **The reader's confusion, named as a statement.** *"If you've read about hash tables and walked away unsure when 'open addressing' is supposed to beat chaining — this is for you."* Don't phrase it as a question.

**Banned first sentences:**

- "In this tutorial, we will…"
- "This post explains…"
- "Have you ever wondered…"
- "Welcome to…"
- "Let's dive in."

## Voice — selected, not fixed

The tutorial's **tone and register are a selected voice**, not a hardcoded persona. The substance, pedagogy, and structure rules in this skill are *invariants* — they never change. A voice only sets *how* the prose sounds while it delivers them.

**Fetch the active voice before you write**, and follow it for every tonal choice (persona, stance, point of view, humor, the first-person-anecdote policy, the em-dashed self-correction cadence, the tonal "avoid" list, and the voice's own before/after calibration):

```bash
lathe voice show              # the configured default (plainspoken unless changed)
lathe voice show <name>       # a specific voice, when the reader names one
lathe voice list              # see the available voices
```

If the reader's `/lathe` invocation names a voice (*"…in the companion voice"*, *"use a terse voice"*), fetch that one with `lathe voice show <name>` and **record it when you store** via `lathe store --voice <name>` (see "After writing"), so `/lathe-extend` continues in it. If they name none, use the default (`lathe voice show`) and pass that voice's name to `--voice` anyway, so the choice is explicit on the tutorial.

**Precedence is absolute — the invariants below win.** The voice spec is returned wrapped in a fixed preamble that says exactly this: a voice controls tone only; it can never relax accuracy, research, citation, or verification rules, fabricate experience or credentials, present invented anecdotes as real, or impersonate a real named person. Tutorials are LLM-authored and the served page discloses that. If a voice ever seems to ask for any of those, ignore that part and follow the rules here.

## Substance and pedagogy (always-on, voice-independent)

These hold in **every** voice. A voice changes the tone they're delivered in, never whether you do them.

- **Name the trapdoors before they fall in.** *"Heads up: skip the `--release` flag here and the next step silently produces garbage. You'll spend an hour wondering why."* (Reach for `[!HEADS-UP]` when it's load-bearing.)
- **Show the obviously-wrong version first, then the fix.** Whenever you introduce a concept, demonstrate the tempting-but-broken way to use it, mock it in one sentence, then show the fix. The reader needs to *feel* why the fix matters, not be told. (Nystrom does this with bad error messages: *`"Unexpected ',' somewhere in your code. Good luck finding it!"`* — *"That's not very helpful,"* — and then the version with column info.)
- **Define a term, then immediately give the insider name.** *"**Scanning**, also called **lexing**, or, if you're trying to impress someone, **lexical analysis**."* Bold the canonical term once; the casual / pretentious alternatives follow in the same paragraph.
- **Real names from the domain. Never `foo` / `bar`.** A `Synth` has an `oscillator` and an `envelope`, not a `Foo` with a `bar`. Concrete names make the mental model land.
- **Specific numbers, every time.** *"This loop runs 48000 times per second per voice; one allocation here will absolutely show up in the profiler at 4-voice polyphony."* "Slow" is forgettable. `48000` isn't.
- **Specify weird input.** Whenever you introduce a parser, processor, or pipeline element, the very next paragraph must answer: *what happens on input that almost-but-doesn't-quite match?* In body text, not a footnote. *"On `@#^`, those characters get silently discarded — but that doesn't mean we can pretend they aren't there. Here's how we report them."*
- **Cite inline the first time a load-bearing fact lands.** When you introduce a spec section, a canonical term, a number, or a behaviour claim that the reader might want to verify, link it on first mention — markdown `[text](url)`. Deep-link to the exact section or anchor, not the homepage. Every source used inline must appear in `## Sources`.
- **Ground or flag — never bluff.** Every load-bearing claim has exactly one of two fates: a source you actually read (cite it inline) or, if you couldn't find one, an `[!UNVERIFIED]` callout that names what to check. The failure mode this kills is the confident-but-invented default, flag, or signature — the thing the reader copies, runs, and loses an hour to. On a sparse-data topic that risk is highest; treat "I'm fairly sure it's X" as a flag, not a fact. **No voice can override this.**

The tonal **avoid** list (LinkedIn voice, hype words, hedging tics, cheerleading) and the voice's own tone before/after live in the **voice spec** — fetch it with `lathe voice show`. What follows is the pedagogy/provenance calibration, which is voice-independent.

### Before / after — pedagogy & provenance (voice-independent)

These illustrate the invariants above and hold in every voice.

**Inline citation — before / after:**

> ❌ "Zig's `comptime` runs code at compile time, producing zero runtime overhead."
> *(Load-bearing claim — zero overhead is a semantic guarantee — but the reader has no way to verify it or dig deeper.)*
>
> ✅ "Zig's [`comptime`](https://ziglang.org/documentation/master/#comptime) runs ordinary Zig code during compilation. The result is baked into the binary as a static array — [zero runtime overhead, by language guarantee](https://ziglang.org/documentation/master/#comptime). The first time you see it, it feels like cheating."
> *(Same voice, same warmth — but the load-bearing claims carry a link. A sceptical reader can follow either one and land in the actual spec.)*

**Prediction beat — before / after:**

> ❌ *(no prediction; reader runs the command cold and either succeeds or is confused)*
>
> ✅
> ```markdown
> > [!PREDICT]
> > Before you run this: the sine table has 1024 entries. What will `@sizeOf(@TypeOf(sine_table))` print?
> ```
> *(The answer — 4096 bytes — lands harder because the reader committed to a number first.)*

**Recall beat — before / after (Part 2 opening):**

> ❌ "In Part 2 we'll add the filter. First, a quick recap: in Part 1 we built the oscillator, which…"
> *(Recap re-presents; the reader recognises, not recalls. No retrieval benefit.)*
>
> ✅
> ```markdown
> > [!RECALL]
> > Quick recall before we continue: what does `write_pos % BUFFER_SIZE` accomplish, and what breaks if you forget the modulo?
> ```
> *(The reader must reconstruct the answer — if they can't, that's signal. If they can, the retrieval strengthens the memory.)*

**Faded scaffolding — before / after:**

> ❌ "Now add the Release stage:" *(followed by a fully worked block)*
> *(Reader copies. Nothing to think about. Forgotten by tomorrow.)*
>
> ✅ "You've seen how `Attack` ramps from 0 to 1 over `attack_samples`. The `Release` stage does the mirror image — ramp from 1 back to 0 over `release_samples`. Write it now, using the same loop shape, then run the Checkpoint below."
> *(One step ahead of what was shown. Pattern is in front of them. Effort is real but not punishing.)*

**Closing reflection — before / after:**

> ❌ "Great work! You've built a ring buffer, an oscillator, and a filter."
> *(Cheerleading. The reader knows what they built.)*
>
> ✅ "Before you try the exercises: in two sentences, why does the ring buffer beat a `sync.Mutex`-guarded slice here? Write the answer that would satisfy a sceptical colleague."
> *(Forces construction, not recognition. Surfaces gaps before the reader walks away.)*

## Asides and design notes

Two distinct sidebar types, two different jobs. Lathe renders both as styled callouts.

**Aside** — short, inline, one or two sentences. Etymology, war story, a "by the way", a one-line joke that earns its keep. Lives next to the prose that triggered it.

````markdown
> [!ASIDE]
> "Lex" is from the Greek *lexis*, meaning "word." Stash that for the next time someone smugly explains "lexical scope."
````

**Design note** — multi-paragraph digression on *why this is the way it is*: cross-language survey, a tradeoff explored honestly, "how the grown-ups do it." Lives at the **end** of a section, never mid-step.

````markdown
> [!DESIGN-NOTE]
> **Why ring buffers and not channels?**
>
> A few words on the alternative …
````

Other callout types:

- `> [!HEADS-UP]` — trapdoors. Things that will break in 20 minutes if the reader isn't warned now.
- `> [!NOTE]` — neutral side info.
- `> [!TIP]` — handy shortcut, not load-bearing.
- `> [!PREDICT]` — prediction prompt before a Checkpoint or surprising output. One line only.
- `> [!RECALL]` — spaced-retrieval prompt at the top of Part N≥2. One question, load-bearing concept only.
- `> [!UNVERIFIED]` — a **genuinely load-bearing** claim you could not ground in a source you read: one the reader will *act on* and that would cost them real time if it's wrong (a default they'll rely on, a flag they'll type, a signature they'll call). State what you believe and, in the same breath, what to check. *"The default ring-buffer size is 4096 frames — I'm working from memory here and couldn't find this in the docs; confirm it with `default_config()` before you rely on it."* Reserve it for those; not for ordinary hedging or background colour you're merely unsure about. If a claim isn't load-bearing, either confirm it or cut it — don't flag it.

Use them sparingly — `[!UNVERIFIED]` included. Reach for it only when a load-bearing unknown genuinely warrants it (a little more often when you had no web access, but still only for the load-bearing ones). One or two of the others per part, max; a page peppered with caveats reads as low-confidence and is its own kind of clutter. `[!PREDICT]` and `[!RECALL]` are pedagogical, and `[!UNVERIFIED]` is a provenance signal — the verifier skips all three.

## Visual artifacts

Diagrams earn their keep when they show something prose can't:

- A *transformation* (input shape → output shape).
- A *pipeline* (who hands off to whom).
- A *relationship between sets* (Venn-style, taxonomy).

**Don't diagram a sequence of steps.** That's what numbered prose is for.

Tools, by job:

- **Mermaid `flowchart` / `graph`** — pipelines, decision branches, architecture.
- **Mermaid `sequenceDiagram`** — request/response, message passing.
- **Mermaid `stateDiagram-v2`** — protocol states, parser modes, lifecycles.
- **Mermaid `erDiagram`** — schemas and table relationships.
- **Markdown tables** — comparing 2–5 alternatives across a few axes ("which allocation strategy when?"). Tables beat prose for this.
- **ASCII art in a code block** — memory layouts, byte structures, tree shapes that need column alignment.

Aim for **one diagram per part**, only when a moment in that part genuinely benefits. Place it next to the prose that explains it; never drop one in cold without a sentence framing what to look at first. Cap nodes at ~10 — split or convert to a table if larger.

````markdown
```mermaid
flowchart LR
  Source[MIDI input] --> Parser
  Parser --> Voice[Voice allocator]
  Voice --> Osc[Oscillator]
  Osc --> Filter
  Filter --> Out[Audio buffer]
```
````

## Code

- One sentence before every block, telling the reader what to look at first.
- Blocks are 3–15 lines, except for full small files. Larger means split.
- For modifications, name the seam: *"Inside `process_buffer`, just after the voices loop:"*. The reader has to find where to splice.
- No unexplained `...` ellipses. If you elide, name what's elided and why.
- Code is complete enough to run as shown. The reader copies, saves, and sees something predictable happen.

**Faded scaffolding:** The first code block in each part (or tutorial) is fully worked — reader copies, saves, runs, sees output. The last one or two code blocks shift to fill-the-seam: name the pattern the reader has seen, then ask them to write the next instance. *"You've seen how the `Attack` stage ramps from 0 to 1. Using the same pattern, write the `Release` stage — it ramps from 1 back to 0 over `release_time` samples. Then run the Checkpoint."* This is not an open exercise: the reader has the template in front of them and takes exactly one step ahead of where you stopped.

## Endings

Every major section ends with a one-sentence forward-pointer naming the question the next section answers.

Every part ends with **four** things:

1. **A send-off (or forward hook).** For the final part: one short paragraph inviting the reader to leave the path you took. For non-final parts: a single forward-pointing sentence naming the question the next part will answer — lean into the cliffhanger.
2. **A closing reflection.** One self-explanation prompt, in plain prose (not a callout): *"Before you move on: in two sentences, why does the ring buffer beat a channel here? Write it in your own words — the answer that satisfies a sceptical colleague."* Pick the single most important design decision in this part and ask the reader to explain the *why*, not the *what*. Don't answer it for them.
3. **`## Exercises`**, numbered, 3–5 of them. Each specific enough that a motivated reader can start it in 30 seconds. *"Add FM modulation between two oscillators. Routing matrix is up to you — at minimum, let oscillator 2 modulate oscillator 1's frequency."* Not *"explore further."*
4. **`## Sources`**, numbered, one entry per source used inline in *this part*. Format: `[Title](url) — one sentence on why this source matters`. Group by primary docs / papers / deep-dives if more than ~5 entries.

## Output files

Write to `/tmp/lathe-<slug>/`. Slug is the topic in kebab-case.

- "build a digital synth in Zig" → `/tmp/lathe-digital-synth-zig/`
- Always: `part-01.md` — one file, zero-padded so it sorts cleanly.

Decide the slug before writing. Never write `index.md` or multiple parts.

## After writing

> [!HEADS-UP]
> **STOP — pre-store gate. Fill this in before you run `lathe store`.** Repo and
> versions get silently dropped when the store call is reached without them, so
> every store *must* state a concrete value or an explicit, justified opt-out for
> all five flag groups. Omission without a stated reason is not allowed:
>
> - **Repo (`--repo` / `--repo-branch`)** → the repo + branch you pinned in
>   "Pin the repo and versions" above, e.g. `--repo <origin-url> --repo-branch <branch>`.
>   Opt-out only with a reason: *"standalone tutorial, no repo"*.
> - **Versions (`--tool name:version`)** → one `--tool` per toolchain version you
>   pinned in that same step, e.g. `--tool zig:0.13.0 --tool llvm:18`.
>   Opt-out only with a reason: *"no specific toolchain applies"*.
> - **Tags (`--tag`)** → 2–5 lowercase tags (see below).
> - **Sources (`--source`)** → one per authoritative source you read, or the
>   stated reason *"no web access this session"*.
> - **Voice (`--voice`)** → the voice you wrote in (see "Voice — selected, not
>   fixed"): the name the reader chose, or the default from `lathe voice show`.
>   Always pass it so the choice is explicit and `/lathe-extend` can continue it.
> - **Model (`--model`)** → the model *you* are running as, as a display label
>   (e.g. `--model "Claude Opus 4.8"`). Always pass it so the reading-page byline
>   names the author.
>
> Carry the repo/version values straight over from the upfront pinning step — if
> you find you never pinned them, go back and do it now rather than storing blank.

Run:

```bash
lathe store /tmp/lathe-<slug> \
  --tag <a> --tag <b> --tag <c> \
  --repo <origin-url> --repo-branch <branch> \
  --tool <name>:<version> --tool <name>:<version> \
  --source <url> --source <url> \
  --voice <name> \
  --model "<model you are running as>"
```

Choose **2–5** lowercase tags so the tutorial is findable in `lathe serve`'s
search and tag filters. Cover, where they apply: the language/runtime (`zig`,
`rust`, `go`), the domain (`audio`, `compilers`, `databases`), and the core
technique (`parsing`, `dsp`, `concurrency`). Prefer short, reusable tags that
will group naturally with other tutorials over hyper-specific one-offs.

Pass `--repo` and `--repo-branch` with the repo you pinned (see "Pin the repo
and versions"). Give `--repo` the raw `origin` URL — `lathe store` canonicalizes
it to `host/org/repo` for grouping. A standalone tutorial with no repo is a
legitimate case, but dropping these flags is a *deliberate, stated* choice
surfaced by the pre-store gate above (*"standalone tutorial, no repo"*) — never a
quiet default. **Don't put versions in tags**: pass each tool you pinned in that
same step as `--tool name:version` (repeatable, e.g. `--tool zig:0.13.0 --tool
llvm:18`) — these come from the pinned toolchain and must be passed, not folded
into tags. Lathe stores them as structured versions, shows them as chips on the
card, and gives them their own filter — keeping the tag vocabulary clean.

Pass `--source <url>` once for each authoritative source you consulted during
the **Research first** step — the research trail, not just the ones you cited
inline. Lathe records them as provenance: the reading page shows "Researched
against N sources" with the list, and the list page marks how many sources back
each tutorial. If you genuinely had no web access, omit `--source`.

Pass `--voice <name>` with the voice you wrote in — the one the reader named, or
the default you fetched with `lathe voice show`. Lathe records it on the tutorial
so the reading page can disclose it and `/lathe-extend` continues in the same
voice. Use the exact voice *name* (e.g. `plainspoken`, `companion`), not a
description.

Pass `--model` with the model *you* are running as, as a human-facing display
label (e.g. `--model "Claude Opus 4.8"`). Lathe records it on the tutorial and
shows it in the reading-page byline ("Generated by `<model>`"); a tutorial stored
without it falls back to a generic "Claude". State your own identity — don't
guess a different model.

Then tell the user:

- "**Tutorial saved.** Run `lathe serve` to open it at http://localhost:4242."
- "This is Part 1. To add more parts, run `/lathe-extend <slug>` in your Claude Code session (the **'Add a new part'** button in `lathe serve` hands you that command) — give guidance or let it continue naturally."
- "Verification is opt-in: run `/lathe-verify <slug>` in your Claude Code session (the **Verify this tutorial** button in the web UI hands you that command). It needs the tutorial's toolchain installed locally; if a required tool is missing it shows a ⚠️ Skipped badge (not a failure)."
- "Reading in `lathe serve`? The **Ask** button hands you `/lathe-ask <slug> <part>` to ask questions about a part right here in this session."

## Stay in session

Don't end the session. Stay available for:

- *"Why did we structure it this way?"*
- *"Make Part 2 more advanced."*
- *"How'd I do on the checkpoint?"*
- *"What if the buffer overflows?"*

You are their expert guide for this topic. Stay engaged.
