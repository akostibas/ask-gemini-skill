package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeMP4 builds a minimal ftyp+moov/mvhd(v0) file of the given length.
func fakeMP4(timescale, duration uint32) []byte {
	mk := func(typ string, payload []byte) []byte {
		b := make([]byte, 8, 8+len(payload))
		binary.BigEndian.PutUint32(b, uint32(8+len(payload)))
		copy(b[4:], typ)
		return append(b, payload...)
	}
	mvhd := make([]byte, 100)
	binary.BigEndian.PutUint32(mvhd[12:], timescale)
	binary.BigEndian.PutUint32(mvhd[16:], duration)
	return append(mk("ftyp", []byte("isom0000")), mk("moov", mk("mvhd", mvhd))...)
}

func TestMP4Duration(t *testing.T) {
	if d, ok := mp4Duration(fakeMP4(1000, 8500)); !ok || d != 8.5 {
		t.Errorf("got %v %v, want 8.5 true", d, ok)
	}
	if _, ok := mp4Duration([]byte("not a video")); ok {
		t.Error("garbage parsed as mp4")
	}
}

func TestFormatVideoCost(t *testing.T) {
	got := formatVideoCost(defaultVideoModel, "720p", fakeMP4(1000, 10000))
	if !strings.Contains(got, "10.0s at 720p") || !strings.Contains(got, "~$1.00") {
		t.Errorf("unexpected cost line: %s", got)
	}
	if got := formatVideoCost(defaultVideoModel, "360p", nil); !strings.Contains(got, "unknown") {
		t.Errorf("want unknown cost for unparseable clip, got: %s", got)
	}
}

func TestVideoModeDetection(t *testing.T) {
	if !isVideoModel("gemini-omni-1.1-flash") || isVideoModel(defaultModel) || isVideoModel(defaultImageModel) {
		t.Error("isVideoModel misclassified a model")
	}
	if !isVideoPath("clip.mp4") || !isVideoPath("a.MOV") || isVideoPath("pic.png") {
		t.Error("isVideoPath misclassified a path")
	}
}

func TestCallOmni(t *testing.T) {
	clip := fakeMP4(1000, 3000)
	var got interactionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "k" {
			t.Errorf("api key header missing")
		}
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(map[string]any{
			"status": "completed",
			"steps": []any{
				map[string]any{"type": "user_input"},
				map[string]any{"type": "model_output", "content": []any{
					map[string]any{"type": "video", "mime_type": "video/mp4", "data": base64.StdEncoding.EncodeToString(clip)},
				}},
			},
		})
	}))
	defer srv.Close()
	orig := interactionsURL
	interactionsURL = srv.URL
	defer func() { interactionsURL = orig }()

	photo := filepath.Join(t.TempDir(), "still.png")
	os.WriteFile(photo, []byte("png"), 0644)

	data, mt, err := callOmni("k", defaultVideoModel, "make it move", []string{photo}, "360p")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(clip) || mt != "video/mp4" {
		t.Errorf("got %d bytes %q", len(data), mt)
	}
	if got.Model != defaultVideoModel || got.ResponseFormat["resolution"] != "360p" || got.ResponseFormat["type"] != "video" {
		t.Errorf("bad request: %+v", got)
	}
	if len(got.Input) != 2 || got.Input[0].Type != "image" || got.Input[0].MimeType != "image/png" || got.Input[1].Text != "make it move" {
		t.Errorf("bad input parts: %+v", got.Input)
	}
}

func TestCallOmniErrors(t *testing.T) {
	orig := interactionsURL
	defer func() { interactionsURL = orig }()
	for name, body := range map[string]string{
		"api error": `{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"bad prompt"}}`,
		"no video":  `{"status":"failed","steps":[]}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		interactionsURL = srv.URL
		if _, _, err := callOmni("k", defaultVideoModel, "x", nil, "360p"); err == nil {
			t.Errorf("%s: want error", name)
		}
		srv.Close()
	}
}
