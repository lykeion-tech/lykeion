package handler

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// finishedStudy makes a study in the first lab with one paper recorded and its state set to done.
func (s *server) finishedStudy(t *testing.T, tok, wsID string) string {
	t.Helper()
	c := s.seedCard(t, tok, wsID, "does x cause y?", nil, "grounding", "validation")
	err := s.H.Store.InWorkspace(t.Context(), uuid.MustParse(wsID), uuid.Nil, func(tx *store.Tx) error {
		b, _ := json.Marshal(map[string]any{"title": "A paper", "text": "x causes y"})
		_, err := tx.Q.InsertRecord(t.Context(), db.InsertRecordParams{WorkspaceID: tx.WS, CardID: uuid.MustParse(c.ID),
			BoardColumn: "grounding", Round: 1, Kind: "paper", Key: "p1", Role: "server", Body: b})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	s.mustUpdate(t, `UPDATE card SET state = 'done' WHERE id = $1`, c.ID)
	s.mustUpdate(t, `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text) SELECT workspace_id, id, owner_id, 'study_report', 'ready' FROM card WHERE id = $1`, c.ID)
	return c.ID
}

func TestReportOfFinishedStudy(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab-a")
	id := s.finishedStudy(t, a, w.ID)
	var rep struct{ Nodes []struct{ ID, Kind string } }
	if code := s.call(t, "GET", "/api/cards/"+id+"/report", a, w.ID, nil, &rep); code != 200 {
		t.Fatalf("code %d", code)
	}
	kinds := map[string]bool{}
	for _, n := range rep.Nodes {
		kinds[n.Kind] = true
	}
	if !kinds["question"] || !kinds["paper"] {
		t.Fatalf("nodes %+v", rep.Nodes)
	}
}

func TestReportOfUnfinishedWorkflowAndStoppedIs409(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab-a")
	mk := func(setup string) string {
		id := s.finishedStudy(t, a, w.ID)
		if setup != "" {
			s.mustUpdate(t, setup, id)
		}
		return id
	}
	for name, id := range map[string]string{
		"unfinished": mk(`UPDATE card SET state = 'working' WHERE id = $1`),
		"workflow":   mk(`UPDATE card SET workflow = 'mendota-ice-phenology' WHERE id = $1`),
		"stopped":    mk(`UPDATE card SET ending = 'stopped' WHERE id = $1`),
	} {
		var out map[string]any
		if code := s.call(t, "GET", "/api/cards/"+id+"/report", a, w.ID, nil, &out); code != 409 || out["error"] != "this study has no report" {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
}

func TestReportReadableByAnyMember(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab-a")
	b := s.member(t, a, w.ID, "b@lab.org", "member")
	id := s.finishedStudy(t, a, w.ID)
	if code := s.call(t, "GET", "/api/cards/"+id+"/report", b, w.ID, nil, nil); code != 200 {
		t.Fatalf("member: %d", code)
	}
}

func TestReportNeedsAStudyReportItem(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab-a")
	id := s.finishedStudy(t, a, w.ID)
	var card struct {
		HasReport bool `json:"has_report"`
	}
	if code := s.call(t, "GET", "/api/cards/"+id, a, w.ID, nil, &card); code != 200 || !card.HasReport {
		t.Fatalf("with a report: %d %+v", code, card)
	}
	// A study that finished before reports shipped has no study_report item.
	s.mustUpdate(t, `DELETE FROM inbox_item WHERE card_id = $1 AND kind = 'study_report'`, id)
	var out map[string]any
	if code := s.call(t, "GET", "/api/cards/"+id+"/report", a, w.ID, nil, &out); code != 409 || out["error"] != "this study has no report" {
		t.Fatalf("report: %d %v", code, out)
	}
	card.HasReport = true
	if code := s.call(t, "GET", "/api/cards/"+id, a, w.ID, nil, &card); code != 200 || card.HasReport {
		t.Fatalf("without a report: %d %+v", code, card)
	}
}

func TestReportSteeringLeavesOutManualDecisions(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab-a")
	id := s.finishedStudy(t, a, w.ID)
	s.mustUpdate(t, `INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, title, text, status, resolved_at, decided_at, decided_by)
		SELECT workspace_id, id, owner_id, 'manual_decision', 'Note', 'private note', 'decided', now(), now(), owner_id FROM card WHERE id = $1`, id)
	var rep struct{ Steering []map[string]any }
	if code := s.call(t, "GET", "/api/cards/"+id+"/report", a, w.ID, nil, &rep); code != 200 || len(rep.Steering) != 0 {
		t.Fatalf("steering: %d %+v", code, rep.Steering)
	}
}

func TestReportEndpointIncludesExtras(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	a := s.signIn(t, "a@lab.org")
	w := s.workspace(t, a, "lab-a")
	id := s.finishedStudy(t, a, w.ID)
	type out struct {
		Extras, Checks, Machine string
		Links                   []struct{ Start int }
		Nodes                   []struct{ ID, Kind string }
	}
	get := func() out {
		var rep out
		if code := s.call(t, "GET", "/api/cards/"+id+"/report", a, w.ID, nil, &rep); code != 200 {
			t.Fatalf("code %d", code)
		}
		return rep
	}
	if r := get(); r.Extras != "none" || r.Checks != "none" || r.Links == nil {
		t.Fatalf("no build: %+v", r)
	}
	s.mustUpdate(t, `INSERT INTO report_build (workspace_id, card_id, status, checks_status) SELECT workspace_id, id, 'done', 'done' FROM card WHERE id = $1`, id)
	s.mustUpdate(t, `INSERT INTO report_concept (workspace_id, card_id, key, label, type) SELECT workspace_id, id, 'x', 'X', 'chemical' FROM card WHERE id = $1`, id)
	r := get()
	if r.Extras != "ready" || r.Checks != "ready" {
		t.Fatalf("done: %+v", r)
	}
	found := false
	for _, n := range r.Nodes {
		found = found || n.ID == "concept:x" && n.Kind == "concept"
	}
	if !found {
		t.Fatalf("no concept node: %+v", r.Nodes)
	}
}
