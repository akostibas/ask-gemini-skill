# ask-gemini

A small CLI that asks Google Gemini for a second opinion, with conversation
history across calls and support for attaching photos, videos, and audio. Ships
with a [Claude Code](https://docs.claude.com/en/docs/claude-code/overview) skill
that wraps it.

## Setup

From a local clone (installs the binary *and* the Claude skill):

```sh
make install
```

Or just the binary, from anywhere:

```sh
go install github.com/akostibas/ask-gemini-skill/cmd/ask-gemini@latest
```

The binary lands in `$(go env GOBIN)`, falling back to `$(go env GOPATH)/bin`.
`make install` also copies `skill/` to `~/.claude/skills/ask-gemini/`; override
either with `GOBIN=...` or `SKILL_DIR=...`. If you used `go install` and want
the Claude skill too, symlink it:

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
- `--out <path>` — generate an image and write it to `<path>` (see below).
- `--model <id>` — override the model (default `gemini-3.6-flash`).
- `--system <prompt>` — system prompt; only applied on the first turn.
- `--history` — print the current conversation and exit.
- `--version` — print the binary version and exit.

File API uploads are retained by Google for ~48 hours, so a single `--session`
can keep referencing attachments across turns without re-uploading.

### Generating images (Nano Banana)

Pass `--out <path>` to generate an image instead of text:

```sh
ask-gemini --out logo.png "a minimalist fox logo, flat vector style"
```

- `--out` auto-selects an image model (`gemini-3.1-flash-image-preview`, Nano
  Banana 2) when you don't pass `--model`. Override with any image-capable
  model, e.g. `--model gemini-3-pro-image-preview` (Nano Banana Pro) or
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
