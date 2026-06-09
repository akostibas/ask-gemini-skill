package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Unit tests: session paths ---

func TestSessionPath(t *testing.T) {
	tests := []struct {
		name     string
		session  string
		wantBase string
	}{
		{"empty falls back to default", "", sessionFilePrefix + defaultSessionName + ".json"},
		{"named session", "review", sessionFilePrefix + "review.json"},
		{"slash is sanitized", "foo/bar", sessionFilePrefix + "foo_bar.json"},
		{"backslash is sanitized", `foo\bar`, sessionFilePrefix + "foo_bar.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sessionPath(tt.session)
			if filepath.Base(got) != tt.wantBase {
				t.Errorf("sessionPath(%q) = %q, want base %q", tt.session, got, tt.wantBase)
			}
			if wantDir := filepath.Clean(os.TempDir()); filepath.Dir(got) != wantDir {
				t.Errorf("sessionPath(%q) dir = %q, want %q", tt.session, filepath.Dir(got), wantDir)
			}
		})
	}
}

// --- Unit tests: conversation persistence ---

func TestSaveLoadConversationRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conv.json")
	want := &Conversation{
		Model:     "gemini-3.5-flash",
		StartedAt: "2026-05-19T00:00:00Z",
		Messages: []Content{
			{Role: "user", Parts: []Part{{Text: "hello"}}},
			{Role: "model", Parts: []Part{{Text: "hi"}}},
		},
	}
	if err := saveConversation(path, want); err != nil {
		t.Fatalf("saveConversation: %v", err)
	}
	got, err := loadConversation(path)
	if err != nil {
		t.Fatalf("loadConversation: %v", err)
	}
	if got == nil {
		t.Fatal("loadConversation returned nil for an existing file")
	}
	if got.Model != want.Model || got.StartedAt != want.StartedAt {
		t.Errorf("metadata mismatch: got %+v, want %+v", got, want)
	}
	if len(got.Messages) != 2 || got.Messages[0].Parts[0].Text != "hello" {
		t.Errorf("messages mismatch: got %+v", got.Messages)
	}
}

func TestLoadConversationMissingFile(t *testing.T) {
	conv, err := loadConversation(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if conv != nil {
		t.Errorf("expected nil conversation for missing file, got %+v", conv)
	}
}

func TestLoadConversationCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.json")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConversation(path); err == nil {
		t.Error("expected error for corrupt JSON, got nil")
	}
}

// --- Unit tests: API key resolution ---

func TestGetAPIKeyFromEnvVar(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "direct-key")
	t.Setenv("ASK_GEMINI_KEY_COMMAND", "echo should-not-be-used")
	key, err := getAPIKey()
	if err != nil {
		t.Fatalf("getAPIKey: %v", err)
	}
	if key != "direct-key" {
		t.Errorf("got %q, want GEMINI_API_KEY to take precedence", key)
	}
}

func TestGetAPIKeyFromCommand(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("ASK_GEMINI_KEY_COMMAND", "echo '  cmd-key  '")
	key, err := getAPIKey()
	if err != nil {
		t.Fatalf("getAPIKey: %v", err)
	}
	if key != "cmd-key" {
		t.Errorf("got %q, want trimmed %q", key, "cmd-key")
	}
}

func TestGetAPIKeyNoneConfigured(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("ASK_GEMINI_KEY_COMMAND", "")
	if _, err := getAPIKey(); err == nil {
		t.Error("expected error when no key is configured, got nil")
	}
}

func TestGetAPIKeyCommandEmptyOutput(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("ASK_GEMINI_KEY_COMMAND", "true") // exits 0, prints nothing
	if _, err := getAPIKey(); err == nil {
		t.Error("expected error when key command produces empty output, got nil")
	}
}

func TestGetAPIKeyCommandFails(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("ASK_GEMINI_KEY_COMMAND", "exit 3")
	if _, err := getAPIKey(); err == nil {
		t.Error("expected error when key command exits non-zero, got nil")
	}
}

// --- Unit tests: MIME detection ---

func TestDetectMimeType(t *testing.T) {
	// jpg/png/gif are stable across platforms via the standard mime package.
	stable := map[string]string{
		"photo.jpg":  "image/jpeg",
		"photo.JPEG": "image/jpeg",
		"anim.gif":   "image/gif",
	}
	for path, want := range stable {
		if got := detectMimeType(path); got != want {
			t.Errorf("detectMimeType(%q) = %q, want %q", path, got, want)
		}
	}

	// Detection must yield a plausible type for known media kinds — the exact
	// string depends on the system MIME database, so only check the prefix.
	prefixes := map[string]string{
		"clip.mov":   "video/",
		"clip.mp4":   "video/",
		"sound.mp3":  "audio/",
		"sound.flac": "audio/",
	}
	for path, want := range prefixes {
		if got := detectMimeType(path); !strings.HasPrefix(got, want) {
			t.Errorf("detectMimeType(%q) = %q, want prefix %q", path, got, want)
		}
	}

	// An extension with no mapping at all still returns a non-empty type.
	if got := detectMimeType("mystery.zzzznotreal"); got == "" {
		t.Error("detectMimeType returned empty string for an unknown extension")
	}
}

