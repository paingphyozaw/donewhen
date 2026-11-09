package models

import "strings"

// ModelPrice is what one model costs, in US dollars per million tokens.
// Cache writes are priced from Input: 1.25x for the 5-minute cache and 2x for
// the 1-hour cache.
type ModelPrice struct {
	Input     float64
	Output    float64
	CacheRead float64
}

// Cache write multipliers of the input price.
const (
	cacheWrite5mFactor = 1.25
	cacheWrite1hFactor = 2
)

// modelPrices is the one price table. A model id matches the longest prefix
// listed here, so `claude-sonnet-4-5-20250929` is priced as `claude-sonnet-4`
// and `claude-fable-5-1` is not priced as `claude-fable-5`.
//
// CHECK THESE AGAINST https://www.anthropic.com/pricing BEFORE TRUSTING A
// TOTAL. Prices change; a message's cost is stored when it is saved, so a
// change here never rewrites old sessions.
//
// Haiku 5.5 is listed at its price for prompts up to 100K tokens.
var modelPrices = map[string]ModelPrice{
	"claude-fable-5-1":  {Input: 10, Output: 50, CacheRead: 0.25},
	"claude-mythos-5-1": {Input: 10, Output: 50, CacheRead: 0.25},
	"claude-fable-5":    {Input: 10, Output: 50, CacheRead: 1},
	"claude-mythos-5":   {Input: 10, Output: 50, CacheRead: 1},

	"claude-opus-5-5": {Input: 4, Output: 20, CacheRead: 0.20},
	"claude-opus-5":   {Input: 5, Output: 25, CacheRead: 0.50},
	"claude-opus-4-8": {Input: 5, Output: 25, CacheRead: 0.50},
	"claude-opus-4-7": {Input: 5, Output: 25, CacheRead: 0.50},
	"claude-opus-4-6": {Input: 5, Output: 25, CacheRead: 0.50},
	"claude-opus-4-5": {Input: 5, Output: 25, CacheRead: 0.50},
	"claude-opus-4":   {Input: 15, Output: 75, CacheRead: 1.50}, // Opus 4 and 4.1

	"claude-sonnet-5-5": {Input: 2, Output: 10, CacheRead: 0.20},
	"claude-sonnet-5":   {Input: 2, Output: 10, CacheRead: 0.20},
	"claude-sonnet-4":   {Input: 3, Output: 15, CacheRead: 0.30},

	"claude-haiku-5-5": {Input: 0.10, Output: 0.50, CacheRead: 0.01},
	"claude-haiku-4":   {Input: 1, Output: 5, CacheRead: 0.10},
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

// MessageCost is the dollar cost of one message. An unknown model costs 0.
func MessageCost(m SessionMessage) float64 {
	p, ok := PriceFor(m.Model)
	if !ok {
		return 0
	}
	const perMillion = 1_000_000
	return (float64(m.InputTokens)*p.Input +
		float64(m.OutputTokens)*p.Output +
		float64(m.CacheReadTokens)*p.CacheRead +
		float64(m.CacheWrite5mTokens)*p.Input*cacheWrite5mFactor +
		float64(m.CacheWrite1hTokens)*p.Input*cacheWrite1hFactor) / perMillion
}

// KindCosts splits the cost of a model's tokens by token kind (input,
// output, cache read, cache write; the order of TokenKinds) with the price
// table, scaled so the four add up to stored: the cost saved with the
// messages. A model the table does not know, or a stored cost of 0, gives
// zeros.
func KindCosts(model string, in, out, cacheRead, write5m, write1h int64, stored float64) [4]float64 {
	var k [4]float64
	p, ok := PriceFor(model)
	if !ok || stored == 0 {
		return k
	}
	k[0] = float64(in) * p.Input
	k[1] = float64(out) * p.Output
	k[2] = float64(cacheRead) * p.CacheRead
	k[3] = float64(write5m)*p.Input*cacheWrite5mFactor + float64(write1h)*p.Input*cacheWrite1hFactor
	sum := k[0] + k[1] + k[2] + k[3]
	if sum == 0 {
		return [4]float64{}
	}
	for i := range k {
		k[i] = k[i] / sum * stored
	}
	return k
}
