---
name: lathe-ask
description: Answer a reader's question about a specific part of a Lathe tutorial, in session. Use when the user invokes /lathe-ask with a slug and part like "/lathe-ask digital-synth-zig part-02.md" followed by the question on the next line (the "Ask" button in `lathe serve` pastes exactly this).
---

# Lathe — Ask About a Part

Answer a reader's question about one part of a stored tutorial, grounded in what that tutorial actually built. Triggered by:

```
/lathe-ask <slug> <part-NN.md>
<the reader's question>
```

The web "Ask" button pastes exactly that: the slug and part on the command line, the question on the next line. Parse all three.

## Protocol

1. **Read the part** at `~/.lathe/tutorials/<slug>/<part-NN.md>`. Read sibling parts (`part-NN.md` in the same dir) when the question reaches across parts or depends on earlier setup — continuity matters here too.

2. **Answer grounded in this tutorial's concrete artifact** — the same controlling example, the same numbers, the same voice the tutorial used. The reader is asking about *their* synth / *their* key-value store, not the topic in the abstract. Don't re-teach the whole topic from scratch.

3. **Point at the tutorial, don't re-derive it.** Prefer "look at the `process_buffer` loop in §3 — the modulo there is doing X" over a fresh ground-up explanation. You're a guide standing next to them at the page they're reading.

4. **Be honest about gaps.** If the question exposes something the tutorial got wrong, glossed over, or left under a `[!UNVERIFIED]` flag, say so plainly — don't paper over it. That's more useful than a confident hand-wave.

5. **Stay engaged** for follow-ups. This is a conversation, not a one-shot reply.

## Boundaries — read-only, conversational

- **There is no `lathe ask` command.** This skill writes nothing and calls back into no CLI command — ask is deliberately conversation-only.
- **No state mutation:** don't touch `metadata.json`, `verify-result.json`, or the part markdown. Don't verify, don't extend, don't tag.
- Keep answers specific to this tutorial's concrete artifact, in its voice.
