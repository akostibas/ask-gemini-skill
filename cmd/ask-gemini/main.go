package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

// version is overridden at build time via
// -ldflags "-X main.version=<tag>". Defaults to "dev" for `go run`/local builds.
var version = "dev"

const (
	defaultModel       = "gemini-3.7-flash"
	defaultImageModel  = "gemini-3.1-flash-image" // Nano Banana 2 (flash tier); auto-selected for --out
	sessionFilePrefix  = "ask-gemini-"
	defaultSessionName = "default"
	videoPollInterval  = 2 * time.Second
	videoPollTimeout   = 5 * time.Minute
)

// API endpoints are vars (not consts) so tests can point them at a local
// httptest server.
var (
	apiBaseURL    = "https://generativelanguage.googleapis.com/v1beta/models"
	uploadBaseURL = "https://generativelanguage.googleapis.com/upload/v1beta/files"
	fileBaseURL   = "https://generativelanguage.googleapis.com/v1beta"
)

// Gemini API types

type Content struct {
	Role  string `json:"role"`
	Parts []Part `json:"parts"`
}

type Part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *InlineData `json:"inlineData,omitempty"`
	FileData   *FileData   `json:"fileData,omitempty"`
}

type InlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type FileData struct {
	MimeType string `json:"mimeType"`
	FileURI  string `json:"fileUri"`
}

type Tool struct {
	GoogleSearch *struct{} `json:"google_search,omitempty"`
	URLContext   *struct{} `json:"url_context,omitempty"`
}

type GenerateRequest struct {
	Contents          []Content        `json:"contents"`
	SystemInstruction *Content         `json:"systemInstruction,omitempty"`
	GenerationConfig  GenerationConfig `json:"generationConfig,omitempty"`
	Tools             []Tool           `json:"tools,omitempty"`
}

type GenerationConfig struct {
	Temperature     float64 `json:"temperature,omitempty"`
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	// ResponseModalities requests non-text output. For image generation
	// (Nano Banana models), set to ["IMAGE","TEXT"] so the model returns the
	// picture as an inlineData part (plus any accompanying text).
	ResponseModalities []string `json:"responseModalities,omitempty"`
}

type GenerateResponse struct {
	Candidates    []Candidate    `json:"candidates"`
	UsageMetadata *UsageMetadata `json:"usageMetadata,omitempty"`
	Error         *APIError      `json:"error,omitempty"`
}

type UsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	// ThoughtsTokenCount is the hidden reasoning ("thinking") tokens on
	// thinking models. They're billed at the output rate and are part of
	// TotalTokenCount but not CandidatesTokenCount.
	ThoughtsTokenCount int `json:"thoughtsTokenCount"`
	TotalTokenCount    int `json:"totalTokenCount"`
}

type Candidate struct {
	Content      Content `json:"content"`
	FinishReason string  `json:"finishReason,omitempty"`
}

type APIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// File API types

type FileResource struct {
	Name      string `json:"name"`
	MimeType  string `json:"mimeType"`
	URI       string `json:"uri"`
	State     string `json:"state"`
	SizeBytes string `json:"sizeBytes,omitempty"`
}

type FileResponse struct {
	File  FileResource `json:"file"`
	Error *APIError    `json:"error,omitempty"`
}

// Conversation persistence

type Conversation struct {
	Model     string    `json:"model"`
	StartedAt string    `json:"started_at"`
	Messages  []Content `json:"messages"`
}

// geminiResult holds the parsed model turn: concatenated text plus any images
// returned as inlineData parts (base64, with their mime type).
type geminiResult struct {
	text   string
	images []InlineData
	usage  usage
}

// isImageModel reports whether a model ID produces images. Every Gemini image
// model carries "image" in its ID (e.g. gemini-3-pro-image,
// gemini-2.5-flash-image); text models never do. Used to keep --out and the
// chosen model consistent without a capability lookup.
func isImageModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "image")
}

// Multi-value string flag

