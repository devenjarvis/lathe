# Lathe Tutorial Verifier

Verify that a technical tutorial works end-to-end on this machine by working through it step by step.

## Setup

The tutorial to verify is at the absolute path in the `LATHE_TUTORIAL_DIR` environment variable.
Your working directory (the project dir) is a temp directory — write all code and create all files here only.
Never write files outside the current working directory.

## Process

1. Read `$LATHE_TUTORIAL_DIR/metadata.json` to determine the files to check:
   - If `series: true`, process each filename listed in `parts` in order
   - If `series: false`, process `index.md`
2. For each file, read it completely, then work through every step in order:
   - Create any code files the tutorial instructs you to create (in the current working directory)
   - Run every command shown in the tutorial
   - At each "Checkpoint" section, run the exact verification command shown
3. Track the step number (1-indexed, reset per part)
4. If any command fails or produces unexpected output, record the failure and stop immediately

### What to skip

The tutorial may include callouts and other non-load-bearing content. **Do not** execute commands or write files based on:

- Anything inside a `> [!ASIDE]`, `> [!DESIGN-NOTE]`, `> [!NOTE]`, `> [!TIP]`, `> [!WARNING]`, or `> [!HEADS-UP]` blockquote callout — these are illustrative or advisory, not part of the build.
- The `## Exercises` section — these are reader homework, not part of the verified path.
- The `## What's next` section (series only) — narrative bridge, no commands.

The load-bearing path is: code blocks and commands in the main body of each section, plus every `## Checkpoint` block.

## Reporting: Success

Write `$LATHE_TUTORIAL_DIR/verify-result.json`:
```json
{"status": "verified", "checked_at": "<RFC3339 timestamp>"}
```

Then update `$LATHE_TUTORIAL_DIR/metadata.json`: change the `"status"` field value to `"verified"`. Do not modify any other fields.

## Reporting: Failure

Write `$LATHE_TUTORIAL_DIR/verify-result.json`:
```json
{
  "status": "failed",
  "part": "<filename of the part that failed, e.g. part-02.md>",
  "failed_step": <step number as integer>,
  "error": "<exact error message or output from the failed command>"
}
```

Then update `$LATHE_TUTORIAL_DIR/metadata.json`: change the `"status"` field value to `"failed"`. Do not modify any other fields.

## Rules

- Only create or modify files inside the current working directory
- Never modify the tutorial markdown files
- If a required tool is not installed (e.g., `zig` binary not found), treat it as a failure: `"error": "required tool not installed: zig"`
- Count steps per part, resetting to 1 for each new part file
