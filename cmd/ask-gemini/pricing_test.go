package main

import (
	"math"
	"strings"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestEstimateCostText(t *testing.T) {
	// gemini-3.6-flash: $1.50/1M in, $7.50/1M out.
	u := usage{promptTokens: 1_000_000, outputTokens: 1_000_000, totalTokens: 2_000_000}
	cost, ok := estimateCost("gemini-3.6-flash", u, 0)
	if !ok {
		t.Fatal("expected a known price for gemini-3.6-flash")
	}
	if !approx(cost, 9.0) {
		t.Errorf("cost = %v, want 9.0 (1.50 + 7.50)", cost)
	}
}

func TestEstimateCostImageUsesPerImage(t *testing.T) {
	// Image models bill output per image, not per token — output tokens must
	// not inflate the cost. gemini-2.5-flash-image: $0.30/1M in, $0.039/image.
	u := usage{promptTokens: 1_000_000, outputTokens: 5_000, totalTokens: 1_005_000}
	cost, ok := estimateCost("gemini-2.5-flash-image", u, 2)
	if !ok {
		t.Fatal("expected a known price for gemini-2.5-flash-image")
	}
	want := 0.30 + 2*0.039 // input $0.30 + two images
	if !approx(cost, want) {
		t.Errorf("cost = %v, want %v", cost, want)
	}
}

func TestEstimateCostIncludesThinkingTokens(t *testing.T) {
	// Thinking tokens bill at the output rate and must not be dropped.
	// gemini-3.6-flash: $7.50/1M out. 1M visible + 1M thinking = $15 output.
	u := usage{outputTokens: 1_000_000, thinkingTokens: 1_000_000, totalTokens: 2_000_000}
	cost, ok := estimateCost("gemini-3.6-flash", u, 0)
	if !ok {
		t.Fatal("expected a known price")
	}
	if !approx(cost, 15.0) {
		t.Errorf("cost = %v, want 15.0 (2M output-billed tokens at $7.50/1M)", cost)
	}
}

func TestEstimateCostUnknownModel(t *testing.T) {
	if _, ok := estimateCost("gemini-made-up", usage{promptTokens: 100}, 0); ok {
		t.Error("expected unknown model to report no price")
	}
}

func TestFormatUsageKnownModel(t *testing.T) {
	// 1240 in + (830 visible + 98 thinking = 928) out = 2070 total. The "out"
	// figure includes thinking, and the components sum to the total.
	u := usage{promptTokens: 1240, outputTokens: 830, thinkingTokens: 98, totalTokens: 2168}
	got := formatUsage("gemini-3.6-flash", u, 0)
	for _, want := range []string{"1,240 in", "928 out", "98 thinking", "2,168 tokens", "est. cost: ~$", pricingVerified} {
		if !strings.Contains(got, want) {
			t.Errorf("formatUsage output %q missing %q", got, want)
		}
	}
}

func TestFormatUsageNoThinkingOmitsNote(t *testing.T) {
	u := usage{promptTokens: 100, outputTokens: 50, totalTokens: 150}
	got := formatUsage("gemini-3.6-flash", u, 0)
	if strings.Contains(got, "thinking") {
		t.Errorf("did not expect a thinking note when thinkingTokens is 0, got %q", got)
	}
}

func TestFormatUsageUnknownModel(t *testing.T) {
	got := formatUsage("gemini-made-up", usage{promptTokens: 10, totalTokens: 10}, 0)
	if !strings.Contains(got, "unknown") {
		t.Errorf("expected unknown-price note, got %q", got)
	}
}

func TestFormatUsageNotReported(t *testing.T) {
	got := formatUsage("gemini-3.6-flash", usage{}, 0)
	if !strings.Contains(got, "not reported") {
		t.Errorf("expected 'not reported' when the API returns no usage, got %q", got)
	}
}

func TestFormatUSDPrecision(t *testing.T) {
	cases := map[float64]string{
		2.5:      "2.50",
		0.134:    "0.1340",
		0.000039: "0.000039",
	}
	for in, want := range cases {
		if got := formatUSD(in); got != want {
			t.Errorf("formatUSD(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestCommafy(t *testing.T) {
	cases := map[int]string{0: "0", 42: "42", 999: "999", 1000: "1,000", 1234567: "1,234,567"}
	for in, want := range cases {
		if got := commafy(in); got != want {
			t.Errorf("commafy(%d) = %q, want %q", in, got, want)
		}
	}
}
