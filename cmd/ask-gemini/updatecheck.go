package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// updateCheckTTL bounds how often we hit GitHub. Between checks the last
// result is served from a small cache file in the system temp dir, so a busy
// consult session makes at most one network call per day.
const updateCheckTTL = 24 * time.Hour

// updateHTTPTimeout keeps a slow or unreachable GitHub from adding latency to a
// consult. On timeout the check is a silent no-op.
const updateHTTPTimeout = 2 * time.Second

// suppressUpdateEnv, when set to any non-empty value, disables the check.
const suppressUpdateEnv = "ASK_GEMINI_NO_UPDATE_CHECK"

// releasesAPIURL is a var so tests can point it at a local httptest server.
var releasesAPIURL = "https://api.github.com/repos/akostibas/ask-gemini-skill/releases/latest"

// updateCachePath is a var so tests can redirect the cache to a temp file.
var updateCachePath = filepath.Join(os.TempDir(), "ask-gemini-update.json")

var semverRE = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

type updateCache struct {
	CheckedAt time.Time `json:"checked_at"`
	LatestTag string    `json:"latest_tag"`
}

// maybeNotifyUpdate prints a one-line stderr hint when a newer release exists.
// Every failure mode — suppressed, un-comparable version, offline, rate-limited,
// malformed response — is a silent no-op: an update checker that prints errors
// is worse than no checker.
func maybeNotifyUpdate(current string, stderr io.Writer) {
	if os.Getenv(suppressUpdateEnv) != "" {
		return
	}
	// Only compare clean release versions. A "dev" build or a `go install`
	// pseudo-version can't be ordered against vX.Y.Z, so skip silently.
	curMaj, curMin, curPatch, ok := parseSemver(current)
	if !ok {
		return
	}

	latest := latestTag()
	if latest == "" {
		return
	}
	latMaj, latMin, latPatch, ok := parseSemver(latest)
	if !ok {
		return
	}

	if compareSemver(latMaj, latMin, latPatch, curMaj, curMin, curPatch) > 0 {
		fmt.Fprintf(stderr, "ask-gemini: update available %s → %s. Run this skill's update.sh to upgrade (suppress with %s=1).\n",
			current, latest, suppressUpdateEnv)
	}
}

// latestTag returns the newest release tag, served from the cache when fresh
// and otherwise fetched from GitHub (and cached). Returns "" on any failure.
func latestTag() string {
	if c, ok := readUpdateCache(); ok && time.Since(c.CheckedAt) < updateCheckTTL {
		return c.LatestTag
	}
	tag := fetchLatestTag()
	if tag == "" {
		return ""
	}
	writeUpdateCache(updateCache{CheckedAt: time.Now(), LatestTag: tag})
	return tag
}

func fetchLatestTag() string {
	client := &http.Client{Timeout: updateHTTPTimeout}
	req, err := http.NewRequest(http.MethodGet, releasesAPIURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ""
	}
	return payload.TagName
}

func readUpdateCache() (updateCache, bool) {
	data, err := os.ReadFile(updateCachePath)
	if err != nil {
		return updateCache{}, false
	}
	var c updateCache
	if err := json.Unmarshal(data, &c); err != nil {
		return updateCache{}, false
	}
	return c, true
}

func writeUpdateCache(c updateCache) {
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	// Best-effort: a failure to cache just means we check again next time.
	_ = os.WriteFile(updateCachePath, data, 0o644)
}

func parseSemver(v string) (maj, min, patch int, ok bool) {
	m := semverRE.FindStringSubmatch(v)
	if m == nil {
		return 0, 0, 0, false
	}
	maj, _ = strconv.Atoi(m[1])
	min, _ = strconv.Atoi(m[2])
	patch, _ = strconv.Atoi(m[3])
	return maj, min, patch, true
}

func compareSemver(aMaj, aMin, aPatch, bMaj, bMin, bPatch int) int {
	switch {
	case aMaj != bMaj:
		return sign(aMaj - bMaj)
	case aMin != bMin:
		return sign(aMin - bMin)
	default:
		return sign(aPatch - bPatch)
	}
}

func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	default:
		return 0
	}
}
