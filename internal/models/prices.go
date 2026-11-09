package models

import "strings"

// ModelPrice is what one model costs, in US dollars per million tokens.
type ModelPrice struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

// modelPrices is the one price table. A model id matches the longest prefix
// listed here, so `claude-sonnet-4-5-20250929` is priced as `claude-sonnet-4`
// and `claude-fable-5-1` is not priced as `claude-fable-5`.
//
// CHECK THESE AGAINST https://www.anthropic.com/pricing BEFORE TRUSTING A
// TOTAL. Prices change; a session's cost is stored when it is saved, so a
// change here never rewrites old sessions.
//
// CacheWrite is the 1-hour cache write price (2x input). Claude Code writes its
// prompt cache with the 1-hour TTL; the 5-minute TTL costs 1.25x input. The
// transcripts give one cache-write count, so one price is used.
//
// Haiku 5.5 is listed at its price for prompts up to 100K tokens.
var modelPrices = map[string]ModelPrice{
	"claude-fable-5-1":  {Input: 10, Output: 50, CacheRead: 0.25, CacheWrite: 20},
	"claude-mythos-5-1": {Input: 10, Output: 50, CacheRead: 0.25, CacheWrite: 20},
	"claude-fable-5":    {Input: 10, Output: 50, CacheRead: 1, CacheWrite: 20},
	"claude-mythos-5":   {Input: 10, Output: 50, CacheRead: 1, CacheWrite: 20},

	"claude-opus-5-5": {Input: 4, Output: 20, CacheRead: 0.20, CacheWrite: 8},
	"claude-opus-5":   {Input: 5, Output: 25, CacheRead: 0.50, CacheWrite: 10},
	"claude-opus-4-8": {Input: 5, Output: 25, CacheRead: 0.50, CacheWrite: 10},
	"claude-opus-4-7": {Input: 5, Output: 25, CacheRead: 0.50, CacheWrite: 10},
	"claude-opus-4-6": {Input: 5, Output: 25, CacheRead: 0.50, CacheWrite: 10},
	"claude-opus-4-5": {Input: 5, Output: 25, CacheRead: 0.50, CacheWrite: 10},
	"claude-opus-4":   {Input: 15, Output: 75, CacheRead: 1.50, CacheWrite: 30}, // Opus 4 and 4.1

	"claude-sonnet-5-5": {Input: 2, Output: 10, CacheRead: 0.20, CacheWrite: 4},
	"claude-sonnet-5":   {Input: 2, Output: 10, CacheRead: 0.20, CacheWrite: 4},
	"claude-sonnet-4":   {Input: 3, Output: 15, CacheRead: 0.30, CacheWrite: 6},

	"claude-haiku-5-5": {Input: 0.10, Output: 0.50, CacheRead: 0.01, CacheWrite: 0.20},
	"claude-haiku-4":   {Input: 1, Output: 5, CacheRead: 0.10, CacheWrite: 2},
}

// PriceFor returns the price of a model by the longest matching prefix.
// ok is false for an unknown model.
func PriceFor(model string) (ModelPrice, bool) {
	best := ""
	for prefix := range modelPrices {
		if len(prefix) > len(best) && strings.HasPrefix(model, prefix) {
			best = prefix
		}
	}
	if best == "" {
		return ModelPrice{}, false
	}
	return modelPrices[best], true
}

// UsageCost is the dollar cost of one usage row. An unknown model costs 0.
func UsageCost(u SessionUsage) float64 {
	p, ok := PriceFor(u.Model)
	if !ok {
		return 0
	}
	const perMillion = 1_000_000
	return (float64(u.InputTokens)*p.Input +
		float64(u.OutputTokens)*p.Output +
		float64(u.CacheReadTokens)*p.CacheRead +
		float64(u.CacheWriteTokens)*p.CacheWrite) / perMillion
}
