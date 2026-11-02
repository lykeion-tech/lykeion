package pricing

import (
	"bytes"
	"log/slog"
	"math"
	"strings"
	"testing"
)

func TestHistoricalCodexPriceIsUnchanged(t *testing.T) {
	if got := costOf("gpt-5-codex", 2_000_000, 1_000_000, 3_000_000, 1_000_000); math.Abs(got-12.875) > 1e-9 {
		t.Fatalf("historical cost %v", got)
	}
}

func TestSupportedCodexEstimateBands(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		input, output, read, write int64
		want                       float64
		warning                    bool
	}{
		{"base_boundary", 122000, 10, 100000, 50000, 0.7782, false},
		{"upper_boundary", 122001, 10, 100000, 50000, 1.556308, true},
		{"output_only", 0, 10, 0, 0, 0.0002, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
			defer slog.SetDefault(prev)
			got := costOf("gpt-5.6-sol", tc.input, tc.output, tc.read, tc.write)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("cost %v, want %v", got, tc.want)
			}
			if strings.Contains(buf.String(), "conservative upper bound") != tc.warning {
				t.Fatalf("warning %q", buf.String())
			}
		})
	}
}

func costOf(model string, input, output, read, write int64) float64 {
	usd, _ := CostFor("openai", model, input, output, read, write)
	return usd
}

func TestUnknownModelUsesProvidersHighestPrice(t *testing.T) {
	want, _ := CostFor("openai", "gpt-5.6-sol", 1e6, 1e6, 0, 0)
	got, est := CostFor("openai", "gpt-6.1-sol", 1e6, 1e6, 0, 0)
	if !est || got != want || got == 0 {
		t.Fatalf("unknown openai model: %v estimated=%v, want %v", got, est, want)
	}
	if _, est := CostFor("openai", "gpt-5", 1e6, 1e6, 0, 0); est {
		t.Fatal("known model marked estimated")
	}
	// The dearest entry overall is gpt-5.6-sol.
	if got, est := CostFor("nobody", "x", 1e6, 1e6, 0, 0); !est || got != want {
		t.Fatalf("unknown provider: %v estimated=%v, want %v", got, est, want)
	}
}