// --- Unit tests: stringSlice flag ---

func TestStringSlice(t *testing.T) {
	var s stringSlice
	for _, v := range []string{"a", "b", "c"} {
		if err := s.Set(v); err != nil {
			t.Fatalf("Set(%q): %v", v, err)
		}
	}
	if got := s.String(); got != "a,b,c" {
		t.Errorf("String() = %q, want %q", got, "a,b,c")
	}
	if len(s) != 3 {
		t.Errorf("len = %d, want 3", len(s))
	}
}

// --- Unit tests: prompt resolution ---

func TestResolvePrompt(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		stdin      string
		stdinPiped bool
		want       string
	}{
		{"arg only", []string{"hello", "world"}, "", false, "hello world"},
		{"stdin only", nil, "  piped body  ", true, "piped body"},
		{"both concatenated", []string{"the question"}, "the payload", true, "the question\n\nthe payload"},
		{"arg with empty piped stdin", []string{"q"}, "   ", true, "q"},
		{"neither", nil, "", false, ""},
		{"stdin present but not piped is ignored", nil, "ignored", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolvePrompt(tt.args, strings.NewReader(tt.stdin), tt.stdinPiped)
			if err != nil {
				t.Fatalf("resolvePrompt: %v", err)
			}
			if got != tt.want {
				t.Errorf("resolvePrompt(%q, %q, %v) = %q, want %q", tt.args, tt.stdin, tt.stdinPiped, got, tt.want)
			}
		})
	}
}

// blockingReader never returns from Read, simulating a stdin pipe that is open
// but never receives data or EOF — as when an agent invokes us via its shell and
// hands down an inherited, idle pipe. Regression guard for the hang fixed by
// time-bounding the optional stdin read in resolvePrompt.
type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) { select {} }

func TestResolvePromptDoesNotHangOnIdleStdin(t *testing.T) {
	type res struct {
		got string
		err error
	}
	ch := make(chan res, 1)
	go func() {
		got, err := resolvePrompt([]string{"q"}, blockingReader{}, true)
		ch <- res{got, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("resolvePrompt: %v", r.err)
		}
		if r.got != "q" {
			t.Errorf("got %q, want %q (arg should be used when stdin yields no payload)", r.got, "q")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resolvePrompt hung on an idle, never-EOF stdin with a positional prompt present")
	}
}

// --- HTTP-mocked integration tests: callGemini ---

// withMockAPI swaps apiBaseURL for the duration of the test.
func withMockAPI(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	orig := apiBaseURL
	apiBaseURL = srv.URL
	t.Cleanup(func() {
		apiBaseURL = orig
		srv.Close()
	})
	return srv
}

func TestCallGeminiSuccess(t *testing.T) {
	var gotPath string
	var gotReq GenerateRequest
	withMockAPI(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotReq)
		json.NewEncoder(w).Encode(GenerateResponse{
			Candidates: []Candidate{
				{Content: Content{Role: "model", Parts: []Part{{Text: "the answer"}}}},
			},
		})
	})

	conv := &Conversation{Messages: []Content{
		{Role: "user", Parts: []Part{{Text: "the question"}}},
	}}
	resp, err := callGemini("test-key", "gemini-3.5-flash", conv, "be terse", nil)
	if err != nil {
		t.Fatalf("callGemini: %v", err)
	}
	if resp != "the answer" {
		t.Errorf("response = %q, want %q", resp, "the answer")
	}
	if !strings.Contains(gotPath, "gemini-3.5-flash:generateContent") {
		t.Errorf("request path = %q, want it to include the model and method", gotPath)
	}
	if gotReq.SystemInstruction == nil || gotReq.SystemInstruction.Parts[0].Text != "be terse" {
		t.Errorf("system instruction not forwarded: %+v", gotReq.SystemInstruction)
	}
	if len(gotReq.Contents) != 1 || gotReq.Contents[0].Parts[0].Text != "the question" {
		t.Errorf("contents not forwarded: %+v", gotReq.Contents)
	}
}

func TestCallGeminiNoSystemPrompt(t *testing.T) {
	var gotReq GenerateRequest
	withMockAPI(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotReq)
		json.NewEncoder(w).Encode(GenerateResponse{
			Candidates: []Candidate{{Content: Content{Parts: []Part{{Text: "ok"}}}}},
		})
	})
	conv := &Conversation{Messages: []Content{{Role: "user", Parts: []Part{{Text: "q"}}}}}
	if _, err := callGemini("k", "m", conv, "", nil); err != nil {
		t.Fatalf("callGemini: %v", err)
	}
	if gotReq.SystemInstruction != nil {
		t.Errorf("expected no systemInstruction when prompt is empty, got %+v", gotReq.SystemInstruction)
	}
}

