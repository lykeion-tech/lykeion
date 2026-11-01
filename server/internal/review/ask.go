package review

import (
	"context"
	"fmt"
	"unicode/utf8"

	"lykeion/server/internal/fence"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/sources"
)

type Asker interface {
	Noul(ctx context.Context, state string, questions map[string]string) (map[string]float64, error)
	Choice(ctx context.Context, state, instructions string, options map[string]string) (jev.Answer, error)
}

type Sources interface {
	Paper(ctx context.Context, id string) (sources.Hit, error)
	Dataset(ctx context.Context, accession string) (sources.Hit, error)
	Search(ctx context.Context, query string) ([]sources.Hit, error)
}

// MaxState is the byte budget for one item's state: jev's 32k-token limit at
// 3 bytes a token, less room for the questions.
const MaxState = 32<<10*3 - 4096

// fenceOverhead leaves room for a fence's header, nonces and cut mark.
const fenceOverhead = 300

// excerpt cuts s to at most n bytes on a character boundary and says whether
// it cut anything.
func excerpt(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

// ItemState is what Jev reads about one item: the stage, the item and each
// piece of evidence fenced as untrusted, within budget bytes.
func ItemState(column, item string, evidence []string, budget int) string {
	head := fmt.Sprintf("Stage: %s\n", column)
	pieces := append([]string{item}, evidence...)
	share := (budget - len(head) - fenceOverhead*len(pieces)) / len(pieces)
	share = max(0, min(share, fence.Max-16))
	out := head
	for i, p := range pieces {
		label := "Item"
		if i > 0 {
			label = fmt.Sprintf("Evidence %d", i)
		}
		cut, was := excerpt(p, share)
		if was {
			cut += "\n[cut]"
		}
		out += fence.Untrusted(label, cut)
	}
	return out
}
