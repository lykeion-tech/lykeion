// Package pricing prices token usage that an agent CLI reported without a cost.
package pricing

import "log/slog"

// Price is in US dollars per million tokens.
type Price struct {
	Provider                             string
	Input, Output, CacheRead, CacheWrite float64
}

// Prices are the providers' list prices per million tokens, keyed by the model
// name the CLI reports. Only models whose CLI reports no cost need a row.
var Prices = map[string]Price{
	// Standard prompts up to 200k tokens; longer prompts are undercounted.
	"gemini-3-pro": {Provider: "google", Input: 2, Output: 12, CacheRead: 0.2},
	// Cursor uses subscription billing; this is an API list-price estimate.
	"gpt-5":       {Provider: "openai", Input: 1.25, Output: 10, CacheRead: 0.125},
	"gpt-5-codex": {Provider: "openai", Input: 1.25, Output: 10, CacheRead: 0.125},
	"gpt-5.6-sol": {Provider: "openai", Input: 4, Output: 20, CacheRead: 0.4, CacheWrite: 5},
}

// ConservativeUpperBound identifies aggregate usage that could include requests
// in the long-context band. Individual request boundaries are unavailable, so
// applying that band to all tokens is an upper bound, not a billing claim.
func ConservativeUpperBound(model string, input, cacheRead, cacheWrite int64) bool {
	const threshold int64 = 272000
	return model == "gpt-5.6-sol" && (input > threshold || cacheRead > threshold-input || cacheWrite > threshold-input-cacheRead)
}

// CostFor estimates one usage row at standard API list prices. A model with no
// list price is priced at its provider's most expensive known model, or the
// most expensive overall when the provider has none, so the spend shown never
// understates what a run cost; estimated is true then.
func CostFor(provider, model string, input, output, cacheRead, cacheWrite int64) (usd float64, estimated bool) {
	p, ok := Prices[model]
	if !ok {
		estimated = true
		key := highest(provider)
		p = Prices[key]
		slog.Info("no list price for model; estimated at the provider's highest price", "model", model, "provider", provider, "priced_as", key)
		model = key
	}
	if ConservativeUpperBound(model, input, cacheRead, cacheWrite) {
		p.Input *= 2
		p.CacheRead *= 2
		p.CacheWrite *= 2
		p.Output *= 1.5
		slog.Warn("conservative upper bound for aggregate token usage; API list-price estimate, not provider billing", "model", model)
	}
	return (float64(input)*p.Input + float64(output)*p.Output +
		float64(cacheRead)*p.CacheRead + float64(cacheWrite)*p.CacheWrite) / 1e6, estimated
}

// highest names the dearest model of the provider (by input plus output
// price), or of all providers when it has none. Ties go to the smaller name.
func highest(provider string) string {
	best := ""
	for _, only := range []bool{true, false} {
		for k, p := range Prices {
			if only && p.Provider != provider {
				continue
			}
			if d := p.Input + p.Output; best == "" || d > Prices[best].Input+Prices[best].Output || (d == Prices[best].Input+Prices[best].Output && k < best) {
				best = k
			}
		}
		if best != "" {
			break
		}
	}
	return best
}
