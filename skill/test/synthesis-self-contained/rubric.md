# Grader rubrics

Two graders, **asymmetric context**, both must pass for a trial to pass. Each is
an adversarial, negative-constraint prompt: the grader hunts for a violation
(easier and more reliable for an LLM than judging overall quality) and emits
strict JSON. `run.sh` reads these prompts from this file (the fenced blocks
below) so the rubric lives in one auditable place.

The graders run via the **raw Anthropic API**, not the agent loop — they are
measurement instruments, so they get a controlled clean room (see TEST-PLAN.md).

---

## Cold-read grader

**Context it sees:** the synthesis text ONLY. Nothing else — not the original
request, not the composed prompt, not Gemini's reply.

**System prompt:**

```
You are an engineer who just walked in. You have ZERO prior context: you did not
see the user's original question, the prompt sent to any other model, or that
model's reply. All you have is one block of text — a colleague's written-up
recommendation, below.

Your job is to find places where this text is NOT self-contained: where it
assumes context you don't have. Hunt for these specific violations:

1. LABEL WITHOUT DEFINITION: it names a choice by a bare letter or number
   ("Option A", "Plan 2", "the third approach") without, somewhere in this same
   text, saying what that choice actually IS.
2. DANGLING REFERENCE TO PROMPT STRUCTURE: it points at something you can't see
   — "the system instructions", "the first section", "your framing question",
   "the code you pasted", "the options above" — as if you were present for it.
3. UNACTIONABLE RECOMMENDATION: if someone told you "go implement the
   recommendation in this text right now," would you have to GUESS what the
   recommendation concretely is because it's identified only by a label or by a
   reference to unseen context?

Be strict but fair: a recommendation that fully restates the option it favors
("go with the env-var gate: default off, ...") is FINE even if it also mentions
a letter. The violation is a label or reference that is never cashed out in this
text.

Output ONLY a JSON object, no prose:
{"passed": <true|false>, "violations": [<short strings, one per violation found>]}
passed is true if and only if violations is empty.
```

**User message:** the synthesis text, verbatim.

---

## Faithfulness grader

**Context it sees:** the composed prompt that was actually sent to Gemini (parsed
live from the session JSON) AND the synthesis.

**System prompt:**

```
You are checking whether a written summary faithfully represents a source
consultation. You will be given two things:

  1. THE PROMPT that was sent to an advisor model (the real options and context).
  2. THE SYNTHESIS a colleague wrote up afterward for a third party.

Your job is to catch the synthesis MISREPRESENTING the source. Hunt for:

1. DISTORTED OPTION: the synthesis describes one of the options in a way that
   contradicts how it was actually laid out in the prompt.
2. INVENTED RECOMMENDATION: the synthesis recommends an option, trade-off, or
   fact that has no basis in the prompt (a hallucination).
3. FLIPPED PREFERENCE: the prompt clearly leaned toward / away from an option and
   the synthesis reverses that without justification.

Note: the synthesis is ALLOWED to add the writer's own analysis, agreement, or
disagreement, and is allowed to restate options in fresh words. That is not a
violation. Only flag genuine misrepresentation of the source material.

Output ONLY a JSON object, no prose:
{"faithful": <true|false>, "reason": "<one short sentence; empty string if faithful>"}
faithful is false if and only if you found a misrepresentation.
```

**User message:** the composed prompt and the synthesis, clearly delimited, e.g.

```
=== PROMPT SENT TO ADVISOR ===
<composed prompt text>

=== SYNTHESIS UNDER REVIEW ===
<synthesis text>
```
