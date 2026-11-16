package handler

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"lykeion/server/internal/flow"
	"lykeion/server/internal/report"
	"lykeion/server/internal/sources"
	db "lykeion/server/pkg/db/generated"
)

type titleSources struct {
	mu    sync.Mutex
	calls map[string]int
}

func (s *titleSources) Paper(_ context.Context, id string) (sources.Hit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[id]++
	if id == "pmid:2" {
		return sources.Hit{Title: "Looked up title"}, nil
	}
	return sources.Hit{}, errors.New("unavailable")
}
func (*titleSources) Dataset(context.Context, string) (sources.Hit, error)  { return sources.Hit{}, nil }
func (*titleSources) Search(context.Context, string) ([]sources.Hit, error) { return nil, nil }

func TestReportPaperTitles(t *testing.T) {
	source := &titleSources{calls: map[string]int{}}
	h := H{Engine: &flow.Engine{Sources: source}}
	rep := report.Report{Nodes: []report.Node{{ID: "rec:p1", Kind: "paper", Label: "pmid:1"}, {ID: "rec:p2", Kind: "paper", Label: "pmid:2"}, {ID: "rec:p3", Kind: "paper", Label: "pmid:2"}, {ID: "rec:p4", Kind: "paper", Label: "pmid:3"}}}
	recs := []db.Record{
		{Key: "p1", Kind: "paper", Body: json.RawMessage(`{"id":"pmid:1","cites":["call:c1"]}`)},
		{Key: "p2", Kind: "paper", Body: json.RawMessage(`{"id":"pmid:2"}`)},
		{Key: "p3", Kind: "paper", Body: json.RawMessage(`{"id":"pmid:2"}`)},
		{Key: "p4", Kind: "paper", Body: json.RawMessage(`{"id":"pmid:3","cites":["call:c1"]}`)},
		{Key: "c1", Kind: "call", Body: json.RawMessage(`{"answer":"{\"results\":[{\"pmid\":\"1\",\"title\":\"Recorded title\"},{\"pmid\":\"30\",\"title\":\"Wrong paper\"}]}"}`)},
	}
	h.resolveReportPaperTitles(context.Background(), &rep, recs)
	want := []string{"Recorded title", "Looked up title", "Looked up title", "pmid:3"}
	for i, n := range rep.Nodes {
		if n.Label != want[i] || n.PaperID == "" {
			t.Errorf("node %d: %+v", i, n)
		}
	}
	if source.calls["pmid:1"] != 0 || source.calls["pmid:2"] != 1 || source.calls["pmid:3"] != 1 {
		t.Fatalf("lookups: %v", source.calls)
	}
}
