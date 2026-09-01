package main

import (
	"fmt"
	"strconv"
	"strings"
)

// pricingVerified is when the modelPricing table below was last checked against
// Google's published rates (https://ai.google.dev/gemini-api/docs/pricing). The
// cost line is stamped with this date; when prices change, update the numbers
// AND this date together.
const pricingVerified = "2026-08"

// modelPrice holds standard-tier USD rates for one model. Text models set
// inputPerM/outputPerM. Image models set inputPerM and perImage — their output
// is billed per generated image (at the default ~1K resolution), so outputPerM
// stays 0. Values are estimates; see pricingVerified.
type modelPrice struct {
	inputPerM  float64 // USD per 1M input tokens
	outputPerM float64 // USD per 1M output (text) tokens
	perImage   float64 // USD per generated image at default resolution
}

// modelPricing is the declarative price table. Add a row to price a new model;
// a model absent here reports tokens but "cost: unknown". Image input rates are
// approximate (input is a tiny fraction of image cost, which the per-image rate
// dominates).
// The 3.x Flash rates below are introductory and double on 2027-01-01
// ($1.50 in / $7.50 out) — update them and pricingVerified together then.
var modelPricing = map[string]modelPrice{
	"gemini-3.7-flash":            {inputPerM: 0.75, outputPerM: 3.75},
	"gemini-3.6-flash":            {inputPerM: 0.75, outputPerM: 3.75},
	"gemini-3.5-flash-lite":       {inputPerM: 0.30, outputPerM: 2.50},
	"gemini-2.5-flash":            {inputPerM: 0.30, outputPerM: 2.50},
	"gemini-3-pro-image":          {inputPerM: 2.00, perImage: 0.134},
	"gemini-3.1-flash-image":      {inputPerM: 0.50, perImage: 0.067},
	"gemini-3.1-flash-lite-image": {inputPerM: 0.25, perImage: 0.0336},
	"gemini-2.5-flash-image":      {inputPerM: 0.30, perImage: 0.039},
}

// usage carries the token counts the API reports for one request. outputTokens
// is the visible answer (candidates); thinkingTokens is hidden reasoning on
// thinking models, billed at the output rate.
type usage struct {
	promptTokens   int
	outputTokens   int
	thinkingTokens int
	totalTokens    int
}

// reported is true when the API actually returned token counts.
func (u usage) reported() bool { return u.totalTokens > 0 || u.promptTokens > 0 }

// billedOutput is what the output rate applies to: the answer plus any hidden
// thinking tokens.
func (u usage) billedOutput() int { return u.outputTokens + u.thinkingTokens }

// estimateCost returns the estimated USD cost for a request and whether a price
// was known for the model. numImages counts generated images (0 for text).
func estimateCost(model string, u usage, numImages int) (float64, bool) {
	p, ok := modelPricing[model]
	if !ok {
		return 0, false
	}
	cost := float64(u.promptTokens) / 1e6 * p.inputPerM
	if p.perImage > 0 {
		cost += float64(numImages) * p.perImage
	} else {
		cost += float64(u.billedOutput()) / 1e6 * p.outputPerM
	}
	return cost, true
}

// formatUsage builds the usage/cost report printed to stderr after a request.
func formatUsage(model string, u usage, numImages int) string {
	if !u.reported() {
		return "usage: not reported by the API"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "usage: %s in + %s out",
		commafy(u.promptTokens), commafy(u.billedOutput()))
	if u.thinkingTokens > 0 {
		fmt.Fprintf(&b, " (%s thinking)", commafy(u.thinkingTokens))
	}
	fmt.Fprintf(&b, " = %s tokens", commafy(u.totalTokens))
	if cost, ok := estimateCost(model, u, numImages); ok {
		fmt.Fprintf(&b, "\nest. cost: ~$%s (%s, prices as of %s)", formatUSD(cost), model, pricingVerified)
	} else {
		fmt.Fprintf(&b, "\nest. cost: unknown (no baked-in price for %s)", model)
	}
	return b.String()
}

// formatUSD renders a dollar amount with enough precision to be meaningful for
// sub-cent consults without a wall of zeros for larger ones.
func formatUSD(c float64) string {
	switch {
	case c >= 1:
		return fmt.Sprintf("%.2f", c)
	case c >= 0.01:
		return fmt.Sprintf("%.4f", c)
	default:
		return fmt.Sprintf("%.6f", c)
	}
}

// commafy formats a non-negative integer with thousands separators (1240 ->
// "1,240").
func commafy(n int) string {
	s := strconv.Itoa(n)
	if n < 1000 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}
