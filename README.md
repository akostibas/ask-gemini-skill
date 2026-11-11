# ask-gemini

A small CLI that asks Google Gemini for a second opinion, with conversation
history across calls and support for attaching photos, videos, and audio. It
can also generate images and video, and answer quick yes/no and multiple-choice
questions for automations. Ships with a
[Claude Code](https://docs.claude.com/en/docs/claude-code/overview) skill that
wraps it. What it's for, and what it isn't: [docs/intent.md](docs/intent.md).

## Setup

From a local clone (installs the binary *and* the Claude skill):

```sh
make install
```

Or just the binary, from anywhere:

```sh
go install github.com/akostibas/ask-gemini-skill/cmd/ask-gemini@latest
```

`make install` puts the binary in `~/bin` and copies `skill/` to
`~/.claude/skills/ask-gemini/`; override either with `BIN_DIR=...` or
`SKILL_DIR=...`. Rerunning it is safe. `go install` puts the binary in
`$(go env GOBIN)`, falling back to `$(go env GOPATH)/bin`. If you used
`go install` and want the Claude skill too, symlink it:

```sh
ln -s "$(pwd)/skill/SKILL.md" ~/.claude/skills/ask-gemini/SKILL.md
```

Then configure credentials. `ask-gemini` reads the API key in this order:

1. `GEMINI_API_KEY` — the key directly.
2. `ASK_GEMINI_KEY_COMMAND` — a shell command whose stdout is the key.

```sh
export GEMINI_API_KEY=AIza...

# or, via a secrets manager:
export ASK_GEMINI_KEY_COMMAND='op item get "Gemini API" --fields password --reveal'
export ASK_GEMINI_KEY_COMMAND='security find-generic-password -s gemini-api -w'
```

Don't inline the key into `ASK_GEMINI_KEY_COMMAND` (e.g. `'echo AIza...'`) —
command arguments are visible to other processes via `ps`. Use
`GEMINI_API_KEY` for that.

## Usage

```
ask-gemini [flags] <prompt>
echo 'prompt' | ask-gemini [flags]
echo 'payload' | ask-gemini [flags] <prompt>
```

When both a positional prompt and stdin are given, stdin is appended to the
prompt, separated by a blank line.

Flags:

- `--session <name>` — name the conversation; persisted at
  `$TMPDIR/ask-gemini-<name>.json`. Reuse the name for follow-up turns.
- `--reset` — delete the named session and exit.
- `--photo <path>` — attach an image (repeatable).
- `--video <path>` — attach a video (repeatable). Waits for File API processing.
- `--audio <path>` — attach an audio file (repeatable).
- `--out <path>` — generate an image (or a video, for `.mp4`) and write it to `<path>` (see below).
- `--resolution <360p|720p|1080p|4k>` — video resolution (default `360p`).
- `--model <id>` — override the model (default `gemini-4-argon`).
- `--system <prompt>` — system prompt; only applied on the first turn.
- `--history` — print the current conversation and exit.
- `--version` — print the binary version and exit.

File API uploads are retained by Google for ~48 hours, so a single `--session`
can keep referencing attachments across turns without re-uploading.

### Usage and cost

After every request, a token-usage line and an estimated cost print to
**stderr** (so they never mix into the answer on stdout):

```
usage: 54 in + 74 out (73 thinking) = 128 tokens
est. cost: ~$0.000848 (gemini-4-argon, prices as of 2026-10)
```

The token counts come straight from the API and are exact; "thinking" is hidden
reasoning tokens (billed at the output rate). The dollar figure is an **estimate**
from a small price table baked into the tool (`cmd/ask-gemini/pricing.go`) — it
can drift when Google changes prices, so it's stamped with the date it was last
verified. A model with no entry in that table still prints tokens, with
`cost: unknown`. Silence the whole line with `ASK_GEMINI_NO_USAGE=1`.

### Generating images (Nano Banana)

Pass `--out <path>` to generate an image instead of text:

```sh
ask-gemini --out logo.png "a minimalist fox logo, flat vector style"
```

- `--out` auto-selects an image model (`gemini-nano-banana-2.1`,
  Nano Banana 2.1) when you don't pass `--model`. Override with any
  image-capable model, e.g. `--model gemini-3.1-flash-image` (Nano Banana 2),
  `--model gemini-3-pro-image` (Nano Banana Pro),
  `--model gemini-3.1-flash-lite-image` (cheapest) or
  `--model gemini-2.5-flash-image`. Pairing `--out` with a text model — or an
  image model without `--out` — is rejected up front.
- The image is written to `<path>`; if the model returns several, they get
  `-1`, `-2` suffixes (`logo-1.png`, `logo-2.png`). Any accompanying text is
  printed to stdout. The model chooses the format (often JPEG); if it doesn't
  match your extension, a note is printed but the file is still written to the
  path you gave.
- Combine with `--session` to edit iteratively — a follow-up turn (no `--out`
  change needed) sees the previous image and can refine it. Attach reference
  images with `--photo` to guide or edit.
- Grounding tools (search, URL context) don't apply in image mode.

### Generating video (Gemini Omni)

Give `--out` a video extension (`.mp4`) to get a 3–10 second clip with sound:

```sh
ask-gemini --out fox.mp4 "a paper fox unfolding itself on a desk"
ask-gemini --out wave.mp4 --photo fox.png --resolution 720p "the fox waves"
```

- Uses `gemini-omni-1.1-flash`. `--photo` animates a still image.
- `--resolution` is `360p` (default, fastest), `720p`, `1080p` or `4k`. Video is
  billed per second: ~$0.03/s at 360p up to ~$0.30/s at 4k, so a 10s 720p clip
  is about $1. The clip length and estimated cost print to stderr.
- Each clip is a one-off: `--session` follow-up edits aren't supported yet.

### Quick decisions (`decide`)

For closed questions (yes/no, pick-one, graded rubric), `decide` uses
`gemini-decision-1` and prints the answers as JSON, each with probabilities and
a 0–1 confidence:

```sh
ask-gemini decide --file cam.jpg --yesno 'rat=Is there a rat?' \
  --choice 'who=Who is visible?:kid,adult,nobody'
ask-gemini decide --file diff.patch --questions checks.json   # - reads stdin
```

- Yes/no results carry `"answer": "yes"|"no"`. `confidence` is for that answer,
  not for "yes"; `noul` is the chance of yes. Check `answer` plus a confidence
  threshold.
- Input is any mix of `--file` (image, video, audio, PDF or text) and positional text.
- `--questions` takes up to 1,000 questions in the
  [API's format](https://aistudio.google.com/docs/decisions-api), including
  rubrics and yes/no criteria, which have no flag. Flags merge into it.
- Keys default to `q1`, `q2`, …; `--resolution low|medium|high|ultra_high` trades
  image detail for cost.
- About $0.10 per 1M input tokens and no output charge, so a camera snapshot
  costs a few thousandths of a cent. It can't answer open-ended questions; use
  a normal prompt for those.

## Staying up to date

On a real consult, `ask-gemini` checks GitHub for a newer release — at most once
a day, cached to the system temp dir — and prints a one-line notice to stderr if
you're behind. All failures (offline, rate-limited) are silent. To upgrade, run
the skill's `update.sh`, which clones the latest tag into the system temp dir and
reinstalls the binary and skill in place:

```sh
~/.claude/skills/ask-gemini/update.sh         # preview current → latest
~/.claude/skills/ask-gemini/update.sh --yes    # apply
```

It needs `git`, `curl`, and the Go toolchain. You can also just re-run
`make install` from a fresh clone, or `go install ...@latest` for the binary
alone.

**Privacy:** the update check is an unauthenticated GitHub API call, which
reveals your IP to GitHub. Set `ASK_GEMINI_NO_UPDATE_CHECK=1` to disable it.

## Bugs and feature requests

Open an issue at https://github.com/akostibas/ask-gemini-skill/issues. Include
the command you ran, what happened (stdout/stderr/exit code), what you
expected, and a minimal repro.

## License

MIT — see [LICENSE](LICENSE).
