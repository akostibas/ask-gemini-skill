package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withMockReleases points releasesAPIURL at a local server returning tag and
// redirects the cache to a fresh temp file. The returned *int counts hits.
func withMockReleases(t *testing.T, tag string) *int {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		json.NewEncoder(w).Encode(map[string]string{"tag_name": tag})
	}))
	origURL := releasesAPIURL
	origCache := updateCachePath
	releasesAPIURL = srv.URL
	updateCachePath = filepath.Join(t.TempDir(), "update.json")
	t.Cleanup(func() {
		releasesAPIURL = origURL
		updateCachePath = origCache
		srv.Close()
	})
	return &hits
}

func TestMaybeNotifyUpdateNewer(t *testing.T) {
	withMockReleases(t, "v0.3.0")
	var buf bytes.Buffer
	maybeNotifyUpdate("v0.2.0", &buf)
	if !strings.Contains(buf.String(), "v0.2.0 → v0.3.0") {
		t.Errorf("expected update nudge, got %q", buf.String())
	}
}

func TestMaybeNotifyUpdateEqual(t *testing.T) {
	withMockReleases(t, "v0.2.0")
	var buf bytes.Buffer
	maybeNotifyUpdate("v0.2.0", &buf)
	if buf.Len() != 0 {
		t.Errorf("expected no output when up to date, got %q", buf.String())
	}
}

func TestMaybeNotifyUpdateOlderRelease(t *testing.T) {
	withMockReleases(t, "v0.1.0")
	var buf bytes.Buffer
	maybeNotifyUpdate("v0.2.0", &buf)
	if buf.Len() != 0 {
		t.Errorf("expected no output when ahead of release, got %q", buf.String())
	}
}

func TestMaybeNotifyUpdateSkipsDevAndPseudo(t *testing.T) {
	for _, v := range []string{"dev", "v0.0.0-20240101000000-abcdef123456", "v0.2.0-3-gdeadbee", "v0.2.0-dirty"} {
		hits := withMockReleases(t, "v9.9.9")
		var buf bytes.Buffer
		maybeNotifyUpdate(v, &buf)
		if buf.Len() != 0 {
			t.Errorf("version %q: expected no nudge, got %q", v, buf.String())
		}
		if *hits != 0 {
			t.Errorf("version %q: expected no network call, got %d", v, *hits)
		}
	}
}

func TestMaybeNotifyUpdateSuppressedByEnv(t *testing.T) {
	hits := withMockReleases(t, "v9.9.9")
	t.Setenv(suppressUpdateEnv, "1")
	var buf bytes.Buffer
	maybeNotifyUpdate("v0.2.0", &buf)
	if buf.Len() != 0 {
		t.Errorf("expected no output when suppressed, got %q", buf.String())
	}
	if *hits != 0 {
		t.Errorf("expected no network call when suppressed, got %d", *hits)
	}
}

func TestFreshCacheAvoidsNetwork(t *testing.T) {
	hits := withMockReleases(t, "v9.9.9")
	// Seed a fresh cache; the network must not be touched.
	writeUpdateCache(updateCache{CheckedAt: time.Now(), LatestTag: "v0.5.0"})
	var buf bytes.Buffer
	maybeNotifyUpdate("v0.2.0", &buf)
	if *hits != 0 {
		t.Errorf("expected no network call with fresh cache, got %d", *hits)
	}
	if !strings.Contains(buf.String(), "v0.2.0 → v0.5.0") {
		t.Errorf("expected nudge from cached tag, got %q", buf.String())
	}
}

func TestStaleCacheTriggersFetchAndRefresh(t *testing.T) {
	hits := withMockReleases(t, "v0.4.0")
	writeUpdateCache(updateCache{CheckedAt: time.Now().Add(-2 * updateCheckTTL), LatestTag: "v0.3.0"})
	var buf bytes.Buffer
	maybeNotifyUpdate("v0.2.0", &buf)
	if *hits != 1 {
		t.Errorf("expected one network call with stale cache, got %d", *hits)
	}
	if !strings.Contains(buf.String(), "v0.2.0 → v0.4.0") {
		t.Errorf("expected nudge from freshly fetched tag, got %q", buf.String())
	}
	c, ok := readUpdateCache()
	if !ok || c.LatestTag != "v0.4.0" {
		t.Errorf("expected cache refreshed to v0.4.0, got %+v (ok=%v)", c, ok)
	}
}

func TestUpdateCheckSilentOnServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden) // simulate rate-limited
	}))
	defer srv.Close()
	origURL, origCache := releasesAPIURL, updateCachePath
	releasesAPIURL = srv.URL
	updateCachePath = filepath.Join(t.TempDir(), "update.json")
	defer func() { releasesAPIURL, updateCachePath = origURL, origCache }()

	var buf bytes.Buffer
	maybeNotifyUpdate("v0.2.0", &buf)
	if buf.Len() != 0 {
		t.Errorf("expected silence on server error, got %q", buf.String())
	}
}

func TestParseSemver(t *testing.T) {
	cases := []struct {
		in              string
		maj, min, patch int
		ok              bool
	}{
		{"v1.2.3", 1, 2, 3, true},
		{"v0.0.0", 0, 0, 0, true},
		{"v10.20.30", 10, 20, 30, true},
		{"1.2.3", 0, 0, 0, false},
		{"v1.2", 0, 0, 0, false},
		{"v1.2.3-rc1", 0, 0, 0, false},
		{"dev", 0, 0, 0, false},
	}
	for _, c := range cases {
		maj, min, patch, ok := parseSemver(c.in)
		if ok != c.ok || maj != c.maj || min != c.min || patch != c.patch {
			t.Errorf("parseSemver(%q) = (%d,%d,%d,%v), want (%d,%d,%d,%v)",
				c.in, maj, min, patch, ok, c.maj, c.min, c.patch, c.ok)
		}
	}
}

func TestCompareSemver(t *testing.T) {
	if compareSemver(1, 0, 0, 0, 9, 9) <= 0 {
		t.Error("1.0.0 should be > 0.9.9")
	}
	if compareSemver(0, 2, 0, 0, 2, 0) != 0 {
		t.Error("equal versions should compare 0")
	}
	if compareSemver(0, 2, 1, 0, 2, 3) >= 0 {
		t.Error("0.2.1 should be < 0.2.3")
	}
}
