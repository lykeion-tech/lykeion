package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"lykeion/server/internal/jev"
	"lykeion/server/internal/testutil"
)

// noulJev answers every noul question p and keeps each question it was asked.
func noulJev(t *testing.T, p float64) (*jev.Client, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Questions map[string]struct{ Instructions string }
		}
		json.NewDecoder(r.Body).Decode(&in)
		out := map[string]any{}
		mu.Lock()
		for id, q := range in.Questions {
			asked = append(asked, q.Instructions)
			out[id] = map[string]any{"type": "noul", "noul": p}
		}
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"model": jev.Model, "answers": out})
	}))
	t.Cleanup(srv.Close)
	return &jev.Client{BaseURL: srv.URL, Key: "k", Model: jev.Model}, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string{}, asked...)
	}
}

type toolsOut struct {
	Skills []struct {
		Name     string
		Sections []struct{ Heading, Text string }
		Folder   []byte
	}
	Tools []struct{ Server, Tool, Description string }
	Error string
}

func TestATeamRunRoutesOnlyToItsDeclaredItems(t *testing.T) {
	l := newTeamLabWith(t, testutil.PackOpts{Name: "hyp", Stage: "hypotheses", Members: 1, Catalogue: map[string][]string{"pi": {"uniprot"}}})
	j, asked := noulJev(t, 0.9)
	l.s.H.Engine.Jev = j
	leader := l.claim(t)
	var out toolsOut
	if c := l.s.call(t, "POST", "/api/role/tools", leader.token, "", map[string]any{"need": "map insulin's identifiers"}, &out); c != 200 {
		t.Fatalf("tools: %d %s", c, out.Error)
	}
	if len(out.Skills) != 1 || out.Skills[0].Name != "work" || len(out.Skills[0].Sections) != 1 || !strings.Contains(out.Skills[0].Sections[0].Text, "Lead the work.") {
		t.Fatalf("skills %+v, want the leader's own skill", out.Skills)
	}
	if len(out.Skills[0].Folder) == 0 {
		t.Fatal("a routed skill came without its folder")
	}
	if len(out.Tools) != 1 || out.Tools[0].Server != "uniprot" || out.Tools[0].Tool != "fetch" {
		t.Fatalf("tools %+v, want uniprot's fetch", out.Tools)
	}
	items := 0
	for _, q := range asked() {
		if strings.Contains(q, `skill "`) && !strings.Contains(q, `skill "work"`) {
			t.Fatalf("Jev was asked about a skill the Team does not declare: %s", q)
		}
		if strings.Contains(q, `connector "`) && !strings.Contains(q, `connector "uniprot"`) {
			t.Fatalf("Jev was asked about a connector the Team does not declare: %s", q)
		}
		if strings.HasPrefix(q, "Could the ") {
			items++
		}
	}
	if items != 2 {
		t.Fatalf("Jev judged %d items, want the Team's skill and connector", items)
	}
	if n := l.s.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'tools' AND role = 'server' AND body->>'need' = $2", l.card.ID, "map insulin's identifiers"); n != 1 {
		t.Fatalf("%d tools records", n)
	}
	if n := l.s.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1 AND kind = 'no_tool'", l.card.ID); n != 0 {
		t.Fatalf("%d no_tool items for a need that was met", n)
	}
}

func TestNothingQualifyingTellsTheResearcher(t *testing.T) {
	l := newTeamLabWith(t, testutil.PackOpts{Name: "hyp", Stage: "hypotheses", Members: 1, Catalogue: map[string][]string{"pi": {"uniprot"}}})
	j, _ := noulJev(t, 0.1)
	l.s.H.Engine.Jev = j
	leader := l.claim(t)
	var out toolsOut
	if c := l.s.call(t, "POST", "/api/role/tools", leader.token, "", map[string]any{"need": "run a cryo-EM reconstruction"}, &out); c != 200 {
		t.Fatalf("tools: %d %s", c, out.Error)
	}
	if len(out.Skills)+len(out.Tools) != 0 {
		t.Fatalf("routed %+v", out)
	}
	owner := "SELECT count(*) FROM inbox_item i JOIN card c ON c.id = i.card_id WHERE i.card_id = $1 AND i.kind = 'no_tool' AND i.user_id = c.owner_id AND i.text LIKE '%run a cryo-EM reconstruction%'"
	if n := l.s.count(t, owner, l.card.ID); n != 1 {
		t.Fatalf("%d no_tool items naming the need, want one", n)
	}
	// The same need again adds no second item while the first is open; a new need does.
	for _, need := range []string{"run a cryo-EM reconstruction", "sequence a genome"} {
		if c := l.s.call(t, "POST", "/api/role/tools", leader.token, "", map[string]any{"need": need}, &out); c != 200 {
			t.Fatalf("tools again: %d %s", c, out.Error)
		}
	}
	if n := l.s.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1 AND kind = 'no_tool'", l.card.ID); n != 2 {
		t.Fatalf("%d no_tool items after a repeated need and a new one, want two", n)
	}
	// A need that is only the end of an open one is a need of its own.
	if c := l.s.call(t, "POST", "/api/role/tools", leader.token, "", map[string]any{"need": "genome"}, &out); c != 200 {
		t.Fatalf("tools genome: %d %s", c, out.Error)
	}
	if n := l.s.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1 AND kind = 'no_tool'", l.card.ID); n != 3 {
		t.Fatalf("%d no_tool items after a need that ends an open one, want three", n)
	}
	// Jev being unreachable is not the same as nothing fitting.
	l.s.H.Engine.Jev = nil
	if c := l.s.call(t, "POST", "/api/role/tools", leader.token, "", map[string]any{"need": "run a cryo-EM reconstruction"}, &out); c != 503 {
		t.Fatalf("without Jev: %d", c)
	}
	if n := l.s.count(t, "SELECT count(*) FROM inbox_item WHERE card_id = $1 AND kind = 'no_tool'", l.card.ID); n != 3 {
		t.Fatalf("%d no_tool items after an outage, want still three", n)
	}
}
