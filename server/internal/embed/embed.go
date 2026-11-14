// Package embed provides the local, pinned lab embedding model.
package embed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

const Model = "Snowflake/snowflake-arctic-embed-s"
const Dimensions = 384
const QueryPrefix = "Represent this sentence for searching relevant passages: "

type Embedder interface {
	Embed(context.Context, []string, bool) ([][]float32, error)
}
type TEI struct {
	URL    string
	Client *http.Client
}

func (t TEI) Embed(ctx context.Context, texts []string, query bool) ([][]float32, error) {
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += 32 {
		end := min(start+32, len(texts))
		inputs := append([]string(nil), texts[start:end]...)
		if query {
			for i := range inputs {
				inputs[i] = QueryPrefix + inputs[i]
			}
		}
		body, _ := json.Marshal(map[string]any{"inputs": inputs, "normalize": true, "truncate": true})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.URL, "/")+"/embed", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("embedding request invalid")
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("embedding service unavailable")
		}
		var vectors [][]float32
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			return nil, fmt.Errorf("embedding service status %d", res.StatusCode)
		}
		err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&vectors)
		res.Body.Close()
		if err != nil || len(vectors) != len(inputs) {
			return nil, fmt.Errorf("invalid embedding response")
		}
		for _, v := range vectors {
			if len(v) != Dimensions {
				return nil, fmt.Errorf("invalid embedding dimensions")
			}
			for _, x := range v {
				if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
					return nil, fmt.Errorf("invalid embedding value")
				}
			}
		}
		out = append(out, vectors...)
	}
	return out, nil
}

// Fake is deterministic and intended only for tests.
type Fake struct{}

func (Fake) Embed(ctx context.Context, texts []string, query bool) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([][]float32, len(texts))
	for i, s := range texts {
		v := make([]float32, Dimensions)
		var norm float64
		for j := range v {
			h := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", j, s)))
			v[j] = float32(int(h[0]) - 128)
			norm += float64(v[j] * v[j])
		}
		for j := range v {
			v[j] /= float32(math.Sqrt(norm))
		}
		out[i] = v
	}
	return out, nil
}
