package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultModel       = "gemini-3.5-flash"
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

type GenerateRequest struct {
	Contents          []Content        `json:"contents"`
	SystemInstruction *Content         `json:"systemInstruction,omitempty"`
	GenerationConfig  GenerationConfig `json:"generationConfig,omitempty"`
}

type GenerationConfig struct {
	Temperature     float64 `json:"temperature,omitempty"`
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
}

type GenerateResponse struct {
	Candidates []Candidate `json:"candidates"`
	Error      *APIError   `json:"error,omitempty"`
}

type Candidate struct {
	Content Content `json:"content"`
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

func callGemini(apiKey, model string, conversation *Conversation, systemPrompt string) (string, error) {
	url := fmt.Sprintf("%s/%s:generateContent?key=%s", apiBaseURL, model, apiKey)

	req := GenerateRequest{
		Contents: conversation.Messages,
		GenerationConfig: GenerationConfig{
			MaxOutputTokens: 8192,
		},
	}

	if systemPrompt != "" {
		req.SystemInstruction = &Content{
			Parts: []Part{{Text: systemPrompt}},
		}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("API call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	var result GenerateResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parsing response: %w\nraw: %s", err, string(respBody))
	}

	if result.Error != nil {
		return "", fmt.Errorf("API error %d (%s): %s", result.Error.Code, result.Error.Status, result.Error.Message)
	}

	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty response from Gemini\nraw: %s", string(respBody))
	}

	return result.Candidates[0].Content.Parts[0].Text, nil
}

// resolvePrompt combines the positional-argument prompt with piped stdin.
// When both are present, stdin is appended to the arg prompt so a framing
// question in the arg and a payload on stdin both reach the model. stdin is
// only read when stdinPiped is true.
func resolvePrompt(args []string, stdin io.Reader, stdinPiped bool) (string, error) {
	argPrompt := strings.TrimSpace(strings.Join(args, " "))
	var stdinPrompt string
	if stdinPiped {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", err
		}
		stdinPrompt = strings.TrimSpace(string(data))
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
	model := flag.String("model", defaultModel, "Gemini model ID")
	reset := flag.Bool("reset", false, "Reset conversation history")
	system := flag.String("system", "", "System prompt (used on first message or after reset)")
	showHistory := flag.Bool("history", false, "Show conversation history and exit")
	session := flag.String("session", "", "Session name; conversation stored at /tmp/ask-gemini-<name>.json")
	var photos stringSlice
	var videos stringSlice
	var audios stringSlice
	flag.Var(&photos, "photo", "Path to a photo to attach (repeatable)")
	flag.Var(&videos, "video", "Path to a video to attach (repeatable)")
	flag.Var(&audios, "audio", "Path to an audio file to attach (repeatable)")
	flag.Parse()

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
		fmt.Fprintln(os.Stderr, "Usage: ask-gemini [flags] <prompt>")
		fmt.Fprintln(os.Stderr, "       echo 'prompt' | ask-gemini [flags]")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Load or create conversation
	conv, err := loadConversation(convPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load conversation, starting fresh: %v\n", err)
	}
	if conv == nil {
		conv = &Conversation{
			Model:     *model,
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

	// Determine system prompt — use provided one, or default on first turn
	systemPrompt := *system
	if systemPrompt == "" && len(conv.Messages) == 1 {
		systemPrompt = "You are a knowledgeable software engineering consultant. " +
			"Give concise, direct answers. When analyzing code, focus on correctness, " +
			"edge cases, and non-obvious issues. If you disagree with an approach, say so clearly."
	}

	// Call Gemini
	response, err := callGemini(apiKey, conv.Model, conv, systemPrompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Add assistant response to conversation
	conv.Messages = append(conv.Messages, Content{
		Role:  "model",
		Parts: []Part{{Text: response}},
	})

	// Save conversation
	if err := saveConversation(convPath, conv); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save conversation: %v\n", err)
	}

	// Output response
	fmt.Println(response)
}
