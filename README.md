# Lathe

An experiment in using LLMs to teach you, rather than think for you.

Lathe generates hands-on, multi-part technical tutorials on demand, with skills tuned to make content approachable. Then you work through them yourself, by hand, in a local UI built from the ground up for pleasant learning. (Just like we did it in the stone age 😎)

<img src="docs/img/read-tutorial.png" alt="Reading a tutorial in the Lathe UI" width="800">

## What is it?
- Generate hands-on technical tutorials (single-part or a multi-part series) from any prompt
- Work through the tutorial yourself in a purpose-built local UI
- Use skills to ask questions, verify the tutorial, and extend it with a new part
- Search, filter, and manage tutorials from your library
- Every tutorial documents its sources, which model was used, and what prompt drove the "voice" for the tutorial

## Quick start

Lathe is a combination of LLM skills and a Golang CLI used to store, manage, and view generated tutorials. After install (below), you can generate a tutorial inside any coding agent (Claude Code, Cursor, Codex, Gemini CLI, opencode, Cline, and Windsurf supported) by prompting something like:

```
/lathe build a 3D Slicer in Erlang
```

<img src="docs/img/lathe-prompt-cc.png" alt="Prompting a coding agent with `/lathe build a 3D Slicer in Erlang`" width="800">

Then open lathe from any terminal:

```bash
lathe serve              # starts the web server, opens the browser
```

<img src="docs/img/tutorial-selection-light-mode.png" alt="Browsing tutorials in lathe (light mode)" width="800">

Don't worry, we also have dark mode:

<img src="docs/img/tutorial-selection-dark-mode.png" alt="Browsing tutorials in lathe (dark mode)" width="800">

Click the tutorial you want to read and start learning! 