type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

func sessionPath(session string) string {
	if session == "" {
		session = defaultSessionName
	}
	safe := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return '_'
		}
		return r
	}, session)
	return filepath.Join(os.TempDir(), sessionFilePrefix+safe+".json")
}

func loadConversation(path string) (*Conversation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var conv Conversation
	if err := json.Unmarshal(data, &conv); err != nil {
		return nil, err
	}
	return &conv, nil
}

func saveConversation(path string, conv *Conversation) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(conv, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func getAPIKey() (string, error) {
	if key := os.Getenv("GEMINI_API_KEY"); key != "" {
		return key, nil
	}
	keyCmd := os.Getenv("ASK_GEMINI_KEY_COMMAND")
	if keyCmd == "" {
		return "", fmt.Errorf("no API key: set GEMINI_API_KEY, or set ASK_GEMINI_KEY_COMMAND to a shell command whose stdout is the key (e.g. an `op`, `pass`, or `security` invocation)")
	}
	cmd := exec.Command("sh", "-c", keyCmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("ASK_GEMINI_KEY_COMMAND failed: %s", msg)
	}
	key := strings.TrimSpace(stdout.String())
	if key == "" {
		return "", fmt.Errorf("ASK_GEMINI_KEY_COMMAND produced empty output")
	}
	return key, nil
}

func detectMimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if mt := mime.TypeByExtension(ext); mt != "" {
		// strip parameters like "; charset=utf-8"
		if i := strings.Index(mt, ";"); i >= 0 {
			mt = strings.TrimSpace(mt[:i])
		}
		return mt
	}
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".heic":
		return "image/heic"
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".m4a":
		return "audio/mp4"
	case ".aac":
		return "audio/aac"
	case ".ogg":
		return "audio/ogg"
	case ".flac":
		return "audio/flac"
	}
	return "application/octet-stream"
}

// uploadFile uploads a file via the resumable File API and returns the
// FileResource. Caller is responsible for waiting for ACTIVE state.
func uploadFile(apiKey, path string) (*FileResource, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	mimeType := detectMimeType(path)

	// Step 1: initiate resumable upload
	initBody, _ := json.Marshal(map[string]any{
		"file": map[string]string{"displayName": filepath.Base(path)},
	})
	initURL := fmt.Sprintf("%s?key=%s", uploadBaseURL, apiKey)
	initReq, err := http.NewRequest("POST", initURL, bytes.NewReader(initBody))
	if err != nil {
		return nil, err
	}
	initReq.Header.Set("X-Goog-Upload-Protocol", "resumable")
	initReq.Header.Set("X-Goog-Upload-Command", "start")
	initReq.Header.Set("X-Goog-Upload-Header-Content-Length", fmt.Sprintf("%d", info.Size()))
	initReq.Header.Set("X-Goog-Upload-Header-Content-Type", mimeType)
	initReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	initResp, err := client.Do(initReq)
	if err != nil {
		return nil, fmt.Errorf("upload init: %w", err)
	}
	defer initResp.Body.Close()
	if initResp.StatusCode >= 300 {
		body, _ := io.ReadAll(initResp.Body)
		return nil, fmt.Errorf("upload init failed (%d): %s", initResp.StatusCode, string(body))
	}
	uploadURL := initResp.Header.Get("X-Goog-Upload-Url")
	if uploadURL == "" {
		return nil, fmt.Errorf("upload init: missing X-Goog-Upload-Url header")
	}

	// Step 2: upload bytes
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	uploadReq, err := http.NewRequest("POST", uploadURL, f)
	if err != nil {
		return nil, err
	}
	uploadReq.ContentLength = info.Size()
	uploadReq.Header.Set("X-Goog-Upload-Offset", "0")
	uploadReq.Header.Set("X-Goog-Upload-Command", "upload, finalize")

	uploadClient := &http.Client{Timeout: 30 * time.Minute}
	uploadResp, err := uploadClient.Do(uploadReq)
	if err != nil {
		return nil, fmt.Errorf("upload bytes: %w", err)
	}
	defer uploadResp.Body.Close()
	respBody, _ := io.ReadAll(uploadResp.Body)
	if uploadResp.StatusCode >= 300 {
		return nil, fmt.Errorf("upload bytes failed (%d): %s", uploadResp.StatusCode, string(respBody))
	}
	var fr FileResponse
	if err := json.Unmarshal(respBody, &fr); err != nil {
		return nil, fmt.Errorf("parsing upload response: %w\nraw: %s", err, string(respBody))
	}
	if fr.Error != nil {
		return nil, fmt.Errorf("upload API error %d (%s): %s", fr.Error.Code, fr.Error.Status, fr.Error.Message)
	}
	if fr.File.URI == "" {
		return nil, fmt.Errorf("upload response missing file.uri: %s", string(respBody))
	}
	return &fr.File, nil
}