func TestCallGeminiAPIError(t *testing.T) {
	withMockAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(GenerateResponse{
			Error: &APIError{Code: 400, Status: "INVALID_ARGUMENT", Message: "bad model"},
		})
	})
	conv := &Conversation{Messages: []Content{{Role: "user", Parts: []Part{{Text: "q"}}}}}
	_, err := callGemini("k", "bogus-model", conv, "", nil)
	if err == nil {
		t.Fatal("expected error for API error response, got nil")
	}
	if !strings.Contains(err.Error(), "bad model") {
		t.Errorf("error %q should surface the API message", err)
	}
}

func TestCallGeminiEmptyCandidates(t *testing.T) {
	withMockAPI(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(GenerateResponse{Candidates: nil})
	})
	conv := &Conversation{Messages: []Content{{Role: "user", Parts: []Part{{Text: "q"}}}}}
	if _, err := callGemini("k", "m", conv, "", nil); err == nil {
		t.Error("expected error for empty candidates, got nil")
	}
}

func TestCallGeminiTruncatedStillReturnsText(t *testing.T) {
	withMockAPI(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(GenerateResponse{
			Candidates: []Candidate{{
				Content:      Content{Parts: []Part{{Text: "partial answer"}}},
				FinishReason: "MAX_TOKENS",
			}},
		})
	})
	conv := &Conversation{Messages: []Content{{Role: "user", Parts: []Part{{Text: "q"}}}}}
	resp, err := callGemini("k", "m", conv, "", nil)
	if err != nil {
		t.Fatalf("callGemini: %v", err)
	}
	if resp != "partial answer" {
		t.Errorf("response = %q, want the partial text returned despite MAX_TOKENS", resp)
	}
}

func TestCallGeminiBlockedEmptyParts(t *testing.T) {
	withMockAPI(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(GenerateResponse{
			Candidates: []Candidate{{FinishReason: "SAFETY"}},
		})
	})
	conv := &Conversation{Messages: []Content{{Role: "user", Parts: []Part{{Text: "q"}}}}}
	_, err := callGemini("k", "m", conv, "", nil)
	if err == nil {
		t.Fatal("expected error when candidate has no content parts, got nil")
	}
	if !strings.Contains(err.Error(), "SAFETY") {
		t.Errorf("error %q should surface the finishReason", err)
	}
}

func TestCallGeminiMalformedJSON(t *testing.T) {
	withMockAPI(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<html>not json</html>")
	})
	conv := &Conversation{Messages: []Content{{Role: "user", Parts: []Part{{Text: "q"}}}}}
	if _, err := callGemini("k", "m", conv, "", nil); err == nil {
		t.Error("expected error for malformed response body, got nil")
	}
}

func TestCallGeminiToolsForwarded(t *testing.T) {
	var gotReq GenerateRequest
	withMockAPI(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotReq)
		json.NewEncoder(w).Encode(GenerateResponse{
			Candidates: []Candidate{{Content: Content{Parts: []Part{{Text: "ok"}}}}},
		})
	})
	conv := &Conversation{Messages: []Content{{Role: "user", Parts: []Part{{Text: "q"}}}}}
	tools := []Tool{
		{GoogleSearch: &struct{}{}},
		{URLContext: &struct{}{}},
	}
	if _, err := callGemini("k", "m", conv, "", tools); err != nil {
		t.Fatalf("callGemini: %v", err)
	}
	if len(gotReq.Tools) != 2 {
		t.Fatalf("expected 2 tools in request, got %d: %+v", len(gotReq.Tools), gotReq.Tools)
	}
	if gotReq.Tools[0].GoogleSearch == nil {
		t.Error("expected first tool to be google_search")
	}
	if gotReq.Tools[1].URLContext == nil {
		t.Error("expected second tool to be url_context")
	}
}

func TestCallGeminiNoToolsOmitted(t *testing.T) {
	var gotBody []byte
	withMockAPI(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		json.NewEncoder(w).Encode(GenerateResponse{
			Candidates: []Candidate{{Content: Content{Parts: []Part{{Text: "ok"}}}}},
		})
	})
	conv := &Conversation{Messages: []Content{{Role: "user", Parts: []Part{{Text: "q"}}}}}
	if _, err := callGemini("k", "m", conv, "", nil); err != nil {
		t.Fatalf("callGemini: %v", err)
	}
	if strings.Contains(string(gotBody), `"tools"`) {
		t.Error("tools field should be omitted from request when no tools are specified")
	}
}
