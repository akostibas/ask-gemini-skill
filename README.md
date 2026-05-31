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
- `--model <id>` — override the model (default `gemini-3.5-flash`).
- `--system <prompt>` — system prompt; only applied on the first turn.
- `--history` — print the current conversation and exit.
- `--version` — print the binary version and exit.

File API uploads are retained by Google for ~48 hours, so a single `--session`
can keep referencing attachments across turns without re-uploading.

## Bugs and feature requests

Open an issue at https://github.com/akostibas/ask-gemini-skill/issues. Include
the command you ran, what happened (stdout/stderr/exit code), what you
expected, and a minimal repro.

## License

MIT — see [LICENSE](LICENSE).