The CLI has a bunch of other commands, but honestly those were built to give the LLM a deterministic way to manage tutorials. I expect the above to be all you need (it's all I ever use) for day-to-day. If you want to ask a question about a tutorial, have the LLM verify it, or extend it with an additional part, the UI has affordances for each of these which will give you the exact skill/prompt to give your LLM in order to trigger the action.

## Install

Lathe is a single self-contained binary. All you need is `lathe` on your `$PATH`; the
skills run in an interactive coding-agent session (Claude Code, Cursor, Codex, Gemini
CLI, opencode, Cline, or Windsurf).

**Homebrew** (macOS, recommended):

```bash
brew install devenjarvis/tap/lathe
```

Distributed as a cask (a pre-built binary), so it's macOS-only — on Linux use the
install script or `go install` below.

**Install script** (`curl | sh`):

```bash
curl -sSf https://raw.githubusercontent.com/devenjarvis/lathe/main/install.sh | sh
```

**Go** (needs Go 1.25+):

```bash
go install github.com/devenjarvis/lathe@latest
```

**From source:**

```bash
git clone https://github.com/devenjarvis/lathe
cd lathe
go build -o lathe
```

### Install the skills

The skills are bundled into the binary. After installing `lathe`, drop them into a
project so your coding agent can discover them:

```bash
lathe skills install                   # ./.claude/skills/<name>/SKILL.md (this project)
lathe skills install --user            # ~/.claude/skills/<name>/SKILL.md (all projects)
lathe skills install --agent cursor    # ./.cursor/commands/<slug>.md (Cursor slash commands)
lathe skills install --agent codex     # ./.agents/skills/<name>/SKILL.md (Codex Agent Skills)
lathe skills install --agent gemini    # ./.gemini/skills/<name>/SKILL.md (Gemini CLI)
lathe skills install --agent opencode  # ./.opencode/skills/<name>/SKILL.md (opencode)
lathe skills install --agent cline        # ./.cline/skills/<name>/SKILL.md (Cline)
lathe skills install --agent windsurf     # ./.windsurf/skills/<name>/SKILL.md (Windsurf)
lathe skills install --agent antigravity  # ./.antigravity/skills/<name>/SKILL.md (Antigravity)
lathe skills install --agent all          # every target above
lathe skills list                      # show the bundled skills
```

`SKILL.md` (name + description frontmatter) is now a cross-tool standard, so every
target except Cursor ships the raw skill verbatim — Claude Code, Codex, Gemini CLI,
opencode, Cline, and Windsurf all read it as-is, and `--user` installs to that agent's
home dir (Cursor and Windsurf are project-only and warn + fall back). Cursor is the
lone translation case: its commands are slash-invoked as `/<slug>` (e.g. `/lathe`).
The interactive handoff model is documented against Claude Code, so a few runtime
details differ on the other agents.

### Local / self-hosted models

Lathe doesn't talk to any model itself, so a local LLM needs no Lathe-specific setup:
point your coding agent at a local OpenAI-compatible endpoint (e.g. [Ollama](https://ollama.com/)'s
`http://localhost:11434/v1`) and run the `/lathe` skills exactly as above. The bigger
the local "thinking" model you can run, the better the tutorials — these are research-
and explanation-heavy tasks, not mechanical edits.


## Why does this exist?

I learned how to program as a teen in the 2000s by building homebrew games for my PSP (PlayStation Portable) in Lua, and then in C++. Lots of what I learned at the time was through the small PSP homebrew community I'm incredibly grateful I got to be a part of, but I also owe much of that formative learning to free online resources and tutorials available on the internet (shoutout to [2007 cplusplus.com](https://web.archive.org/web/20070213043837/http://www.cplusplus.com/articles/Sacha1.html) - man does that site have a lot more ads now than it used to 😅). Eventually I became a professional software engineer and I spent the next decade "upskilling" (though usually to learn more interesting topics than needed for <dayjob>) by finding and consuming a wealth of technical blogs, and more importantly for my learning style - hands on tutorials. Resources like the [build-your-own-x repo](https://github.com/codecrafters-io/build-your-own-x), and [Crafting Interpreters](https://craftinginterpreters.com/), and the 1,000 other one-off tutorials that taught me everything from building [a raytracer](https://raytracing.github.io/books/RayTracingInOneWeekend.html), to [a timeseries database](https://nakabonne.dev/posts/write-tsdb-from-scratch/), to [a linear algebra matrix library](https://www.andreinc.net/2021/01/20/writing-your-own-linear-algebra-matrix-library-in-c/) and everything in between (seriously, I couldn't even begin to list all the amazing hands-on tutorials out there that have influenced me). 

Hands on learning is how I've always learned best. These tutorials gave me the learning curve I needed to go from zero-to-one in a brand new domain, but even more importantly they gave me footing and confidence to take it from one-to-two-to-ten on my own. 

Fast forward to 2026, and now we've got LLMs. I'm not going to go off topic about my complicated relationship with LLMs, but for writing software they are _interesting_ and in many cases they can be really productive! But they do most of the work for you, and with that work gone they also take away the part that helped me learn a new concept or domain. In some cases, that doesn't matter - we've got a product to ship and LLMs help us ship it faster - but for me and my joy in this field and hobby I still crave those "ah ha!" moments where something finally clicks and I have the confidence I need to begin shaping it into my own.

So lathe is an experiment in using LLMs to teach me, rather than think for me. To recreate those moments of hands-on learning that taught me to love this work, and marry it with the _potential_ of a broad "expert" LLM who can, in theory, teach me anything. I use lathe as a catalyst to get me started on projects I wouldn't know how to start in, and **can't find any existing human written resources to teach**. For example I first came up with lathe because I wanted to write a 3D Slicer Software from scratch (just finding documentation on g-code was a pain, shoutout to [reprap](https://reprap.org/wiki/G-code)). At the time of writing I'm diving into the world of embedded software development with Zig. Both of these cases lathe has been an effective tool in getting me from zero-to-one in obscure or extremely young domains where the human written resources just don't exist yet (and I wonder for how long humans will still bother writing tutorials if only the LLMs read them...).

## But what about hallucinations?

Are lathe tutorials as good as ones written by humans? Not in the slightest. But what they lack in heart, personality, and architectural soundness, they make up for by having the tutorial writer ready and waiting to answer all of your questions, always willing to fix or update their tutorial when it isn't _exactly_ what you wanted, and they actually complete writing all 6 parts to that series they started in 2018 (we've ALL been there 😁). Lathe is an LLM, and while I've built and tuned it to be as good as I know how to make it for this particular task, it's still going to fail in the ways LLMs fail. I recommend using the biggest "thinking" model you have access to (Opus, GPT-5 Codex, etc) as these tasks are less about iterative mechanical execution you might optimize for when programming, and more about researching, designing, and explaining a tangible concept from start to finish. 

Additionally, the risk for hallucinations in this context is, in my opinion, significantly lower. Lathe is built to help _you_ do the thinking, and is built around the expectation that you're the one typing this code out yourself. By reading through the guide and typing it out, you are actively engaged in the work and should be well positioned to naturally ask "wait, does that make sense?" when you come across something weird. At which point you can `/lathe-ask` (and sometimes the LLM comes back with _good_ reasoning I didn't have because it's a foreign domain, and I learn something) or just straight tell your LLM to update the tutorial. While I have no pedagogical credentials to back this up, I think I may be actually internalizing concepts better by catching and pushing back on perceived slip-ups of the LLM. YMMV. 

All of that said, if you can find a tutorial written by a human, I'd always reach for that first. I hope more often than not you do. But if you learn the same way I do and want to dive into a domain that is light on teaching materials, lathe is a pretty cool tool. Just remember it is an LLM and not a human. To help with this, I try to make it clear at all times what you are and are not getting. The lathe skills to write tutorials will tell you when it isn't sure about something it has written, and while I offer a more "personal" voice, I've defaulted to one that doesn't pretend to be something it isn't. 

## Be honest, did you vibecode this? Isn't that contradictory to your thesis?

Yep, lathe is "vibecoded". In this case, the scope and risk of lathe is low. It's a living thesis, for personal learning. That said, I've been using it daily lately and it's proven to be a useful and stable tool in my toolbox. I'm learning a lot by using it, and at this point I think it's good enough that others might benefit from it too. I expect the next few point releases to be some intentional code/architecture clean up to ensure it remains stable for others, and of course incorporate any feedback I get. 

That said, for the sake of transparency, today I test lathe for my own usecases - using Claude Code on macOS. Lathe is built to be agent-agnostic (the skills are a cross-tool standard and the CLI never calls a model itself), so other agents and platforms _should_ work, but I've only verified my own setup. If you're willing to try it on a different agent or OS and it does work, or you end up hitting a bump in the road, I'd love an issue letting me know either way!

## Alright then, how does it work?

- **LLM skills** — generate and work with tutorials, all run in your interactive coding-agent session: `/lathe` writes `part-01.md`, `/lathe-extend` adds the next part, `/lathe-verify` works through a tutorial to confirm it compiles and runs, `/lathe-ask` answers questions about a part you're reading, and `/lathe-tag` adds search tags to existing tutorials. 
  - The Go binary never drives a model — all model work runs in your interactive agent session, so it stays on whatever subscription or endpoint that agent uses. (Concretely, this also keeps Lathe off metered headless runs like Claude Code's `claude -p`, which is planned to be metered as of 2026-06-15.) Maybe down the line the cost proves minimal — generating tutorials doesn't consume a lot of tokens compared to vibecoding — and we move some of these interactions back into the UI. We'll see!
- **`lathe` CLI** (Go) — copies tutorials into `~/.lathe/tutorials/`, serves the rendered output at `http://localhost:4242`, and owns all durable state. It never calls an LLM itself: the web buttons and the `lathe verify`/`lathe extend` commands just hand you the skill command to paste into your session, and the skills call back into the CLI (`lathe store`, `lathe verify-result`, `lathe extend-start`/`extend-commit`, `lathe voice add`) to record results.

## What's up with the fancy UI?

I'm glad you asked! The lathe skills and CLI were built in tandem to offer (what I think is) a great reading and learning experience. A few key features that make using lathe worth more than just prompting Claude directly (for me) are:

### Full table of contents navigation if you hover on the right side bar

<img src="docs/img/table-of-contents.png" alt="Table of contents in generated lathe tutorial" width="800">

### Content is written with side-notes throughout to prompt me to think more deeply

<img src="docs/img/tutorial-content.png" alt="Example content of a generated lathe tutorial" width="800">

### Left-to-the-reader Exercises at the end of each tutorial

<img src="docs/img/exercises.png" alt="Example exercises at the end of a generated lathe tutorial" width="800">

## Writing voices

Every tutorial is written in a **voice**. A voice controls *how the prose sounds* but it doesn't change accuracy, research, citation, verification, or structure, which are fixed. Two voices ship with lathe:

- **`plainspoken`** (the default) — honest and precise, with no invented persona
  or fabricated first-person war stories. It's written to avoid anthropomorphizing
  the LLM that produced it.
- **`companion`** — an attempt at a warm, wry, first-person "friend at the keyboard".

Pick one per run by naming it in your `/lathe` invocation (*"…in the companion
voice"*), or change the global default:

```bash
lathe voice list                     # see what's available; * marks the default
lathe voice show companion           # print a voice's full spec
lathe voice set-default companion    # change the default for new tutorials
```

**Custom voices.** If you don't like the voices that come with lathe that's cool, you do you. You can author your own with `/lathe-voice` in an LLM session, and it'll interview you about register, person, and humor, draft a spec, and (on
your approval) save it via `lathe voice add <name> --file -` into
`~/.lathe/voices/`.

Custom voices are instructed to not impersonate a real named person, fabricate credentials, or deny LLM authorship. `/lathe-voice` refuses those, and every voice is wrapped with a fixed preamble enforcing the same at generation time. The voice a tutorial was written in is recorded on it (so `/lathe-extend` continues in it) and is disclosed in an authorship byline at the top of every tutorial: `Generated by <Model> · voice <name>` where the model is the specific LLM used to generate the tutorial (e.g. "Claude Opus 4.8"), and the voice name expands to reveal the full spec. 

I fully recognize this is a cat and mouse game, and that any attempts at safety here can be circumvented. Unfortunately, whether I publish lathe or not the bad actors who want to flood the world with AI slop tutorials are already going full steam ahead. I want to do my part though to make it clear that lathe is NOT intended for writing content outside of your personal use for your personal learning. 

## Finding tutorials

As your library grows, the web list page (`lathe serve`) has a search box and filters to narrow it down — all client-side, so it stays fast and offline:

- **Search** matches a tutorial's title, topic, tags, repo, and tool versions.
- **Sort** by newest, oldest, or title (A–Z).
- **Filter** by status, by type (single vs. series), by tag, and by version.

Default port is `4242`; override with `--port`.

## Storage layout

Tutorials live globally in `~/.lathe/tutorials/`, one directory per slug:

```
~/.lathe/tutorials/
  digital-synth-zig/
    metadata.json
    part-01.md
    part-02.md
    part-03.md
  database-from-scratch-go/
    metadata.json
    index.md
```

`metadata.json`:

```json
{
  "slug": "digital-synth-zig",
  "title": "Build a Digital Synth in Zig",
  "topic": "build a digital synth in Zig",
  "created": "2026-05-03T19:00:00Z",
  "status": "unverified",
  "tags": ["zig", "audio", "dsp"],
  "parts": ["part-01.md", "part-02.md", "part-03.md"],
  "tools": [{ "name": "zig", "version": "0.13.0" }],
  "sources": ["https://ziglang.org/documentation/0.13.0/"],
  "voice": "plainspoken",
  "model": "Claude Opus 4.8"
}
```

Everything beyond the core fields (`slug`/`title`/`topic`/`created`/`status`) is optional and omitted when empty: `tools` (the languages/toolchains the tutorial targets, surfaced as version chips and the **Versions** filter), `sources` (the research trail — see below), `voice` and `model` (the byline on the reading page), and `repo`/`repo_branch` when a tutorial was written against a specific git repository.

Status is one of `unverified` (the default after `lathe store`; renders no badge), `verifying`, `verified`, `failed`, `skipped`, or `extending` (set while `/lathe-extend` is writing a new part). On failure, a `verify-result.json` is written alongside with the failed part, step number, and error output; the web UI renders it as a panel on the tutorial page.

## Sources & provenance

Every tutorial keeps the research trail behind it — the URLs the generation skill actually consulted while writing. This is distinct from the inline `## Sources` citations inside a part's markdown: it's a durable, tutorial-level record stored in the `sources` field of `metadata.json` and surfaced in the UI as provenance, so you can sanity-check where the material came from rather than taking the prose on faith.

- `/lathe` captures them via `lathe store --source <url>` (repeatable), and `/lathe-extend` folds any newly-consulted URLs into the same trail (`lathe extend-commit --source`), de-duped against what's already there.
- On the **list page**, each card shows a `· N sources` count in its metadata line.
- On the **reading page**, a *"Researched against N sources"* panel expands to the full list of links.

<img src="docs/img/sources-and-extend.png" alt="Sources section at the end of a generated lathe tutorial" width="800">


## Verification

Verification is **opt-in** and runs in your interactive LLM session. Storing a tutorial leaves it `unverified` and nothing runs until you ask. The `lathe verify <slug>` command, the `--verify` flag on `lathe store`, and the **Verify this tutorial** button in the web UI all just hand you the same command to paste into your session:

```
/lathe-verify <slug>
```

The `/lathe-verify` skill works through every step in the tutorial, creating files in a fresh `mktemp -d` scratch dir (never your repo), running commands, executing each `## Checkpoint` block and then calls `lathe verify-result` to record the outcome in the tutorial's `metadata.json`. It marks the run `verifying` when it starts and a terminal `verified` / `failed` / `skipped` when it finishes.

Verification only makes sense where the tutorial's toolchain is installed. If a required tool is missing (e.g. no `zig` binary), the run is reported as **skipped** (⚠️) rather than failed — "couldn't verify here" is not the same as "broken."

Because verification now runs in your own interactive session, it executes under your normal LLM permission model, so you see and approve the tool calls. The scratch-dir convention keeps build artifacts out of your repo, but treat it as soft isolation at best, not a security boundary.
