# ask-gemini: product intent

## Purpose

One small command that puts Google's Gemini models within reach of an agent
(Claude Code) or an automation (Home Assistant, shell scripts) without either
one needing to know the Gemini API. It serves three kinds of need:

- **A second opinion.** A different model with a different way of thinking,
  consulted mid-problem, that remembers the consult across follow-ups.
- **Making media.** Images and short video clips from a prompt.
- **Quick decisions.** Closed questions (yes/no, pick one, grade on a scale)
  about text or media, answered fast and cheaply with probabilities that an
  automation can act on.

## What "better" means

- **Less effort from the caller.** An agent or a one-line automation can
  invoke it correctly on the first try. Output is easy to consume: prose for
  people, machine-readable output that is hard to misread for automations.
- **Honest about uncertainty and cost.** Every call says roughly what it cost.
  Answers that carry a confidence make clear what that confidence is about.
- **Safe to leave running.** It never hangs waiting on input that isn't coming,
  never leaks the API key, and fails with a clear message and a non-zero exit.
- **Cheap to keep current.** New Gemini models and endpoints can be adopted
  without reshaping how callers use it.

## Floors

- The API key never appears in output or errors.
- A failed call never looks like a successful one.
- Live (billable) calls happen only when the caller asks for them. Tests never
  make them.

## Non-goals

- A general Gemini SDK or full API coverage. Features are added when a real
  caller needs them.
- A chat UI or long-running service. It is a command you run and that exits.
- Native integrations for specific platforms (e.g. a Home Assistant
  component). Those can call the command; they live elsewhere.
