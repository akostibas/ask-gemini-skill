---
name: ask-gemini
description: Get a second opinion from Gemini on thorny problems. Calls the Gemini 3.5 Flash API with conversation history for multi-turn consults.
---

# Ask Gemini — Second Opinion Consult

**Prereqs:** the `ask-gemini` binary is on `PATH` and either `GEMINI_API_KEY` or `ASK_GEMINI_KEY_COMMAND` is set in your environment. See the project README for installation and credential setup.

Get a second opinion from Google's Gemini 3.5 Flash on a tricky problem. Useful for:
- Debugging dead ends — describe what you've tried and what's failing
- Architectural second opinions — "here are two approaches, which is better?"
- Code review of a specific function or pattern
- Sanity-checking an unusual approach
- Showing Gemini photos or videos for visual analysis

## CLI

The tool is `ask-gemini`. It maintains conversation history across calls so you can have a multi-turn consult.

```
ask-gemini [flags] <prompt>
echo 'prompt' | ask-gemini [flags]
echo 'payload' | ask-gemini [flags] <prompt>
```

When both a positional prompt and piped stdin are given, stdin is appended to the arg prompt (separated by a blank line) — handy for putting a framing question in the arg and a long payload on stdin.

Flags:
- `--session <name>` — name the conversation; stored at `$TMPDIR/ask-gemini-<name>.json` (falls back to `/tmp` if unset). Always pass this from the skill so concurrent Claude sessions don't trample each other.
- `--reset` — start a fresh conversation (use on first call per consult topic)
- `--photo <path>` — attach an image (repeatable). Uploaded via Gemini's File API and referenced by URI.
- `--video <path>` — attach a video (repeatable). Uploaded via the File API; the CLI waits for processing to complete (`ACTIVE` state) before sending.
- `--audio <path>` — attach an audio file (repeatable). Uploaded via the File API; the CLI waits for `ACTIVE` state before sending.
- `--model <id>` — override model (default: `gemini-3.5-flash`)
- `--system <prompt>` — custom system prompt (only used on first turn)
- `--history` — show the current conversation and exit
- `--version` — print the binary version and exit

Notes on attachments:
- File API uploads are retained for 48 hours, so a `--session` consult can keep referencing them across turns without re-uploading.
- Photos and videos can be combined in the same call. Multiple of each are allowed.

## How to Use This Skill

When the user invokes `/ask-gemini`, follow this process:

### 1. Pick a session name

**Always pass `--session <name>`.** Choose a short, slug-style name tied to the consult topic (e.g. `--session auth-rewrite`, `--session pyspark-perf`). Reuse the same name for follow-ups in the same consult so Gemini retains context. Use a fresh name (or `--reset`) when starting a new topic.

### 2. Formulate the question

Gather context for a good consult. Include:

- **The problem statement** — what you're trying to do and what's going wrong
- **Relevant code** — paste the specific code in question (not huge files, just the relevant parts)
- **What's been tried** — approaches attempted and why they didn't work
- **Follow up questions** — ask for a round of follow up questions to ensure Gemini has sufficient context.

Write this up as a single clear prompt. Think of it as briefing a smart colleague who just sat down.

In your next message:

- **Answer questions** — follow up on Gemini's questions, doing research if needed
- **The specific question** — now, ask what you want Gemini's take on

Take advantage of the fact that Gemini is a different model with a different thought process. It is
a collaborator that you can benefit from, and will make your work stronger.

### 3. Call the CLI

Use `--reset` on the first call for a new topic. It deletes any existing session file with that name, then sends your prompt against a fresh conversation:

```bash
ask-gemini --session <topic> --reset "Your well-formulated question here"
```

For follow-up questions in the same consult (Gemini remembers the conversation), drop `--reset`:

```bash
ask-gemini --session <topic> "Follow-up question here"
```

Attaching media:

```bash
ask-gemini --session ui-bug --reset \
  --photo ./screenshot1.png --photo ./screenshot2.png \
  "What's wrong with the alignment in the second screenshot vs the first?"

ask-gemini --session demo-review --reset \
  --video ./recording.mov \
  "Review this UX flow — where does the user appear confused?"
```

You can also pipe longer prompts via stdin if the question is too long for a shell argument.

### 4. Relay the response

The user has **not seen the prompt you sent to Gemini** — only your synthesis. Treat Gemini's response as raw input that needs to be re-framed for someone with no prior context:

- Don't reference labels, option letters, or section names from your prompt without restating what they mean.
- Restate the question and the options being weighed before giving the recommendation.
- Make the synthesis self-contained: a reader should understand the conclusion without seeing either your prompt or Gemini's full reply.

Then add your own analysis on top:
- Where you agree or disagree with Gemini's take
- How Gemini's suggestion relates to the current codebase
- Whether you'd recommend acting on the advice

Do NOT just parrot back the response — add value by synthesizing both perspectives.

### 5. Continue the consult if needed

If the user wants to dig deeper, send follow-up questions on the same `--session` (without `--reset`) to continue the conversation with Gemini.

## Keeping the skill up to date

`ask-gemini` checks GitHub for a newer release at most once a day (cached in the
system temp dir) and prints a one-line notice to **stderr** when one exists, e.g.:

```
ask-gemini: update available v0.2.0 → v0.3.0. Run this skill's update.sh to upgrade (suppress with ASK_GEMINI_NO_UPDATE_CHECK=1).
```

When you see that notice, tell the user a newer version is available and offer to
upgrade — don't upgrade silently. The updater lives next to this file:

```bash
"$(dirname SKILL.md)/update.sh"          # preview: prints current → latest, then stops
"$(dirname SKILL.md)/update.sh" --yes    # apply after the user agrees
```

`update.sh` installs the new binary and skill to wherever *this* skill is
installed (so a project-level `.claude` install upgrades in place), cloning into
the system temp dir and cleaning up after itself. It needs `git`, `curl`, and the
Go toolchain. The check makes an unauthenticated GitHub API call, which reveals
the user's IP to GitHub; set `ASK_GEMINI_NO_UPDATE_CHECK=1` to disable it.

## Reporting bugs or requesting features

If `ask-gemini` misbehaves (silent failures, confusing errors, missing flags, wrong-looking output) or the skill instructions are unclear, **open a GitHub issue** at:

https://github.com/akostibas/ask-gemini-skill/issues

Do not leave a `bug_report*.txt` file in the user's working directory — it will sit unnoticed. File the issue directly so it lands in the project tracker.

Before filing, use `gh issue list -R akostibas/ask-gemini-skill --search "<keyword>"` to check for a duplicate. A good bug report includes:
- **What you ran** — the exact command line and any relevant env (`GEMINI_API_KEY` set? `ASK_GEMINI_KEY_COMMAND` set?).
- **What happened** — stdout, stderr, exit code. Note especially silent / exit-0-with-no-output cases.
- **What you expected.**
- **Minimal repro** — the shortest sequence that reproduces it.
- **Binary version** — `ask-gemini --version` (or `ls -la $(which ask-gemini)` for the build mtime).

File via:

```bash
gh issue create -R akostibas/ask-gemini-skill --title "<short title>" --body-file <path-to-writeup>
```
