#!/usr/bin/env bash
#
# release.sh — cut a tagged release of ask-gemini.
#
# Releasing is a multi-step workflow (tests green -> tag -> push -> GitHub
# release). This script makes it mechanical and refuses to proceed when a
# precondition isn't met, so a release can't go out from a dirty tree, the
# wrong branch, or with failing tests.
#
# Usage:
#   bin/release.sh <version>     # explicit, e.g. v0.2.0
#   bin/release.sh patch         # bump x.y.Z from the latest tag
#   bin/release.sh minor         # bump x.Y.0
#   bin/release.sh major         # bump X.0.0
#
# By default a release runs the live smoke test (bin/smoke-test.sh) after the
# unit tests, so a bad model ID or request shape can't ship. It makes one
# billable API call and needs GEMINI_API_KEY or ASK_GEMINI_KEY_COMMAND set.
# Pass --no-smoke to skip it (e.g. no credentials handy, or a docs-only bump).
#
# See ../CLAUDE.md for what counts as a patch/minor/major change.
#
# Exit codes: 0 = released, 1 = precondition failure, 2 = tests/smoke failed.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

release_branch="main"

# --- Parse arguments (one positional version + optional flags) ---

run_smoke=1
version_arg=""
for arg in "$@"; do
  case "$arg" in
    --no-smoke) run_smoke=0 ;;
    -*)
      echo "FAIL: unknown flag '$arg'. Supported: --no-smoke." >&2
      exit 1
      ;;
    *)
      if [[ -n "$version_arg" ]]; then
        echo "FAIL: expected a single version (v0.2.0, or patch/minor/major); got '$version_arg' and '$arg'." >&2
        exit 1
      fi
      version_arg="$arg"
      ;;
  esac
done

# --- Report current state before doing anything ---

current_branch="$(git rev-parse --abbrev-ref HEAD)"
latest_tag="$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")"
echo "Branch:     $current_branch"
echo "Latest tag: $latest_tag"

if [[ -z "$version_arg" ]]; then
  echo "FAIL: expected a version argument (a version like v0.2.0, or patch/minor/major)." >&2
  exit 1
fi

# --- Resolve the requested version ---

bump() {
  local part="$1" cur="${latest_tag#v}"
  local major minor patch
  IFS='.' read -r major minor patch <<<"$cur"
  case "$part" in
    major) echo "v$((major + 1)).0.0" ;;
    minor) echo "v${major}.$((minor + 1)).0" ;;
    patch) echo "v${major}.${minor}.$((patch + 1))" ;;
  esac
}

case "$version_arg" in
  major | minor | patch) version="$(bump "$version_arg")" ;;
  *) version="$version_arg" ;;
esac

if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "FAIL: version '$version' is not vMAJOR.MINOR.PATCH (e.g. v0.2.0)." >&2
  exit 1
fi
echo "Releasing:  $version"

# --- Preconditions ---

if [[ "$current_branch" != "$release_branch" ]]; then
  echo "FAIL: releases are cut from '$release_branch', but you're on '$current_branch'." >&2
  exit 1
fi

if [[ -n "$(git status --porcelain)" ]]; then
  echo "FAIL: working tree is dirty. Commit or stash before releasing." >&2
  git status --short >&2
  exit 1
fi

echo "Fetching from origin..."
git fetch --quiet origin "$release_branch"
if [[ "$(git rev-parse HEAD)" != "$(git rev-parse "origin/$release_branch")" ]]; then
  echo "FAIL: local '$release_branch' is not in sync with 'origin/$release_branch'." >&2
  echo "      Pull/push so the tag points at the published commit." >&2
  exit 1
fi

if git rev-parse "$version" >/dev/null 2>&1; then
  echo "FAIL: tag '$version' already exists." >&2
  exit 1
fi

# --- Verify the build is releasable ---

echo "Running go vet..."
go vet ./...
echo "Running go test..."
if ! go test ./...; then
  echo "FAIL: tests failed; not releasing." >&2
  exit 2
fi

if [[ "$run_smoke" -eq 1 ]]; then
  echo "Running live smoke test (bin/smoke-test.sh)..."
  if ! bin/smoke-test.sh; then
    echo "FAIL: smoke test failed; not releasing." >&2
    echo "      Fix the issue, or pass --no-smoke to skip (e.g. no credentials)." >&2
    exit 2
  fi
else
  echo "Skipping smoke test (--no-smoke)."
fi

# --- Tag and push ---

echo "Tagging $version..."
git tag -a "$version" -m "$version"
git push origin "$version"

# --- Optional GitHub release ---

if command -v gh >/dev/null 2>&1; then
  echo "Creating GitHub release..."
  gh release create "$version" --title "$version" --generate-notes
else
  echo "gh not found; skipped GitHub release. Tag '$version' is pushed."
fi

echo "PASS: released $version."
