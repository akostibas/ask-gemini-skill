#!/usr/bin/env bash
#
# smoke-test.sh — end-to-end check that ask-gemini can reach the real Gemini
# API with the configured model and credentials.
#
# Unlike `go test`, this makes a live, billable API call. Run it after changing
# the default model, the API endpoints, or the request shape.
#
# Usage:
#   bin/smoke-test.sh [model-id]
#
# Exit codes: 0 = pass, 1 = setup/precondition failure, 2 = API call failed.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

# --- Report preconditions before doing anything ---

if [[ -z "${GEMINI_API_KEY:-}" && -z "${ASK_GEMINI_KEY_COMMAND:-}" ]]; then
  echo "FAIL: no credentials. Set GEMINI_API_KEY or ASK_GEMINI_KEY_COMMAND." >&2
  echo "      This script makes a real API call and cannot run without a key." >&2
  exit 1
fi

model="${1:-}"
if [[ -n "$model" ]]; then
  echo "Model:   $model (override)"
else
  echo "Model:   default (compiled into binary)"
fi

# --- Build a fresh binary into a temp dir ---

bindir="$(mktemp -d)"
trap 'rm -rf "$bindir"' EXIT
echo "Building ask-gemini..."
ver="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
go build -ldflags "-X main.version=$ver" -o "$bindir/ask-gemini" ./cmd/ask-gemini

# --- Make the call against an isolated session ---

session="smoke-$$"
args=(--reset --session "$session")
[[ -n "$model" ]] && args+=(--model "$model")
args+=("Reply with exactly the word: pong")

echo "Calling Gemini..."
set +e
output="$("$bindir/ask-gemini" "${args[@]}" 2>&1)"
status=$?
set -e

# Clean up the session file regardless of outcome.
"$bindir/ask-gemini" --reset --session "$session" >/dev/null 2>&1 || true

if [[ $status -ne 0 ]]; then
  echo "FAIL: ask-gemini exited $status" >&2
  echo "----- output -----" >&2
  echo "$output" >&2
  exit 2
fi

if [[ -z "$output" ]]; then
  echo "FAIL: API call succeeded but returned an empty response." >&2
  exit 2
fi

echo "Response: $output"
echo "PASS: live API call succeeded."