// waitForActive polls the file resource until state == ACTIVE, or returns an
// error on FAILED / timeout.
func waitForActive(apiKey string, file *FileResource) error {
	if file.State == "ACTIVE" {
		return nil
	}
	deadline := time.Now().Add(videoPollTimeout)
	client := &http.Client{Timeout: 30 * time.Second}
	url := fmt.Sprintf("%s/%s?key=%s", fileBaseURL, file.Name, apiKey)
	for time.Now().Before(deadline) {
		time.Sleep(videoPollInterval)
		resp, err := client.Get(url)
		if err != nil {
			return fmt.Errorf("polling %s: %w", file.Name, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("polling %s failed (%d): %s", file.Name, resp.StatusCode, string(body))
		}
		var cur FileResource
		if err := json.Unmarshal(body, &cur); err != nil {
			return fmt.Errorf("parsing poll response: %w\nraw: %s", err, string(body))
		}
		switch cur.State {
		case "ACTIVE":
			*file = cur
			return nil
		case "FAILED":
			return fmt.Errorf("file %s entered FAILED state", file.Name)
		}
	}
	return fmt.Errorf("file %s did not become ACTIVE within %s", file.Name, videoPollTimeout)
}

func attachFiles(apiKey string, paths []string, requireActive bool) ([]Part, error) {
	var parts []Part
	for _, p := range paths {
		fmt.Fprintf(os.Stderr, "Uploading %s...\n", p)
		fr, err := uploadFile(apiKey, p)
		if err != nil {
			return nil, fmt.Errorf("uploading %s: %w", p, err)
		}
		if requireActive {
			fmt.Fprintf(os.Stderr, "Waiting for %s to be processed...\n", filepath.Base(p))
			if err := waitForActive(apiKey, fr); err != nil {
				return nil, err
			}
		}
		parts = append(parts, Part{
			FileData: &FileData{MimeType: fr.MimeType, FileURI: fr.URI},
		})
	}
	return parts, nil
}

func callGemini(apiKey, model string, conversation *Conversation, systemPrompt string, tools []Tool, imageOut bool) (*geminiResult, error) {
	url := fmt.Sprintf("%s/%s:generateContent?key=%s", apiBaseURL, model, apiKey)

	genConfig := GenerationConfig{
		MaxOutputTokens: 32768,
	}
	if imageOut {
		genConfig.ResponseModalities = []string{"IMAGE", "TEXT"}
	}

	req := GenerateRequest{
		Contents:         conversation.Messages,
		GenerationConfig: genConfig,
		Tools:            tools,
	}

	if systemPrompt != "" {
		req.SystemInstruction = &Content{
			Parts: []Part{{Text: systemPrompt}},
		}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("API call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var result GenerateResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w\nraw: %s", err, string(respBody))
	}

	if result.Error != nil {
		msg := fmt.Sprintf("API error %d (%s): %s", result.Error.Code, result.Error.Status, result.Error.Message)
		if result.Error.Code == 503 && len(tools) > 0 {
			msg += "\n(503 with grounding tools active can mean Google's fetcher refused or timed out on a URL — try --no-url-context if you passed one)"
		}
		return nil, fmt.Errorf("%s", msg)
	}

	if len(result.Candidates) == 0 {
		return nil, fmt.Errorf("empty response from Gemini\nraw: %s", string(respBody))
	}

	cand := result.Candidates[0]
	// STOP is the normal completion reason. Anything else (MAX_TOKENS, SAFETY,
	// RECITATION, ...) means the output is truncated or was blocked — warn so a
	// partial answer isn't mistaken for a complete one.
	if cand.FinishReason != "" && cand.FinishReason != "STOP" {
		fmt.Fprintf(os.Stderr, "Warning: Gemini stopped early (finishReason=%s); the response may be truncated or filtered.\n", cand.FinishReason)
	}

	if len(cand.Content.Parts) == 0 {
		return nil, fmt.Errorf("no content in Gemini response (finishReason=%q)\nraw: %s", cand.FinishReason, string(respBody))
	}

	// Collect every part: text is concatenated, image inlineData parts are
	// gathered for the caller to write out. Image models may return several
	// images and/or interleave explanatory text.
	res := &geminiResult{}
	for _, p := range cand.Content.Parts {
		switch {
		case p.Text != "":
			res.text += p.Text
		case p.InlineData != nil && strings.HasPrefix(p.InlineData.MimeType, "image/"):
			res.images = append(res.images, *p.InlineData)
		}
	}
	if m := result.UsageMetadata; m != nil {
		res.usage = usage{
			promptTokens:   m.PromptTokenCount,
			outputTokens:   m.CandidatesTokenCount,
			thinkingTokens: m.ThoughtsTokenCount,
			totalTokens:    m.TotalTokenCount,
		}
	}

	return res, nil
}

// writeImages decodes base64 image parts and writes them to disk. With a single
// image it writes exactly outPath; with several it inserts a 1-based index
// before the extension (out.png -> out-1.png, out-2.png). Returns the paths
// written, in order.
func writeImages(outPath string, images []InlineData) ([]string, error) {
	var written []string
	for i, img := range images {
		data, err := base64.StdEncoding.DecodeString(img.Data)
		if err != nil {
			return written, fmt.Errorf("decoding image %d: %w", i+1, err)
		}
		p := outPath
		if len(images) > 1 {
			ext := filepath.Ext(outPath)
			p = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(outPath, ext), i+1, ext)
		}
		// The model picks the output format (often JPEG); the user picks the
		// path. Honor the path but warn if its extension implies a different
		// format than the bytes we're writing, so a .png holding JPEG isn't a
		// silent surprise.
		if want := detectMimeType(p); strings.HasPrefix(want, "image/") && want != img.MimeType {
			fmt.Fprintf(os.Stderr, "Note: model returned %s but %s has a %s extension; wrote %s bytes to that path.\n",
				img.MimeType, filepath.Base(p), filepath.Ext(p), img.MimeType)
		}
		if err := os.WriteFile(p, data, 0644); err != nil {
			return written, fmt.Errorf("writing %s: %w", p, err)
		}
		written = append(written, p)
	}
	return written, nil
}

