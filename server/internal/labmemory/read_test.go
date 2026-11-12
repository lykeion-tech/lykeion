package labmemory

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/report"
	"lykeion/server/internal/store"
)

func TestParseRefRoundTrip(t *testing.T) {
	for _, kind := range []string{"study", "record", "comment", "decision", "lesson", "workspace"} {
		s := kind + ":" + uuid.NewString()
		r, err := ParseRef(s)
		if err != nil || r.String() != s {
			t.Fatalf("%s: %v %v", s, r, err)
		}
	}
	r, err := ParseRef("workspace:context")
	if err != nil || r.String() != "workspace:context" {
		t.Fatalf("sentinel: %v %v", r, err)
	}
	for _, s := range []string{"coresearch:" + uuid.NewString(), "record:", "study:not-a-uuid", "study:" + uuid.Nil.String()} {
		if _, err := ParseRef(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}

func TestParseReportRefWithNode(t *testing.T) {
	id := uuid.New()
	for _, node := range []string{"", "rec:H1", "concept:brca1", "doubt:validation:f1"} {
		s := "report:" + id.String()
		if node != "" {
			s += "#" + node
		}
		r, err := ParseRef(s)
		if err != nil || r.Kind != "report" || r.ID != id || r.Node != node || r.String() != s {
			t.Fatalf("%s: %+v %v", s, r, err)
		}
	}
	for _, s := range []string{"report:" + id.String() + "#", "report:not-a-uuid#rec:H1"} {
		if _, err := ParseRef(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}

func TestParseRejectsFragmentOnOtherKinds(t *testing.T) {
	id := uuid.NewString()
	for _, kind := range []string{"study", "record", "comment", "decision", "lesson"} {
		if _, err := ParseRef(kind + ":" + id + "#rec:H1"); err == nil {
			t.Fatalf("%s accepted a fragment", kind)
		}
	}
	if _, err := ParseRef("workspace:context#rec:H1"); err == nil {
		t.Fatal("workspace accepted a fragment")
	}
}

// finishReport makes the fixture's study a finished study with a report, one
// judged concept and one fact Jev accepted.
func (f *fixture) finishReport() {
	f.exec(`UPDATE card SET state='done' WHERE id=$1`, f.study)
	f.exec(`INSERT INTO inbox_item(workspace_id,card_id,user_id,kind,text) VALUES($1,$2,$3,'study_report','ready')`, f.ws, f.study, f.user)
	f.exec(`INSERT INTO report_build(workspace_id,card_id,status,checks_status) VALUES($1,$2,'done','done')`, f.ws, f.study)
	f.exec(`INSERT INTO report_concept(workspace_id,card_id,key,label,type) VALUES($1,$2,'brca1','BRCA1','gene'),($1,$2,'cancer','Breast cancer','disease')`, f.ws, f.study)
	f.exec(`INSERT INTO report_fact(workspace_id,card_id,subject,relation,object,quote,source_node,p) VALUES($1,$2,'brca1','increases','cancer','BRCA1 loss raises risk','question',0.93)`, f.ws, f.study)
}

func TestReadReportRefRequiresStudyAccess(t *testing.T) {
	f := setup(t)
	r := Ref{Kind: "report", ID: f.study}
	if _, err := f.read(r); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unfinished study: %v", err)
	}
	f.finishReport()
	if _, err := f.read(r); err != nil {
		t.Fatal(err)
	}
	if _, err := f.read(Ref{Kind: "report", ID: f.study, Node: "concept:nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing node: %v", err)
	}
	f.exec(`UPDATE card SET ending='stopped' WHERE id=$1`, f.study)
	if _, err := f.read(r); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stopped study: %v", err)
	}
	f.exec(`UPDATE card SET ending='' WHERE id=$1`, f.study)
	f.exec(`DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, f.ws, f.user)
	if _, err := f.read(r); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member: %v", err)
	}
}

func TestReadReportRefIncludesJudgedFacts(t *testing.T) {
	f := setup(t)
	f.finishReport()
	fact, err := f.read(Ref{Kind: "report", ID: f.study})
	if err != nil {
		t.Fatal(err)
	}
	if fact.Title != "Report of study #1" || len(fact.Links) != 1 || fact.Links[0] != (Ref{Kind: "study", ID: f.study}) {
		t.Fatalf("%+v", fact)
	}
	node, err := f.read(Ref{Kind: "report", ID: f.study, Node: "concept:brca1"})
	if err != nil || node.Title != "Report of study #1 › BRCA1" {
		t.Fatalf("%+v %v", node, err)
	}
	tf := newTools(t, f, nil)
	out := tf.call("read", map[string]any{"ref": "report:" + f.study.String()}, false)
	var content []struct{ Text string }
	if err := json.Unmarshal([]byte(out), &content); err != nil || len(content) != 1 {
		t.Fatal(out, err)
	}
	var e struct{ Text string }
	if err := json.Unmarshal([]byte(content[0].Text), &e); err != nil {
		t.Fatal(err)
	}
	var g struct {
		Nodes []struct {
			ID, Kind, Label, Status, Quote string
			Facts                          []struct {
				Relation, Object, Quote string
				P                       float64
			}
		}
		Edges []struct{ From, To, Kind string }
	}
	if err := json.Unmarshal([]byte(e.Text), &g); err != nil {
		t.Fatal(e.Text, err)
	}
	var concept, question bool
	for _, n := range g.Nodes {
		switch n.ID {
		case "question":
			question = n.Kind == "question" && strings.Contains(n.Quote, "untrusted data") && strings.Contains(n.Quote, "Question")
		case "concept:brca1":
			concept = n.Label == "BRCA1" && n.Status == "gene" && len(n.Facts) == 1 && n.Facts[0].P == 0.93 &&
				n.Facts[0].Relation == "increases" && strings.Contains(n.Facts[0].Quote, "untrusted data") && strings.Contains(n.Facts[0].Quote, "BRCA1 loss raises risk")
		}
	}
	edge := slices.Contains(g.Edges, struct{ From, To, Kind string }{"concept:brca1", "concept:cancer", "increases"})
	if !concept || !question || !edge {
		t.Fatalf("%s", e.Text)
	}
}

func TestReportPageKeepsToOnePage(t *testing.T) {
	g := reportGraph{Edges: []report.Edge{{From: "a", To: "b", Kind: "uses"}}}
	for i := range 10 {
		g.Nodes = append(g.Nodes, reportNode{ID: fmt.Sprint("rec:", i), Kind: "paper", Quote: strings.Repeat("x", 30<<10),
			Facts: []report.Fact{{Quote: strings.Repeat("y", 500)}}})
	}
	kept := onePage(g)
	if !kept.Truncated || len(kept.Nodes) == 0 || len(kept.Nodes) == 10 || len(kept.Edges) != 0 || strings.Contains(kept.Nodes[0].Quote, "untrusted") {
		t.Fatalf("%v %d %d", kept.Truncated, len(kept.Nodes), len(kept.Edges))
	}
	text, _ := json.Marshal(kept)
	page, err := reportPage(string(text))
	if err != nil || len(page) > pageSize || !strings.Contains(page, "untrusted data") {
		t.Fatal(len(page), err)
	}
}

func TestReportIsNeverMaintenanceOrLessonEvidence(t *testing.T) {
	f := setup(t)
	f.finishReport()
	r := Ref{Kind: "report", ID: f.study}
	fact, err := f.read(r)
	if err != nil {
		t.Fatal(err)
	}
	err = (&store.Store{Pool: f.env.App, LessonsEnabled: true}).Maintenance(t.Context(), func(tx *store.Tx) error {
		if e := tx.Workspace(t.Context(), f.ws); e != nil {
			return e
		}
		if _, e := ReadMaintenance(t.Context(), tx, r); !errors.Is(e, ErrNotFound) {
			t.Fatalf("maintenance read: %v", e)
		}
		if _, e := Version(t.Context(), tx, r); !errors.Is(e, ErrNotFound) {
			t.Fatalf("maintenance version: %v", e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	cites, _ := json.Marshal([]Citation{{Ref: r.String(), Version: fact.Version}})
	f.exec(`INSERT INTO lesson(id,workspace_id,kind,text,cites,model,checked_at) VALUES($1,$2,'lab','Lesson',$3,'test',now())`, id, f.ws, cites)
	if _, err := f.read(Ref{Kind: "lesson", ID: id}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("lesson citing a report: %v", err)
	}
}
