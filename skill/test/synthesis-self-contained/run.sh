#!/usr/bin/env bash
#
# run.sh — behavioral eval for the ask-gemini skill's step 4 ("Relay the
# response"). Verifies an assistant following the skill produces a synthesis the
# user can read WITHOUT having seen the prompt sent to Gemini (issue #3).
#
# This is an EVAL, not a unit test: the model under test is nondeterministic, so
# we run N trials per scenario and require a pass-rate threshold.
#
# It drives the REAL `claude` CLI agent loop running the full skill, which makes
# LIVE, BILLABLE calls to both Anthropic (model under test + graders) and Gemini
# (the consult). Run on demand after editing step 4 — never on every push.
#
# See TEST-PLAN.md (same dir) for the design and the general harness principles.
#
# Usage:
#   skill/test/synthesis-self-contained/run.sh
#
# Env:
#   ANTHROPIC_API_KEY   (required) — nested claude auth + grader API calls.
#   GEMINI_API_KEY | ASK_GEMINI_KEY_COMMAND  (required) — the live consult.
#   N                   trials per scenario      (default 5)
#   THRESHOLD           passes needed per scenario (default 4)
#   MODEL               model under test          (default claude-opus-4-8)
#   GRADER_MODEL        grader model              (default claude-sonnet-4-6)
#   SCENARIOS           space-separated subset    (default "label section framing")
#   KEEP_SCRATCH=1      keep the scratch dir even on success
#
# Exit codes: 0 = all scenarios cleared threshold; 1 = setup/precondition
# failure; 2 = at least one scenario failed.

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../../.." && pwd)"
cd "$repo_root"

N="${N:-5}"
THRESHOLD="${THRESHOLD:-4}"
MODEL="${MODEL:-claude-opus-4-8}"
GRADER_MODEL="${GRADER_MODEL:-claude-sonnet-4-6}"
read -r -a SCENARIOS <<<"${SCENARIOS:-label section framing}"

# --- Preconditions -----------------------------------------------------------

fail_setup() { echo "FAIL (setup): $*" >&2; exit 1; }

[[ -n "${ANTHROPIC_API_KEY:-}" ]] || fail_setup \
  "ANTHROPIC_API_KEY is unset. Needed for the nested claude and the graders.
      A nested claude cannot use the macOS Keychain login — an API key is the
      only automatable auth path (see TEST-PLAN.md)."

[[ -n "${GEMINI_API_KEY:-}" || -n "${ASK_GEMINI_KEY_COMMAND:-}" ]] || fail_setup \
  "no Gemini credential. Set GEMINI_API_KEY or ASK_GEMINI_KEY_COMMAND.
      The consult runs live (a stub gets detected — TEST-PLAN.md principle 6)."

command -v claude >/dev/null || fail_setup "claude CLI not on PATH."
command -v jq     >/dev/null || fail_setup "jq not on PATH."
command -v go     >/dev/null || fail_setup "go not on PATH."

for s in "${SCENARIOS[@]}"; do
  [[ -f "$script_dir/scenario-$s.md" ]] || fail_setup "missing scenario-$s.md"
done
[[ -f "$script_dir/rubric.md"  ]] || fail_setup "missing rubric.md"
[[ -f "$repo_root/skill/SKILL.md" ]] || fail_setup "missing skill/SKILL.md"

# --- Scratch (random, but UNDER the worktree — principles 1 & 2) -------------

SCRATCH="$(mktemp -d "$repo_root/.run-synthesis-eval-XXXXXX")"
cleanup() {
  if [[ -n "${KEEP_SCRATCH:-}" ]]; then
    echo "Scratch kept at: $SCRATCH" >&2
  else
    rm -rf "$SCRATCH"
  fi
}
trap cleanup EXIT

# --- Build the worktree's REAL ask-gemini, first on PATH ----------------------

echo "Building ask-gemini from this worktree..." >&2
mkdir -p "$SCRATCH/bin"
ver="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
go build -ldflags "-X main.version=$ver" -o "$SCRATCH/bin/ask-gemini" ./cmd/ask-gemini

# --- Extract the full skill body (strip YAML frontmatter) ---------------------

SKILL_FILE="$SCRATCH/_skill.txt"
awk 'BEGIN{fm=0} /^---[[:space:]]*$/{fm++; next} fm>=2{print}' \
  "$repo_root/skill/SKILL.md" > "$SKILL_FILE"
[[ -s "$SKILL_FILE" ]] || fail_setup "extracted skill body is empty — check frontmatter delimiters."

# --- Pull the two grader prompts out of rubric.md (nth fenced block) ----------

