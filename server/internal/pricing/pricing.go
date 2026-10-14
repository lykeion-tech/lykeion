// Package pricing prices token usage that an agent CLI reported without a cost.
package pricing

import "log/slog"

// Price is in US dollars per million tokens.
type Price struct {
	Input, Output, CacheRead, CacheWrite float64
}

// Prices are the providers' list prices per million tokens, keyed by the model
// name the CLI reports. Only models whose CLI reports no cost need a row.
var Prices = map[string]Price{
	"gpt-5-codex": {Input: 1.25, Output: 10, CacheRead: 0.125},
}

// Cost prices one usage row. A model missing from Prices costs nothing, so a
// card's budget cannot see it; the warning is how anyone finds out.
func Cost(model string, input, output, cacheRead, cacheWrite int64) float64 {
	p, ok := Prices[model]
	if !ok {
		slog.Warn("no list price for model; usage recorded at zero cost", "model", model)
		return 0
	}
	return (float64(input)*p.Input + float64(output)*p.Output +
		float64(cacheRead)*p.CacheRead + float64(cacheWrite)*p.CacheWrite) / 1e6
}
