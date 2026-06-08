#!/usr/bin/env bash
#
# update.sh — upgrade this skill (binary + skill files) to the latest GitHub
# release. Portable self-update pattern: to reuse in another skill, copy this
# file and change the REPO / SKILL_NAME / BIN vars below. The repo must ship a
# Makefile whose `install` target accepts a SKILL_DIR override (see this
# project's Makefile).
#
# What it does:
#   1. Resolves its own directory as the skill install location — so it upgrades
#      wherever it was installed (~/.claude or a project-level .claude).
#   2. Reads the latest release tag from the GitHub API.
#   3. Without --yes, prints the plan and exits (so the caller can confirm).
#      With --yes (or an interactive yes), clones the tag into the system temp
#      dir, runs `make install SKILL_DIR=<here>`, then removes the checkout.
#
# No files are written outside the system temp dir; the checkout is removed on
# exit. Requires git, curl, and the Go toolchain (same as the original install).
#
# Usage:
#   update.sh            # show the plan (current → latest), confirm if a TTY
#   update.sh --yes      # apply without prompting (the agent-driven path)

set -euo pipefail

# --- Portable configuration: edit these three to reuse in another skill -------
REPO="akostibas/ask-gemini-skill"
SKILL_NAME="ask-gemini"
BIN="ask-gemini"            # binary that reports --version; "" if the skill has none
# -----------------------------------------------------------------------------

API_URL="https://api.github.com/repos/${REPO}/releases/latest"
CLONE_URL="https://github.com/${REPO}.git"

die() { echo "${SKILL_NAME} update: $*" >&2; exit 1; }

assume_yes=false
[[ "${1:-}" == "--yes" || "${1:-}" == "-y" ]] && assume_yes=true

# Skill install location = this script's own directory.
SKILL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Current installed version (best-effort; empty if the binary isn't on PATH).
current=""
if [[ -n "$BIN" ]] && command -v "$BIN" >/dev/null 2>&1; then
  current="$("$BIN" --version 2>/dev/null || true)"
fi

# Latest release tag from GitHub. jq if present, else a tag_name grep.
command -v curl >/dev/null 2>&1 || die "curl not found"
release_json="$(curl -fsSL -H "Accept: application/vnd.github+json" "$API_URL" 2>/dev/null)" \
  || die "could not reach GitHub (offline or rate-limited)"
if command -v jq >/dev/null 2>&1; then
  latest="$(printf '%s' "$release_json" | jq -r '.tag_name // empty')"
else
  latest="$(printf '%s' "$release_json" | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*"\([^"]*\)"$/\1/')"
fi
[[ -n "$latest" ]] || die "could not determine the latest release tag"

echo "Skill dir: $SKILL_DIR"
echo "Current:   ${current:-unknown}"
echo "Latest:    $latest"

if [[ -n "$current" && "$current" == "$latest" ]]; then
  echo "Already up to date."
  exit 0
fi

# --- Confirm -----------------------------------------------------------------
if ! $assume_yes; then
  if [[ -t 0 ]]; then
    read -r -p "Upgrade ${SKILL_NAME} to ${latest}? [y/N] " reply
    [[ "$reply" =~ ^[Yy]$ ]] || { echo "Aborted."; exit 0; }
  else
    echo
    echo "Re-run with --yes to upgrade (reinstalls the binary and skill to the locations above)."
    exit 0
  fi
fi

# --- Clone the tag into the system temp dir and install ----------------------
tmp="$(mktemp -d "${TMPDIR:-/tmp}/${SKILL_NAME}-update.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

echo "Cloning ${latest} into a temp checkout..."
git clone --quiet --depth 1 --branch "$latest" "$CLONE_URL" "$tmp" \
  || die "git clone of $latest failed"

echo "Installing (make install SKILL_DIR=$SKILL_DIR)..."
# make install removes and rewrites this script's file. POSIX unlink semantics
# keep the running script's open inode readable, so self-replacement is safe.
make -C "$tmp" install SKILL_DIR="$SKILL_DIR" \
  || die "make install failed"

echo "Upgraded ${SKILL_NAME} ${current:-?} → ${latest}."
