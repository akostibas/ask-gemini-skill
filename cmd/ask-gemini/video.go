package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultVideoModel      = "gemini-omni-1.1-flash"
	defaultVideoResolution = "360p"
)

// Omni only speaks the Interactions API, not generateContent.
var interactionsURL = "https://generativelanguage.googleapis.com/v1beta/interactions"

// videoPerSecond is USD per second of generated video, by resolution (see
// pricingVerified). Omni bills video per output second, not per token.
var videoPerSecond = map[string]float64{
	"360p":  0.03,
	"720p":  0.10,
	"1080p": 0.15,
	"4k":    0.30,
}

func isVideoModel(model string) bool { return strings.Contains(strings.ToLower(model), "omni") }

func isVideoPath(path string) bool { return strings.HasPrefix(detectMimeType(path), "video/") }

type interactionPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type interactionRequest struct {
	Model          string            `json:"model"`
	Input          []interactionPart `json:"input"`
	ResponseFormat map[string]string `json:"response_format"`
}

type interactionResponse struct {
	Status string `json:"status"`
	Steps  []struct {
		Type    string            `json:"type"`
		Content []interactionPart `json:"content"`
	} `json:"steps"`
	Error *APIError `json:"error,omitempty"`
}

// callOmni generates one video clip from a prompt plus optional still images
// (image-to-video). Returns the raw video bytes and their mime type.
func callOmni(apiKey, model, prompt string, photos []string, resolution string) ([]byte, string, error) {
	var input []interactionPart
	for _, p := range photos {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, "", fmt.Errorf("reading %s: %w", p, err)
		}
		input = append(input, interactionPart{Type: "image", Data: base64.StdEncoding.EncodeToString(data), MimeType: detectMimeType(p)})
	}
	if prompt != "" {
		input = append(input, interactionPart{Type: "text", Text: prompt})
	}
	body, err := json.Marshal(interactionRequest{
		Model:          model,
		Input:          input,
		ResponseFormat: map[string]string{"type": "video", "resolution": resolution},
	})
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequest("POST", interactionsURL, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)

	// Generation is synchronous and can take minutes at higher resolutions.
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("API call failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("reading response: %w", err)
	}
	var r interactionResponse
	if err := json.Unmarshal(respBody, &r); err != nil {
		return nil, "", fmt.Errorf("parsing response (HTTP %d): %w\nraw: %.500s", resp.StatusCode, err, respBody)
	}
	if r.Error != nil {
		return nil, "", fmt.Errorf("API error %d (%s): %s", r.Error.Code, r.Error.Status, r.Error.Message)
	}
	for _, s := range r.Steps {
		for _, c := range s.Content {
			if c.Type == "video" && c.Data != "" {
				data, err := base64.StdEncoding.DecodeString(c.Data)
				if err != nil {
					return nil, "", fmt.Errorf("decoding video: %w", err)
				}
				return data, c.MimeType, nil
			}
		}
	}
	return nil, "", fmt.Errorf("no video in response (status=%q)\nraw: %.500s", r.Status, respBody)
}

// mp4Duration reads the clip length from the top-level moov/mvhd box. ok is
// false if the bytes aren't a parseable MP4.
func mp4Duration(b []byte) (float64, bool) {
	box := func(b []byte, want string) []byte {
		for len(b) >= 8 {
			size := int(binary.BigEndian.Uint32(b))
			if size < 8 || size > len(b) {
				return nil
			}
			if string(b[4:8]) == want {
				return b[8:size]
			}
			b = b[size:]
		}
		return nil
	}
	mvhd := box(box(b, "moov"), "mvhd")
	if len(mvhd) < 4 {
		return 0, false
	}
	var scale, dur uint64
	if mvhd[0] == 1 && len(mvhd) >= 32 { // version 1: 64-bit times
		scale, dur = uint64(binary.BigEndian.Uint32(mvhd[20:])), binary.BigEndian.Uint64(mvhd[24:])
	} else if len(mvhd) >= 20 {
		scale, dur = uint64(binary.BigEndian.Uint32(mvhd[12:])), uint64(binary.BigEndian.Uint32(mvhd[16:]))
	}
	if scale == 0 {
		return 0, false
	}
	return float64(dur) / float64(scale), true
}

// formatVideoCost is the stderr cost line for a generated clip.
func formatVideoCost(model, resolution string, video []byte) string {
	secs, ok := mp4Duration(video)
	if !ok {
		return fmt.Sprintf("est. cost: unknown (couldn't read clip length; %s at %s is $%.2f/s)", model, resolution, videoPerSecond[resolution])
	}
	return fmt.Sprintf("video: %.1fs at %s\nest. cost: ~$%s (%s, prices as of %s)",
		secs, resolution, formatUSD(secs*videoPerSecond[resolution]), model, pricingVerified)
}
