# ask-gemini

A small CLI that asks Google Gemini for a second opinion, with conversation
history across calls and support for attaching photos, videos, and audio. Ships
with a [Claude Code](https://docs.claude.com/en/docs/claude-code/overview) skill
that wraps it.

## Install

```sh
go install github.com/akostibas/ask-gemini-skill/cmd/ask-gemini@latest
```

This installs an `ask-gemini` binary into `$(go env GOPATH)/bin` — make sure
that's on your `PATH`.

## Configure credentials

`ask-gemini` reads the API key in this order:

1. The `GEMINI_API_KEY` environment variable.
2. The `ASK_GEMINI_KEY_COMMAND` environment variable, treated as a shell command
   whose stdout is the key.

Pick whichever fits your secrets workflow. Examples:

```sh
# Plain env var
export GEMINI_API_KEY=...

# 1Password CLI
export ASK_GEMINI_KEY_COMMAND='op item get "Gemini API" --fields password --reveal'

# pass
export ASK_GEMINI_KEY_COMMAND='pass show gemini/api-key'

# macOS keychain
export ASK_GEMINI_KEY_COMMAND='security find-generic-password -s gemini-api -w'
```

`ASK_GEMINI_KEY_COMMAND` is meant to invoke a secrets manager — don't inline the key itself (e.g. `export ASK_GEMINI_KEY_COMMAND='echo AIza...'`), since the command and its arguments are visible to other processes via `ps`. If you want the key directly in the environment, use `GEMINI_API_KEY`.

## Install the Claude skill

Copy or symlink the skill file into your Claude skills directory:

```sh
mkdir -p ~/.claude/skills/ask-gemini
ln -s "$(pwd)/skill/SKILL.md" ~/.claude/skills/ask-gemini/SKILL.md
```

Then invoke it as `/ask-gemini` in Claude Code. See [`skill/SKILL.md`](skill/SKILL.md)
for the full skill instructions.

## Usage

```
ask-gemini [flags] <prompt>
echo 'prompt' | ask-gemini [flags]
echo 'payload' | ask-gemini [flags] <prompt>
```

When both a positional prompt and piped stdin are given, the stdin body is
appended to the arg prompt (separated by a blank line) — useful for putting a
framing question in the arg and the payload on stdin.

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

File API uploads are retained by Google for ~48 hours, so a single
`--session` consult can keep referencing attachments across turns without
re-uploading.

## Bugs and feature requests

Open an issue at https://github.com/akostibas/ask-gemini-skill/issues. Include the command you ran, what happened (stdout/stderr/exit code), what you expected, and a minimal repro.

## License

MIT — see [LICENSE](LICENSE).
