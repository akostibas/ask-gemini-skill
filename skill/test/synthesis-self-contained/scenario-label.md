I'm adding a self-update capability to a Go CLI tool that's distributed as a
single binary via `go install`. I want a second opinion on the approach before I
build it. Here are the four options I'm weighing:

A. A `--version` flag only — the user checks manually and re-runs `go install`
   themselves. Zero network calls at runtime, but no nudge to upgrade.
B. A cached background check — on each invocation, if the cache is older than
   24h, fire an async HEAD request to the releases API and print a banner if a
   newer tag exists. Fast, but adds a network dependency and a cache file.
C. An opt-in env var (`MYTOOL_CHECK_UPDATES=1`) that gates the background check
   from B. Privacy-friendly default-off, but most users never discover it.
D. A hybrid: `--version` does a live check inline (blocking, but only when the
   user explicitly asks), and there's no background behavior at all.

Two specific things I want your take on:
1. For a single-binary `go install` tool, is a runtime update check even worth
   the complexity, or is the `--version` manual path the honest default?
2. If we do check, what's the least surprising failure behavior when the
   releases API is unreachable (offline, rate-limited, GitHub down)?

Please ask me any clarifying questions you need before giving a recommendation.
