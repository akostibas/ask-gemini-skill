package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

const defaultDecisionModel = "gemini-decision-1"

var decisionsURL = "https://generativelanguage.googleapis.com/v1beta/decisions"

var questionKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type decisionBlock struct {
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	Data       string `json:"data,omitempty"`
	MimeType   string `json:"mime_type,omitempty"`
	Resolution string `json:"resolution,omitempty"`
}

type decisionRequest struct {
	Model     string                     `json:"model"`
	Input     []decisionBlock            `json:"input"`
	Questions map[string]json.RawMessage `json:"questions"`
}

type decisionResponse struct {
	Answers json.RawMessage `json:"answers"`
	Usage   struct {
		TotalInputTokens int `json:"total_input_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	// Decisions errors carry a string code ("safety", ...), unlike generateContent.
	Error *struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

// splitKey peels an optional "key=" prefix; without one, the key is q<n>.
func splitKey(s string, n int) (string, string) {
	if k, rest, ok := strings.Cut(s, "="); ok && questionKeyRe.MatchString(k) {
		return k, rest
	}
	return fmt.Sprintf("q%d", n), s
}

// parseFlagQuestions turns --yesno and --choice values into API question objects.
// --choice is "[key=]question:opt1,opt2,..."; the LAST colon splits the options.
func parseFlagQuestions(yesno, choice []string, start int) (map[string]json.RawMessage, error) {
	qs := map[string]json.RawMessage{}
	n := start
	add := func(key string, q any) error {
		if _, dup := qs[key]; dup {
			return fmt.Errorf("duplicate question key %q", key)
		}
		b, _ := json.Marshal(q)
		qs[key] = b
		return nil
	}
	for _, s := range yesno {
		n++
		key, text := splitKey(s, n)
		if err := add(key, map[string]any{"type": "noul", "instructions": strings.TrimSpace(text)}); err != nil {
			return nil, err
		}
	}
	for _, s := range choice {
		n++
		key, rest := splitKey(s, n)
		i := strings.LastIndex(rest, ":")
		if i < 0 {
			return nil, fmt.Errorf("--choice %q: want \"[key=]question:opt1,opt2\"", s)
		}
		criteria := map[string]string{}
		for _, o := range strings.Split(rest[i+1:], ",") {
			if o = strings.TrimSpace(o); o != "" {
				criteria[o] = ""
			}
		}
		if len(criteria) < 2 {
			return nil, fmt.Errorf("--choice %q: need at least 2 options", s)
		}
		if err := add(key, map[string]any{"type": "choice", "instructions": strings.TrimSpace(rest[:i]), "criteria": criteria}); err != nil {
			return nil, err
		}
	}
	return qs, nil
}

func buildDecisionInput(text string, files []string, resolution string) ([]decisionBlock, error) {
	var in []decisionBlock
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", p, err)
		}
		mt := detectMimeType(p)
		b := decisionBlock{Data: base64.StdEncoding.EncodeToString(data), MimeType: mt}
		switch {
		case strings.HasPrefix(mt, "image/"):
			b.Type, b.Resolution = "image", resolution
		case strings.HasPrefix(mt, "video/"):
			b.Type, b.Resolution = "video", resolution
		case strings.HasPrefix(mt, "audio/"):
			b.Type = "audio"
		case mt == "application/pdf":
			b.Type = "document"
		default:
			// Plain-text-ish files (code, diffs, logs) go in as text.
			b = decisionBlock{Type: "text", Text: string(data) + "\n"}
		}
		in = append(in, b)
	}
	if text != "" {
		in = append(in, decisionBlock{Type: "text", Text: text})
	}
	return in, nil
}

func callDecisions(apiKey string, dr decisionRequest) (*decisionResponse, error) {
	body, err := json.Marshal(dr)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", decisionsURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("API call failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	var r decisionResponse
	if err := json.Unmarshal(respBody, &r); err != nil {
		return nil, fmt.Errorf("parsing response (HTTP %d): %w\nraw: %.500s", resp.StatusCode, err, respBody)
	}
	if r.Error != nil {
		return nil, fmt.Errorf("API error %v: %s", r.Error.Code, r.Error.Message)
	}
	if resp.StatusCode != http.StatusOK || len(r.Answers) == 0 {
		return nil, fmt.Errorf("unexpected response (HTTP %d)\nraw: %.500s", resp.StatusCode, respBody)
	}
	return &r, nil
}

// runDecide implements `ask-gemini decide`; returns the process exit code.
func runDecide(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("decide", flag.ContinueOnError)
	fs.SetOutput(stderr)
	model := fs.String("model", defaultDecisionModel, "Decision model ID")
	resolution := fs.String("resolution", "", "Image/video detail: low, medium, high, or ultra_high (images only); default is the API's")
	questionsPath := fs.String("questions", "", "JSON file of questions in the API's format (a map of key to question); - reads stdin")
	var files, yesno, choice stringSlice
	fs.Var(&files, "file", "File to decide about: image, video, audio, PDF, or text (repeatable)")
	fs.Var(&yesno, "yesno", "Yes/no question, \"[key=]question\" (repeatable)")
	fs.Var(&choice, "choice", "Multiple-choice question, \"[key=]question:opt1,opt2,...\" (repeatable)")
	fs.Usage = func() {
		fmt.Fprint(stderr, "ask-gemini decide — fast yes/no, multiple-choice and rubric answers with probabilities.\n\n"+
			"Usage:\n"+
			"  ask-gemini decide [flags] [context text]\n\n"+
			"Example:\n"+
			"  ask-gemini decide --file cam.jpg --yesno 'rat=Is there a rat?' --choice 'who=Who is visible?:kid,adult,nobody'\n\n"+
			"Prints the answers as JSON on stdout. Question keys default to q1, q2, ...\n"+
			"Rubric (score) questions and yes/no criteria are only available via --questions.\n"+
			"Docs: https://aistudio.google.com/docs/decisions-api\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	fail := func(err error) int { fmt.Fprintf(stderr, "Error: %v\n", err); return 1 }

	questions := map[string]json.RawMessage{}
	if *questionsPath != "" {
		var data []byte
		var err error
		if *questionsPath == "-" {
			data, err = io.ReadAll(stdin)
		} else {
			data, err = os.ReadFile(*questionsPath)
		}
		if err != nil {
			return fail(fmt.Errorf("reading questions: %w", err))
		}
		if err := json.Unmarshal(data, &questions); err != nil {
			return fail(fmt.Errorf("questions must be a JSON object of key -> question: %w", err))
		}
	}
	flagQs, err := parseFlagQuestions(yesno, choice, len(questions))
	if err != nil {
		return fail(err)
	}
	for k, v := range flagQs {
		if _, dup := questions[k]; dup {
			return fail(fmt.Errorf("question key %q is in both --questions and a flag", k))
		}
		questions[k] = v
	}

	text := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if len(questions) == 0 || (text == "" && len(files) == 0) {
		fs.Usage()
		return 2
	}
	input, err := buildDecisionInput(text, files, *resolution)
	if err != nil {
		return fail(err)
	}

	maybeNotifyUpdate(resolveVersion(), stderr)
	apiKey, err := getAPIKey()
	if err != nil {
		return fail(err)
	}
	r, err := callDecisions(apiKey, decisionRequest{Model: *model, Input: input, Questions: questions})
	if err != nil {
		return fail(err)
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, r.Answers, "", "  ") != nil {
		pretty.Reset()
		pretty.Write(r.Answers)
	}
	fmt.Fprintln(stdout, pretty.String())
	if os.Getenv("ASK_GEMINI_NO_USAGE") == "" {
		fmt.Fprintln(stderr, formatUsage(*model, usage{promptTokens: r.Usage.TotalInputTokens, totalTokens: r.Usage.TotalTokens}, 0))
	}
	return 0
}
