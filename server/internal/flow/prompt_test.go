package flow

import (
	"strings"
	"testing"

	db "lykeion/server/pkg/db/generated"
)

func TestThePromptListsTheFiles(t *testing.T) {
	c := db.Card{Number: 1, BoardColumn: "grounding", Question: "q?"}
	files := []db.Attachment{{Name: "../Data.csv", DiskName: "data.csv", RecordKey: "c1"}, {Name: "notes.md", DiskName: "notes.md", RecordKey: "c2"}}
	p := prompt(c, "agent", nil, "", files)
	if !strings.Contains(p, "Attached files (read-only) are in ./attachments: data.csv (cite call:c1), notes.md (cite call:c2)\n") {
		t.Fatalf("the prompt doesn't list the files:\n%s", p)
	}
	if strings.Contains(p, "../Data.csv") {
		t.Fatalf("the prompt names a file by its display name, not its name on disk:\n%s", p)
	}
	if p := prompt(c, "agent", nil, "", nil); strings.Contains(p, "attachments") {
		t.Fatalf("a study without files mentions attachments:\n%s", p)
	}
}

func TestExperimentationRunsAreToldToSaveOutputs(t *testing.T) {
	const put = "lyk output put"
	for _, role := range []string{"leader", "agent", "Curie"} {
		if p := Protocol(role, "experimentation", "", nil); !strings.Contains(p, put) {
			t.Errorf("%s on experimentation is not told to save outputs:\n%s", role, p)
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