nth_block() {  # nth_block <file> <n>  -> contents of the nth ``` fenced block
  awk -v want="$2" '
    /^```/ { fence++; if (fence == 2*want-1) { inblk=1; next }
                      if (fence == 2*want)   { inblk=0 } }
    inblk { print }
  ' "$1"
}

COLD_SYS="$(nth_block "$script_dir/rubric.md" 1)"
FAITH_SYS="$(nth_block "$script_dir/rubric.md" 2)"
[[ -n "$COLD_SYS"  ]] || fail_setup "could not extract cold-read prompt from rubric.md"
[[ -n "$FAITH_SYS" ]] || fail_setup "could not extract faithfulness prompt from rubric.md"

# --- Helpers ------------------------------------------------------------------

# call_api <system> <user>  -> assistant text on stdout (empty on transport fail)
call_api() {
  local sys="$1" user="$2" body resp
  body="$(jq -n --arg m "$GRADER_MODEL" --arg s "$sys" --arg u "$user" \
    '{model:$m, max_tokens:1024, system:$s, messages:[{role:"user", content:$u}]}')"
  resp="$(curl -sS https://api.anthropic.com/v1/messages \
    -H "x-api-key: $ANTHROPIC_API_KEY" \
    -H "anthropic-version: 2023-06-01" \
    -H "content-type: application/json" \
    -d "$body")" || { echo ""; return; }
  jq -r '.content[0].text // ""' <<<"$resp" 2>/dev/null || echo ""
}

# json_field <text> <jq-filter>  -> field value, or "ERR" if not parseable
json_field() {
  jq -er "$2" <<<"$1" 2>/dev/null || echo "ERR"
}

# --- Run ----------------------------------------------------------------------

echo "N=$N  THRESHOLD=$THRESHOLD  MODEL=$MODEL  GRADER=$GRADER_MODEL" >&2
echo >&2

overall_ok=1

for scenario in "${SCENARIOS[@]}"; do
  prompt="$(cat "$script_dir/scenario-$scenario.md")"
  passes=0
  echo "=== scenario: $scenario ===" >&2

  for ((i=1; i<=N; i++)); do
    trial_dir="$SCRATCH/$scenario/trial-$i"
    mkdir -p "$trial_dir"
    rm -f "$SCRATCH"/ask-gemini-*.json   # fresh session capture per trial

    # Model under test: real claude agent loop, full skill, live Gemini consult.
    set +e
    synthesis="$(PATH="$SCRATCH/bin:$PATH" TMPDIR="$SCRATCH" \
      claude -p --bare --model "$MODEL" \
        --allowedTools "Bash(ask-gemini:*)" \
        --append-system-prompt-file "$SKILL_FILE" \
        "$prompt" < /dev/null 2>"$trial_dir/claude.err")"
    claude_status=$?
    set -e

    printf '%s' "$synthesis" > "$trial_dir/synthesis.txt"

    if [[ $claude_status -ne 0 || -z "$synthesis" ]]; then
      echo "  trial $i: FAIL (claude exited $claude_status / empty synthesis)" >&2
      cp "$trial_dir/claude.err" "$trial_dir/FAIL-claude" 2>/dev/null || true
      continue
    fi

    # Capture what was ACTUALLY sent to / received from Gemini this trial.
    shopt -s nullglob
    sess=( "$SCRATCH"/ask-gemini-*.json )
    shopt -u nullglob
    if (( ${#sess[@]} == 0 )); then
      echo "  trial $i: FAIL (no ask-gemini session file — did the model call the CLI?)" >&2
      continue
    fi
    composed="$(jq -r '.messages[]? | select(.role=="user")  | .parts[]?.text // empty' "${sess[@]}")"
    reply="$(   jq -r '.messages[]? | select(.role=="model") | .parts[]?.text // empty' "${sess[@]}")"
    printf '%s' "$composed" > "$trial_dir/composed.txt"
    printf '%s' "$reply"    > "$trial_dir/reply.txt"

    # Grade (raw API — graders are instruments, not under test).
    cold_out="$(call_api "$COLD_SYS" "$synthesis")"
    faith_user="=== PROMPT SENT TO ADVISOR ===
$composed

=== SYNTHESIS UNDER REVIEW ===
$synthesis"
    faith_out="$(call_api "$FAITH_SYS" "$faith_user")"
    printf '%s' "$cold_out"  > "$trial_dir/cold.json"
    printf '%s' "$faith_out" > "$trial_dir/faith.json"

    cold_passed="$(json_field "$cold_out" '.passed')"
    faith_ok="$(json_field "$faith_out" '.faithful')"

    if [[ "$cold_passed" == "true" && "$faith_ok" == "true" ]]; then
      passes=$((passes+1))
      echo "  trial $i: pass" >&2
    else
      echo "  trial $i: FAIL (cold=$cold_passed faithful=$faith_ok) -> $trial_dir" >&2
    fi
  done

  if (( passes >= THRESHOLD )); then
    echo "  scenario $scenario: PASS ($passes/$N)" >&2
  else
    echo "  scenario $scenario: FAIL ($passes/$N, need $THRESHOLD)" >&2
    overall_ok=0
  fi
  echo >&2
done

if (( overall_ok == 1 )); then
  echo "PASS: every scenario cleared threshold."
  exit 0
else
  echo "FAIL: at least one scenario below threshold." >&2
  KEEP_SCRATCH=1   # preserve failing-trial artifacts for inspection
  exit 2
fi
