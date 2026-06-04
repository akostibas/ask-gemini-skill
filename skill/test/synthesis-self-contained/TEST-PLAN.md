# Test plan: synthesis step is self-contained

Behavioral eval for the ask-gemini skill's **step 4 ("Relay the response")**.
Verifies that an assistant following the skill produces a synthesis the user can
read *without* having seen the prompt that was sent to Gemini (issue #3).

This is an **eval, not a unit test** — the model under test is nondeterministic,
so we run N trials and require a pass rate, not a single green/red.

## General test-harness principles

These are reusable rules for any Claude skill / light-app test harness, not
just this one. Apply them by default.

1. **Never lock a global/shared resource.** No hard-coded ports (`:8080`), no
   fixed tmp paths, no fixed session names. Allocate randomly — `mktemp -d` for
   scratch dirs, a random suffix for session names, an ephemeral port (bind `:0`
   and read back the assigned port) for anything that listens. If a tool
   *cannot* be made to randomize, fall back to running it in a container so the
   isolation is enforced at the boundary instead.
2. **Run inside a worktree; touch only relative paths.** The harness must write
   every artifact (scratch files, output dumps, logs) under the worktree using
   relative paths, so a run can't mutate the main checkout or another worktree.
   No absolute paths into the project. Read inputs by relative path too
   (e.g. `../../SKILL.md`).
3. **Freeze inputs where you can — but not at a layer the system under test can
   detect.** Pinning inputs makes runs comparable, but see principle 6: a stubbed
   dependency that the agent can probe is worse than no stub. When freezing would
   require a detectable fake, prefer a real call and absorb the drift (principle
   4). *For this eval we deliberately do NOT freeze the consult* — we run live
   Gemini calls and let trials absorb the variance.
4. **Measure nondeterminism, don't fight it.** Where the thing under test is a
   model, run N trials and threshold the pass rate rather than expecting a
   single deterministic result.
5. **Integration tests drive from the user's angle.** Exercise the real entry
   point a user would (here: the actual `claude` CLI agent loop running the
   skill end-to-end), even if it inherits messiness and some unreproducibility.
   Minimize that (pin the model, isolate config) but don't treat residual
   non-determinism as a blocker — fidelity to the real path beats a clean room
   that tests something users never run.
6. **An agentic system under test will detect fake fixtures — don't stub at a
   layer it can inspect.** Verified live (2026-06-04): a `claude -p` agent driving
   the skill ran an unprompted sanity probe (a separate "reply PONG" consult),
   noticed the stub returned identical canned text, *and read the stub's source
   off disk* (read-only tools aren't blocked by a scoped `--allowedTools
   "Bash(...)"`), then refused to relay the reply. So a surface-faithful CLI stub
   is useless here. Either mock below the inspectable layer (the upstream HTTP),
   or — as chosen here — make a real call.

## What we're testing

The instruction under test lives in `skill/SKILL.md`, step 4. The failure mode
(issue #3): Gemini's reply references prompt-internal structure (option letters
A/B/C/D, section names, "the framing question"), and the assistant parrots that
structure into its synthesis — leaving the user reading labels they never saw
defined.

What the user *has* seen: their own original query (they typed it). What they
have **not** seen: the prompt the assistant composed for Gemini, or Gemini's
full reply. "Self-contained" therefore means **readable by someone holding only
the original user query**.

## Strategy: live consults, leak induced via crafted scenario prompts

We can't freeze the Gemini reply with a stub (principle 6 — the agent detects
it), so the consult runs **live** against the real Gemini API. We don't *need* a
frozen reply to test self-containedness; we need the reply to reliably reference
**prompt-internal structure**. We get that by controlling the *input*: each
scenario is a user request that lays out **labeled options the model will carry
into its Gemini prompt**, so Gemini's live reply naturally refers back to them
(A/B/C/D, "your framing question", etc.). The leak is induced by framing, not
faked.

**Three scenarios, not one** — a single scenario invites overfitting (whoever
edits step 4 just strips "A/B/C/D" and the test passes while real consults leak
*other* structure). Each scenario induces a different leak style:

- `scenario-label.md` — **label leak**: option letters/numbers (A/B/C, Option 1/2).
- `scenario-section.md` — **section leak**: a multi-section request the reply
  refers back to ("as in the first block", "the code snippet above").
- `scenario-framing.md` — **framing leak**: a request whose reply leans on meta
  framing ("to answer your framing question", "per the guidelines you gave").

Because the reply is live, the **composed prompt and the actual reply are
captured per-run** from the session file the real `ask-gemini` writes (we point
`TMPDIR` at the scratch dir so only this run's `ask-gemini-*.json` lands there),
and handed to the faithfulness grader. So the grader judges against what was
*actually* sent, not a frozen guess.

## Artifacts (this directory)

- `TEST-PLAN.md` — this file.
- `scenario-label.md`, `scenario-section.md`, `scenario-framing.md` — the user
  requests that induce each leak style.
- `rubric.md` — the adversarial grader prompts (below).
- `run.sh` — the automated loop.

## Execution: real `claude` CLI agent loop (driven full-skill)

Per principle 5, the model under test runs through the **real Claude Code agent
loop**, not a raw API call — the same path a user hits. The model drives the
**whole skill end-to-end**: picks a session, formulates the question, "calls"
`ask-gemini`, then synthesizes. We only care about the synthesis (step 4), but
running steps 1–3 for real is the point — step 4 must hold up regardless of how
the model framed things. The N-trials threshold absorbs the steps 1–3 variance.

Invocation shape (run from inside the worktree, all paths relative). **All flags
below were verified live on 2026-06-04:**

```
PATH="$SCRATCH/bin:$PATH" \                  # the worktree-built REAL ask-gemini, first on PATH
TMPDIR="$SCRATCH" \                          # session JSONs land here for capture (principle 1/2)
ANTHROPIC_API_KEY=... \                      # nested claude auth — env-key path, NOT keychain (see below)
ASK_GEMINI_KEY_COMMAND=... \                 # the real ask-gemini's Gemini credential
claude -p \
  --bare \                                   # no ambient skill/CLAUDE.md bleed
  --model claude-opus-4-8 \                  # pin the model for a clean regression signal
  --allowedTools "Bash(ask-gemini:*)" \      # let it run the real CLI; no permission bypass
  --append-system-prompt-file ./_skill.txt \ # the FULL skill under test, extracted live from ../../SKILL.md
  < /dev/null \                              # redirect stdin or claude stalls 3s waiting on it
  "<realistic request — must NOT start with '/' or it's parsed as a slash command>"
```

- **Drive the full skill, live.** The full `skill/SKILL.md` is injected (not just
  step 4) so the model genuinely runs steps 1–4: formulates the consult, calls
  the **real** `ask-gemini` (live Gemini call), then synthesizes. We only score
  step 4, but running 1–3 for real is the point.
- **`--bare`** disables auto-discovery of the *installed* skill, hooks, plugins,
  MCP, auto-memory, and CLAUDE.md — so the only skill wording in context is the
  worktree copy we inject. (Needed because **personal skills override
  project-local**, so a worktree `.claude/skills/` copy would NOT shadow the
  installed one — verified against the Claude Code skills docs.)
- **Auth is via `ANTHROPIC_API_KEY`, not the Keychain.** Verified: a nested
  `claude` spawned from a non-interactive shell **cannot** use the macOS Keychain
  OAuth login ("Not logged in"); it *does* honor `ANTHROPIC_API_KEY`. So this
  harness is automatable (CI/agent) only with an API key — never via interactive
  login.
- **Scoped allowlist, not `bypassPermissions`.** `--allowedTools
  "Bash(ask-gemini:*)"` lets the agent run only the CLI. Note read-only tools are
  still available (that's how a stub gets caught — principle 6); with live calls
  that's harmless.

**Fidelity caveat:** `--bare` strips ambient `CLAUDE.md` and other skills, so
this is "real agent loop + real base system prompt, minus user-specific bleed,"
not 100% production fidelity. Still strictly more realistic than a raw-API call.
Residual non-determinism (live Gemini + live agent loop) is accepted per
principles 4–5, not engineered away.

**Graders still run via raw API.** The two graders (below) are *measurement
instruments*, not the thing under test, so they get a clean room — raw Anthropic
API, controlled context — to keep grading consistent.

## Dual-grader architecture

Grading splits into two roles with **asymmetric context**; both must pass.

- **Faithfulness grader** — *sees the composed prompt + the synthesis.* Checks
  the synthesis didn't misrepresent the real options or recommend something that
  wasn't actually preferred. Catches hallucination / distortion.
- **Cold-read grader** — *sees the synthesis ONLY*, zero outside context.
  Performs an extraction task; if it can't, the synthesis isn't self-contained.

Because Claude-grading-Claude shares model-family priors (the cold-read grader
may "fill in the blanks" a human couldn't), the cold-read grader's prompt is
**adversarial / extraction-based**, not "is this clear?". Optionally run the
cold-read grader on a *different* model family (e.g. Gemini) to break the shared
prior — at the cost of an extra dependency.

## Rubric (adversarial, negative-constraint)

Easier for an LLM to find a violation than to judge quality, so graders hunt
violations and emit JSON.

**Cold-read grader** (synthesis only):
> You are an engineer who just joined; you have zero prior context. Read the
> synthesis. Flag violations:
> 1. Uses a letter/number label ("Option A", "Plan 2") for a choice without
>    explaining within this text what that choice is.
> 2. Refers to prompt elements ("the system instructions", "the first section",
>    "your framing question").
> 3. To implement the recommendation right now, would you have to guess what it
>    is because it's named only by a label?
> Output `{"passed": bool, "violations": [...]}`.

**Faithfulness grader** (composed prompt + synthesis):
> Compare synthesis to the composed prompt. Does it misrepresent the trade-offs
> or recommend a non-preferred option? Output `{"faithful": bool, "reason": ...}`.

## Automated loop (`run.sh` / runner)

No human-in-the-loop. Honors the harness principles above.

```
0. Run inside a worktree; cd to this dir; all paths relative.
1. SCRATCH=$(mktemp -d)                  # random scratch, cleaned on exit (trap)
   extract full skill from ../../SKILL.md -> $SCRATCH/_skill.txt
   build the worktree's real ask-gemini -> $SCRATCH/bin/ask-gemini (prepend PATH)
2. For each scenario in {label, section, framing}:
     PROMPT = scenario file's user request (induces the leak style)
     For i in 1..N:
       a. clear $SCRATCH/ask-gemini-*.json   # fresh session capture per trial
          SYNTHESIS = claude -p --bare --model claude-opus-4-8 \
                        --allowedTools "Bash(ask-gemini:*)" \
                        --append-system-prompt-file $SCRATCH/_skill.txt \
                        < /dev/null "$PROMPT"
          # model drives the full skill; ask-gemini makes a LIVE Gemini call;
          # model synthesizes. TMPDIR=$SCRATCH so the session JSON is captured.
       b. COMPOSED, REPLY = parse the captured $SCRATCH/ask-gemini-*.json
          COLD  = api(cold_read_grader,    input=SYNTHESIS)            # raw API
          FAITH = api(faithfulness_grader, input=COMPOSED + SYNTHESIS) # raw API
       c. trial passes iff COLD.passed AND FAITH.faithful
3. Per scenario: passes >= THRESHOLD ? ok : fail (dump failing trials to SCRATCH).
4. Exit 0 only if every scenario clears threshold.
```

Defaults: `N=5`, `THRESHOLD=4` per scenario. Model under test via real `claude`
CLI making live Gemini consults; graders via raw API.

## Cost & when to run

Each run is `3 scenarios × N × (1 full-skill claude run + 1 live Gemini consult
+ 2 grades)`. At N=5 that's 15 agent runs, ~15+ live Gemini calls, and 30 grade
calls — more than the frozen design, because we chose realism over a free clean
room. Run **on demand** after editing step 4, never on every push.

## Resolved during review (was "open questions")

- One scenario is too easy → **three leak-style scenarios**.
- Grader should/shouldn't see the prompt → **both**: faithfulness sees it
  (captured live from the session JSON), cold-read is blind.
- Raw API vacuum vs. real loop → **drive the real `claude` CLI full-skill**
  (principle 5). Graders stay on raw API (instruments, not under test).
- Freeze the consult vs. live → **live** (principle 6): a stub gets detected by
  the agent, so we make real Gemini calls and absorb drift with trials.
- Model-under-test environment → `--bare` (personal skills override
  project-local) + `ANTHROPIC_API_KEY` auth (Keychain unusable when nested) +
  scoped `--allowedTools` (no permission bypass).

## Verified live (2026-06-04)

A throwaway end-to-end run (real `claude -p`, valid key, full skill injected)
confirmed: auth via `ANTHROPIC_API_KEY` works under `--bare`; the agent drives
the whole skill (formulated a strong consult, asked Gemini for clarifying
questions, did a follow-up turn); the flag set composes. It also caught a stub
red-handed (principle 6) — which is *why* this plan now uses live consults.

A full `run.sh` smoke run (`SCENARIOS=label N=1`) then went green end-to-end:
the agent restated every option (A–D) in its synthesis, and both graders passed
on their own merits (cold-read found no dangling labels; faithfulness confirmed
no distortion). So the harness exercises the real failure mode, not a trivial
pass.

**Bug surfaced by driving the real agent loop (principle 5 paying off):** the
first smoke attempt *hung for ~20 min*. Root cause was in `ask-gemini` itself,
not the harness — `resolvePrompt` read stdin unconditionally whenever stdin was
"not a TTY," and when the nested agent invoked the CLI with a positional prompt
it inherited the agent shell's stdin (an open pipe with no data and no EOF), so
`io.ReadAll` blocked forever. A clean-room raw-API test would never have caught
this; it only appears when a real agent shells out to the tool. Fixed by
time-bounding the *optional* stdin read when a positional prompt is present
(`stdinAppendTimeout`), with a regression test (`TestResolvePromptDoesNotHangOnIdleStdin`).

Open follow-ups: tune N/THRESHOLD against observed variance; decide whether the
cold-read grader should run on a different model family (Gemini) to break
Claude-on-Claude priors.
