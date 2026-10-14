package pricing

import (
	"bytes"
	"log/slog"
	"math"
	"strings"
	"testing"
)

func TestKnownModelIsPricedPerMillionTokens(t *testing.T) {
	for model, p := range Prices {
		want := 2*p.Input + p.Output + 3*p.CacheRead + p.CacheWrite
		if got := Cost(model, 2_000_000, 1_000_000, 3_000_000, 1_000_000); math.Abs(got-want) > 1e-9 {
			t.Fatalf("%s: %v, want %v", model, got, want)
		}
	}
}

func TestUnknownModelCostsZeroAndWarns(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)
	if got := Cost("no-such-model", 1_000_000, 1_000_000, 0, 0); got != 0 {
		t.Fatalf("unknown model cost %v", got)
	}
	if !strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "no-such-model") {
		t.Fatalf("no warning logged: %q", buf.String())
	}
}