// resolveVersion reports the build version. A value injected via
// -ldflags "-X main.version=..." wins; otherwise it falls back to the module
// version Go embeds when installed with `go install ...@<tag>`.
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

// stdinAppendTimeout bounds how long we wait for an optional stdin payload when
// a positional prompt is also present. It only ever elapses for a stdin that is
// open but never reaches EOF (e.g. a parent process — like an agent's shell —
// that hands us an inherited pipe with no data and never closes it). A normal
// `echo … | ask-gemini "q"` reaches EOF and returns well under this.
const stdinAppendTimeout = 1 * time.Second

// readAllWithTimeout reads r to EOF, but gives up after d. ok is false on
// timeout or read error — the caller treats that as "no payload".
func readAllWithTimeout(r io.Reader, d time.Duration) (data []byte, ok bool) {
	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := io.ReadAll(r)
		ch <- result{b, err}
	}()
	select {
	case res := <-ch:
		return res.data, res.err == nil
	case <-time.After(d):
		return nil, false
	}
}

// resolvePrompt combines the positional-argument prompt with piped stdin.
// When both are present, stdin is appended to the arg prompt so a framing
// question in the arg and a payload on stdin both reach the model. stdin is
// only read when stdinPiped is true.
//
// With a positional prompt present, stdin is an OPTIONAL payload, so its read is
// time-bounded: an inherited-but-idle stdin (e.g. an agent invoking us via its
// shell, leaving a pipe open with no data and no EOF) must not hang us forever.
// With no positional prompt, stdin IS the prompt, so we block until EOF.
func resolvePrompt(args []string, stdin io.Reader, stdinPiped bool) (string, error) {
	argPrompt := strings.TrimSpace(strings.Join(args, " "))
	var stdinPrompt string
	if stdinPiped {
		if argPrompt != "" {
			if data, ok := readAllWithTimeout(stdin, stdinAppendTimeout); ok {
				stdinPrompt = strings.TrimSpace(string(data))
			}
		} else {
			data, err := io.ReadAll(stdin)
			if err != nil {
				return "", err
			}
			stdinPrompt = strings.TrimSpace(string(data))
		}
	}
	switch {
	case argPrompt != "" && stdinPrompt != "":
		return argPrompt + "\n\n" + stdinPrompt, nil
	case argPrompt != "":
		return argPrompt, nil
	default:
		return stdinPrompt, nil
	}
}

