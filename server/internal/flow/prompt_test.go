package flow

import (
	"strings"
	"testing"

	db "lykeion/server/pkg/db/generated"
)

func TestThePromptListsTheFiles(t *testing.T) {
	c := db.Card{Number: 1, BoardColumn: "grounding", Question: "q?"}
	files := []db.Attachment{{Name: "../Data.csv", DiskName: "data.csv", RecordKey: "c1"}, {Name: "notes.md", DiskName: "notes.md", RecordKey: "c2"}}
	p := prompt(c, "agent", nil, "", files, nil)
	if !strings.Contains(p, "Attached files (read-only) are in ./attachments: data.csv (cite call:c1), notes.md (cite call:c2)\n") {
		t.Fatalf("the prompt doesn't list the files:\n%s", p)
	}
	if strings.Contains(p, "../Data.csv") {
		t.Fatalf("the prompt names a file by its display name, not its name on disk:\n%s", p)
	}
	if p := prompt(c, "agent", nil, "", nil, nil); strings.Contains(p, "attachments") {
		t.Fatalf("a study without files mentions attachments:\n%s", p)
	}
}

func TestExperimentationRunsAreToldToSaveOutputs(t *testing.T) {
	const put = "lyk output put"
	const install = "Install any package you need into the study's folder"
	for _, role := range []string{"leader", "agent", "Curie"} {
		p := Protocol(role, "experimentation", "", nil)
		if !strings.Contains(p, put) {
			t.Errorf("%s on experimentation is not told to save outputs:\n%s", role, p)
		}
		if !strings.Contains(p, install) {
			t.Errorf("%s on experimentation is not told it may install packages:\n%s", role, p)
		}
	}
	if p := Protocol("auditor", "experimentation", "", nil); strings.Contains(p, put) {
		t.Errorf("the auditor is told to save outputs:\n%s", p)
	}
	for _, col := range []string{"hypotheses", "validation"} {
		for _, role := range []string{"leader", "agent", "auditor", "Curie"} {
			if p := Protocol(role, col, "", nil); strings.Contains(p, put) {
				t.Errorf("%s on %s is told to save outputs:\n%s", role, col, p)
			}
		}
	}
}

const twoPreds = `[{"name":"effect","text":"effect > 0.2"},{"name":"cap","text":"loss < 0.5"}]`

func TestExperimentationRunsAreToldThePredicatesAndTheResultsRule(t *testing.T) {
	c := db.Card{Number: 1, BoardColumn: "experimentation", Question: "q?", Predicates: []byte(twoPreds)}
	p := prompt(c, "agent", nil, "", nil, nil)
	for _, want := range []string{"effect", "effect > 0.2", "cap", "loss < 0.5", "$LYK_RESULTS", `{"results_run": "run:x<N>"}`} {
		if !strings.Contains(p, want) {
			t.Errorf("no %q in:\n%s", want, p)
		}
	}
}

func TestOtherColumnsSeeNoPredicatesBeforeTheyAreDecided(t *testing.T) {
	c := db.Card{Number: 1, BoardColumn: "hypotheses", Question: "q?", Predicates: []byte(twoPreds)}
	p := prompt(c, "agent", nil, "", nil, nil)
	for _, no := range []string{"effect > 0.2", "loss < 0.5", "LYK_RESULTS", "results_run"} {
		if strings.Contains(p, no) {
			t.Errorf("%q in:\n%s", no, p)
		}
	}
}

func TestLaterColumnsGetTheOutcomesAsFixedFacts(t *testing.T) {
	c := db.Card{Number: 1, BoardColumn: "reporting", Question: "q?", Predicates: []byte(twoPreds)}
	decided := []Decision{
		{Name: "effect", Text: "effect > 0.2", Outcome: "missed", Round: 1, Run: "x3", Values: map[string]float64{"effect": 0.1}},
		{Name: "cap", Text: "loss < 0.5", Outcome: "unconfirmed", Reason: "no results run named"},
	}
	p := prompt(c, "agent", nil, "", nil, decided)
	for _, want := range []string{"effect", "missed", "effect=0.1", "x3", "report them as they are", "not confirmed: no results run named"} {
		if !strings.Contains(p, want) {
			t.Errorf("no %q in:\n%s", want, p)
		}
	}
	if strings.Contains(prompt(db.Card{BoardColumn: "experimentation"}, "agent", nil, "", nil, decided), "missed") {
		t.Error("Experimentation was told the outcomes")
	}
}
