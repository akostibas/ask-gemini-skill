package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFlagQuestions(t *testing.T) {
	qs, err := parseFlagQuestions(
		[]string{"rat=Is there a rat?", "Is it a=b?"},
		[]string{"who=Time: who is visible?:kid, adult,nobody"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var rat, q2, who map[string]any
	json.Unmarshal(qs["rat"], &rat)
	json.Unmarshal(qs["q2"], &q2)
	json.Unmarshal(qs["who"], &who)
	if rat["type"] != "noul" || rat["instructions"] != "Is there a rat?" {
		t.Errorf("rat: %v", rat)
	}
	if q2["instructions"] != "Is it a=b?" {
		t.Errorf("non-key '=' should stay in question, got %v", q2)
	}
	crit := who["criteria"].(map[string]any)
	if who["instructions"] != "Time: who is visible?" || len(crit) != 3 || crit["adult"] == nil {
		t.Errorf("who: %v", who)
	}

	for _, bad := range [][]string{{"no options"}, {"q=one option:a"}} {
		if _, err := parseFlagQuestions(nil, bad, 0); err == nil {
			t.Errorf("want error for %v", bad)
		}
	}
	if _, err := parseFlagQuestions([]string{"a=x", "a=y"}, nil, 0); err == nil {
		t.Error("want duplicate key error")
	}
}

func TestRunDecide(t *testing.T) {
	var got decisionRequest
	status := http.StatusOK
	reply := `{"answers":{"rat":{"type":"noul","noul":0.94,"confidence":0.9}},"usage":{"total_input_tokens":300,"total_tokens":300}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "secret-key" {
			t.Error("api key header missing")
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
		w.Write([]byte(reply))
	}))
	defer srv.Close()
	orig := decisionsURL
	decisionsURL = srv.URL
	defer func() { decisionsURL = orig }()
	t.Setenv("GEMINI_API_KEY", "secret-key")
	t.Setenv("ASK_GEMINI_NO_UPDATE_CHECK", "1")

	dir := t.TempDir()
	img := filepath.Join(dir, "cam.jpg")
	os.WriteFile(img, []byte("jpeg"), 0644)
	qfile := filepath.Join(dir, "q.json")
	os.WriteFile(qfile, []byte(`{"grade":{"type":"score","instructions":"How dirty?","criteria":["clean","dirty"]}}`), 0644)

	var out, errb bytes.Buffer
	code := runDecide([]string{"--file", img, "--resolution", "low", "--questions", qfile, "--yesno", "rat=Rat?", "context"}, nil, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if got.Model != defaultDecisionModel || len(got.Questions) != 2 || got.Questions["grade"] == nil {
		t.Errorf("request: %+v", got)
	}
	if len(got.Input) != 2 || got.Input[0].Type != "image" || got.Input[0].Resolution != "low" || got.Input[1].Text != "context" {
		t.Errorf("input: %+v", got.Input)
	}
	var answers map[string]map[string]any
	if err := json.Unmarshal(out.Bytes(), &answers); err != nil || answers["rat"]["noul"] != 0.94 {
		t.Errorf("stdout should be the answers JSON, got %q", out.String())
	}
	if !strings.Contains(errb.String(), "$0.000030") {
		t.Errorf("want cost line, got %q", errb.String())
	}

	// Flag key colliding with the file's key is refused before any call.
	if code := runDecide([]string{"--questions", qfile, "--yesno", "grade=x", "ctx"}, nil, &out, &errb); code != 1 {
		t.Errorf("want exit 1 on key collision, got %d", code)
	}

	// Blocked content: clear message, non-zero exit, no key leak.
	status = http.StatusBadRequest
	reply = `{"error":{"code":"safety","message":"blocked"}}`
	errb.Reset()
	if code := runDecide([]string{"--yesno", "x", "ctx"}, nil, &out, &errb); code != 1 {
		t.Errorf("want exit 1, got %d", code)
	}
	if !strings.Contains(errb.String(), "safety: blocked") || strings.Contains(errb.String(), "secret-key") {
		t.Errorf("error output: %q", errb.String())
	}
}