func main() {
	model := flag.String("model", defaultModel, "Gemini model ID (see Models below)")
	reset := flag.Bool("reset", false, "Reset conversation history")
	system := flag.String("system", "", "System prompt (used on first message or after reset)")
	showHistory := flag.Bool("history", false, "Show conversation history and exit")
	showVersion := flag.Bool("version", false, "Print version and exit")
	session := flag.String("session", "", "Session name; conversation stored at /tmp/ask-gemini-<name>.json")
	noSearch := flag.Bool("no-search", false, "Disable Google Search grounding (enabled by default)")
	noURLContext := flag.Bool("no-url-context", false, "Disable URL context fetching (enabled by default)")
	out := flag.String("out", "", "Generate an image and write it here (see Models below). Multiple images get -N suffixes.")
	var photos stringSlice
	var videos stringSlice
	var audios stringSlice
	flag.Var(&photos, "photo", "Path to a photo to attach (repeatable)")
	flag.Var(&videos, "video", "Path to a video to attach (repeatable)")
	flag.Var(&audios, "audio", "Path to an audio file to attach (repeatable)")

	flag.Usage = func() {
		w := flag.CommandLine.Output()
		fmt.Fprint(w, "ask-gemini — a second opinion from Google Gemini, or image generation with Nano Banana.\n\n"+
			"Usage:\n"+
			"  ask-gemini [flags] <prompt>\n"+
			"  echo 'prompt' | ask-gemini [flags]\n"+
			"  echo 'payload' | ask-gemini [flags] <prompt>\n\n"+
			"Flags:\n")
		// flag.PrintDefaults() renders single-dash names (-model); rewrite the
		// flag-name lines to the double-dash form to match how the flags are
		// documented and typed. Both forms work at parse time.
		var buf bytes.Buffer
		flag.CommandLine.SetOutput(&buf)
		flag.PrintDefaults()
		flag.CommandLine.SetOutput(w)
		for _, line := range strings.SplitAfter(buf.String(), "\n") {
			if strings.HasPrefix(line, "  -") && !strings.HasPrefix(line, "  --") {
				line = "  -" + line[2:] // "  -name" -> "  --name"
			}
			fmt.Fprint(w, line)
		}
		fmt.Fprintf(w, "\nModels:\n"+
			"  Text (default):  %s   (override with --model <id>)\n"+
			"  Image (--out):   %-27s   (Nano Banana 2 — default when --out is set)\n"+
			"                   %-27s   (Nano Banana Pro — highest quality)\n"+
			"                   %-27s   (cheapest)\n"+
			"                   %-27s   (Nano Banana — original)\n"+
			"  Any other Gemini model ID also works with --model.\n\n"+
			"Credentials (one required):\n"+
			"  GEMINI_API_KEY          the API key, directly\n"+
			"  ASK_GEMINI_KEY_COMMAND  a shell command whose stdout is the key\n",
			defaultModel, defaultImageModel,
			"gemini-3-pro-image", "gemini-3.1-flash-lite-image", "gemini-2.5-flash-image")
	}

	flag.Parse()

	if *showVersion {
		fmt.Println(resolveVersion())
		return
	}

	// Was --model given explicitly? We can't tell an explicit --model from the
	// default by value alone, so ask the flag package which flags were set.
	modelExplicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "model" {
			modelExplicit = true
		}
	})

	// Resolve the model and image mode. --out turns on image output. When --out
	// is set and no --model was given, auto-select the default image model. When
	// --model IS given, honor it but keep it consistent with --out: a text model
	// with --out (or an image model without --out) is a mistake we catch here
	// rather than after a wasted round trip.
	imageMode := *out != ""
	resolvedModel := *model
	switch {
	case imageMode && !modelExplicit:
		resolvedModel = defaultImageModel
	case imageMode && !isImageModel(resolvedModel):
		fmt.Fprintf(os.Stderr, "Error: --out needs an image-capable model, but %q is a text model.\n"+
			"Use one of: gemini-3-pro-image, gemini-3.1-flash-image, gemini-3.1-flash-lite-image, gemini-2.5-flash-image — or omit --model to use the default (%s).\n",
			resolvedModel, defaultImageModel)
		os.Exit(1)
	case !imageMode && isImageModel(resolvedModel):
		fmt.Fprintf(os.Stderr, "Error: %q only produces images; pass --out <path> to save the result.\n", resolvedModel)
		os.Exit(1)
	}

	convPath := sessionPath(*session)

	if *showHistory {
		conv, err := loadConversation(convPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading conversation: %v\n", err)
			os.Exit(1)
		}
		if conv == nil || len(conv.Messages) == 0 {
			fmt.Println("No conversation history.")
			return
		}
		fmt.Printf("Model: %s | Started: %s | Turns: %d | Path: %s\n\n", conv.Model, conv.StartedAt, len(conv.Messages), convPath)
		for _, msg := range conv.Messages {
			role := strings.ToUpper(msg.Role)
			fmt.Printf("--- %s ---\n", role)
			for _, p := range msg.Parts {
				switch {
				case p.Text != "":
					fmt.Println(p.Text)
				case p.FileData != nil:
					fmt.Printf("[file: %s (%s)]\n", p.FileData.FileURI, p.FileData.MimeType)
				case p.InlineData != nil:
					fmt.Printf("[inline: %s, %d bytes base64]\n", p.InlineData.MimeType, len(p.InlineData.Data))
				}
			}
			fmt.Println()
		}
		return
	}

	// Read prompt from args and/or stdin. When both are present, stdin is
	// appended to the arg prompt (the common "framing question + payload" case).
	stat, _ := os.Stdin.Stat()
	stdinPiped := (stat.Mode() & os.ModeCharDevice) == 0
	prompt, err := resolvePrompt(flag.Args(), os.Stdin, stdinPiped)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
		os.Exit(1)
	}

	hasInput := prompt != "" || len(photos) > 0 || len(videos) > 0 || len(audios) > 0

	// -reset deletes the session file. If a prompt or attachment is also given,
	// fall through and send it against a fresh session. If -reset is the only
	// thing supplied, exit after deleting.
	if *reset {
		if err := os.Remove(convPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Error removing %s: %v\n", convPath, err)
			os.Exit(1)
		}
		if !hasInput {
			return
		}
	}

	if !hasInput {
		flag.Usage()
		os.Exit(1)
	}

	// On a real consult, nudge (to stderr) if a newer release exists. Throttled
	// to one network check per day; silent on any failure.
	maybeNotifyUpdate(resolveVersion(), os.Stderr)

	// Load or create conversation
	conv, err := loadConversation(convPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load conversation, starting fresh: %v\n", err)
	}
	if conv == nil {
		conv = &Conversation{
			Model:     resolvedModel,
			StartedAt: time.Now().Format(time.RFC3339),
			Messages:  []Content{},
		}
	}

	// Get API key (needed up-front if uploading files)
	apiKey, err := getAPIKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Build user message parts: attachments first, then text.
	var parts []Part
	if len(photos) > 0 {
		photoParts, err := attachFiles(apiKey, photos, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		parts = append(parts, photoParts...)
	}
	if len(videos) > 0 {
		videoParts, err := attachFiles(apiKey, videos, true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		parts = append(parts, videoParts...)
	}
	if len(audios) > 0 {
		audioParts, err := attachFiles(apiKey, audios, true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		parts = append(parts, audioParts...)
	}
	if prompt != "" {
		parts = append(parts, Part{Text: prompt})
	}

	conv.Messages = append(conv.Messages, Content{
		Role:  "user",
		Parts: parts,
	})

	// Determine system prompt — use provided one, or the consultant default on
	// the first turn. The default is a text-consult persona, so skip it in image
	// mode (a --system prompt still applies if the user gives one).
	systemPrompt := *system
	if systemPrompt == "" && !imageMode && len(conv.Messages) == 1 {
		systemPrompt = "You are a knowledgeable software engineering consultant. " +
			"Give concise, direct answers. When analyzing code, focus on correctness, " +
			"edge cases, and non-obvious issues. If you disagree with an approach, say so clearly."
	}

	// Build tools list — grounding tools are enabled by default for text
	// consults, opt-out via flags. Image models don't support them, so omit
	// tools entirely in image mode.
	var tools []Tool
	if !imageMode {
		if !*noSearch {
			tools = append(tools, Tool{GoogleSearch: &struct{}{}})
		}
		if !*noURLContext {
			tools = append(tools, Tool{URLContext: &struct{}{}})
		}
	}

	// Call Gemini. Use the resolved model (which may differ from a stored
	// session's original model when --model/--out is set on this turn).
	res, err := callGemini(apiKey, resolvedModel, conv, systemPrompt, tools, imageMode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Add assistant response to conversation. Persist both the text and any
	// generated images (as inlineData) so a multi-turn --session can keep
	// editing the last image.
	var respParts []Part
	if res.text != "" {
		respParts = append(respParts, Part{Text: res.text})
	}
	for _, img := range res.images {
		respParts = append(respParts, Part{InlineData: &img})
	}
	conv.Messages = append(conv.Messages, Content{
		Role:  "model",
		Parts: respParts,
	})

	// Save conversation
	if err := saveConversation(convPath, conv); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save conversation: %v\n", err)
	}

	// Write generated images, if any were requested.
	if imageMode {
		if len(res.images) == 0 {
			fmt.Fprintln(os.Stderr, "Warning: the model returned no image.")
		} else {
			paths, err := writeImages(*out, res.images)
			for _, p := range paths {
				fmt.Fprintf(os.Stderr, "Saved image to %s\n", p)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}
	}

	// Output any text response.
	if res.text != "" {
		fmt.Println(res.text)
	}

	// Report token usage and estimated cost to stderr, unless suppressed.
	if os.Getenv("ASK_GEMINI_NO_USAGE") == "" {
		fmt.Fprintln(os.Stderr, formatUsage(resolvedModel, res.usage, len(res.images)))
	}
}
