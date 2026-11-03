package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTEIEmbedsInBatchesOf32(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/embed" {
			t.Error(r.URL.Path)
		}
		var req struct {
			Inputs              []string
			Normalize, Truncate bool
		}
		json.NewDecoder(r.Body).Decode(&req)
		if len(req.Inputs) > 32 || !req.Normalize || !req.Truncate {
			t.Error(req)
		}
		vectors := make([][]float32, len(req.Inputs))
		for j, s := range req.Inputs {
			if !strings.HasPrefix(s, QueryPrefix) {
				t.Error(s)
			}
			vectors[j] = make([]float32, Dimensions)
		}
		json.NewEncoder(w).Encode(vectors)
	}))
	defer s.Close()
	inputs := make([]string, 70)
	v, err := (TEI{URL: s.URL}).Embed(context.Background(), inputs, true)
	if err != nil || len(v) != 70 || calls != 3 {
		t.Fatalf("vectors=%d calls=%d err=%v", len(v), calls, err)
	}
	if inputs[0] != "" {
		t.Fatal("mutated caller input")
	}
}
func TestTEIRejectsMalformedVectors(t *testing.T) {
	for _, body := range []string{`[]`, `[[1,2]]`, `not json`} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
			defer s.Close()
			if _, err := (TEI{URL: s.URL}).Embed(context.Background(), []string{"x"}, false); err == nil {
				t.Fatal("accepted malformed response")
			}
		})
	}
}
