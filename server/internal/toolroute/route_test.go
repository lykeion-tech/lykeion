package toolroute

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"lykeion/server/internal/jev"
	"lykeion/server/internal/science"
)

// answer is one entry of the stub's fixed map: a question whose instructions
// contain sub is answered p. The first matching entry wins; anything else is 0.
type answer struct {
	sub string
	p   float64
}

type stub struct {
	mu       sync.Mutex
	requests [][]string // each request's questions
}

func (s *stub) asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, r := range s.requests {
		out = append(out, r...)
	}
	return out
}

// jevStub answers noul questions from a fixed map.
func jevStub(t *testing.T, answers []answer) (*jev.Client, *stub) {
	t.Helper()
	s := &stub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Questions map[string]struct {
				Type, Instructions string
			}
		}
		json.NewDecoder(r.Body).Decode(&in)
		out := map[string]any{}
		var qs []string
		for id, q := range in.Questions {
			if q.Type != "noul" {
				t.Errorf("question type %q", q.Type)
			}
			qs = append(qs, q.Instructions)
			p := 0.0
			for _, a := range answers {
				if strings.Contains(q.Instructions, a.sub) {
					p = a.p
					break
				}
			}
			out[id] = map[string]any{"type": "noul", "noul": p}
		}
		s.mu.Lock()
		s.requests = append(s.requests, qs)
		s.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"model": jev.Model, "answers": out})
	}))
	t.Cleanup(srv.Close)
	return &jev.Client{BaseURL: srv.URL, Key: "k", Model: jev.Model}, s
}

func skill(name, domain string, sections ...string) Skill {
	s := Skill{Name: name, Domain: domain, Description: name + " things"}
	for _, h := range sections {
		s.Sections = append(s.Sections, science.Section{Heading: h, Text: h + " text"})
	}
	return s
}

func names(r Result) []string {
	var out []string
	for _, s := range r.Skills {
		out = append(out, s.Name)
	}
	for _, t := range r.Tools {
		out = append(out, t.Server+"/"+t.Tool)
	}
	return out
}

func TestRouteKeepsEveryItemAboveTheThreshold(t *testing.T) {
	j, _ := jevStub(t, []answer{{`section "Use"`, 0.9}, {`"alpha"`, 0.9}, {`"beta"`, 0.6}, {`"gamma"`, 0.4}, {`"Chemistry"`, 0.9}})
	c := Candidates{Skills: []Skill{skill("alpha", "Chemistry", "Use"), skill("beta", "Chemistry", "Use"), skill("gamma", "Chemistry", "Use")}}
	r, err := Route(context.Background(), j, "dock a ligand", c)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(r); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Fatalf("routed %v, want alpha and beta", got)
	}
}

func TestRouteSkipsItemsOfARejectedDomain(t *testing.T) {
	j, s := jevStub(t, []answer{{`section "Use"`, 0.9}, {`tool "lookup"`, 0.9}, {`"Chemistry"`, 0.9}, {`"Genomics"`, 0.2}, {`"alpha"`, 0.9}, {`"genes"`, 0.9}, {`"blast"`, 0.9}})
	c := Candidates{
		Skills:     []Skill{skill("alpha", "Chemistry", "Use"), skill("blast", "Genomics", "Use")},
		Connectors: []Connector{{Name: "genes", Domain: "Genomics", Description: "gene lookups", Tools: []Tool{{Name: "lookup"}}}},
	}
	r, err := Route(context.Background(), j, "dock a ligand", c)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(r); !slices.Equal(got, []string{"alpha"}) {
		t.Fatalf("routed %v, want alpha only", got)
	}
	for _, q := range s.asked() {
		if strings.Contains(q, `"blast"`) || strings.Contains(q, `"genes"`) {
			t.Fatalf("Jev was asked about an item of a rejected domain: %s", q)
		}
	}
}

func TestRouteReturnsOnlyQualifyingSections(t *testing.T) {
	j, _ := jevStub(t, []answer{{`section "Install"`, 0.2}, {`section "Usage"`, 0.8}, {`tool "search"`, 0.7}, {`tool "delete"`, 0.1}, {`"Chemistry"`, 0.9}, {`"rdkit"`, 0.9}, {`"chembl"`, 0.9}})
	c := Candidates{
		Skills:     []Skill{skill("rdkit", "Chemistry", "Install", "Usage")},
		Connectors: []Connector{{Name: "chembl", Domain: "Chemistry", Description: "bioactivity", Tools: []Tool{{Name: "search", Description: "find molecules"}, {Name: "delete"}}}},
	}
	r, err := Route(context.Background(), j, "find kinase inhibitors", c)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Skills) != 1 || len(r.Skills[0].Sections) != 1 || r.Skills[0].Sections[0].Heading != "Usage" {
		t.Fatalf("skills %+v, want rdkit with Usage only", r.Skills)
	}
	if len(r.Tools) != 1 || r.Tools[0] != (RoutedTool{Server: "chembl", Tool: "search", Description: "find molecules"}) {
		t.Fatalf("tools %+v, want chembl search only", r.Tools)
	}
}

func TestRouteSplitsALevelAt200Questions(t *testing.T) {
	j, s := jevStub(t, []answer{{`"Chemistry"`, 0.9}})
	var c Candidates
	for i := range 450 {
		c.Skills = append(c.Skills, skill("s"+strings.Repeat("x", i%3)+string(rune('a'+i%26)), "Chemistry", "Use"))
	}
	if _, err := Route(context.Background(), j, "anything", c); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// one domain question, then 450 item questions in three requests
	if len(s.requests) != 4 {
		t.Fatalf("%d requests, want 4", len(s.requests))
	}
	for _, r := range s.requests {
		if len(r) > 200 {
			t.Fatalf("a request asked %d questions", len(r))
		}
	}
}

func TestRouteWithoutJevIsAnError(t *testing.T) {
	_, err := Route(context.Background(), nil, "anything", Candidates{Skills: []Skill{skill("a", "Chemistry", "Use")}})
	if err == nil {
		t.Fatal("routing without Jev succeeded")
	}
}
