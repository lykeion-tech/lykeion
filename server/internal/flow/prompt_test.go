package flow

import (
	"encoding/json"
	"strings"
	"testing"

	"lykeion/server/internal/board"
	"lykeion/server/internal/handin"
	db "lykeion/server/pkg/db/generated"
)

func TestThePromptListsTheFiles(t *testing.T) {
	c := db.Card{Number: 1, BoardColumn: "grounding", Question: "q?"}
	files := []db.Attachment{{Name: "../Data.csv", DiskName: "data.csv", RecordKey: "c1"}, {Name: "notes.md", DiskName: "notes.md", RecordKey: "c2"}}
	p := prompt(c, "agent", nil, "", files)
	if !strings.Contains(p, "Attached files (read-only) are in ./attachments: data.csv, notes.md\n") {
		t.Fatalf("the prompt doesn't list the files:\n%s", p)
	}
	c.BoardColumn = board.Validation
	if p := prompt(c, "agent", nil, "", files); !strings.Contains(p, "Attached files (read-only) are in ./attachments: data.csv (cite call:c1), notes.md (cite call:c2)\n") {
		t.Fatalf("a Validation prompt doesn't list the files with their cites:\n%s", p)
	}
	if strings.Contains(p, "../Data.csv") {
		t.Fatalf("the prompt names a file by its display name, not its name on disk:\n%s", p)
	}
	if p := prompt(c, "agent", nil, "", nil); strings.Contains(p, "attachments") {
		t.Fatalf("a study without files mentions attachments:\n%s", p)
	}
}

func TestNoPromptNamesAReportingStage(t *testing.T) {
	for _, col := range board.Stages {
		for _, role := range []string{"agent", "leader", "member", "auditor"} {
			p := prompt(db.Card{Number: 1, BoardColumn: col, Question: "q?"}, role, nil, "", nil)
			if strings.Contains(p, "Reporting") || strings.Contains(p, "manuscript") {
				t.Errorf("%s/%s:\n%s", col, role, p)
			}
		}
	}
}

func TestExperimentationRunsAreToldToSaveOutputs(t *testing.T) {
	const put = "outputs/"
	const install = "Install any package you need into the study's folder"
	const python = "run_script"
	for _, role := range []string{"leader", "agent", "Curie"} {
		p := Protocol(role, "experimentation", "", nil)
		for _, want := range []string{put, install, python, "read_output"} {
			if !strings.Contains(p, want) {
				t.Errorf("%s on experimentation is not told %q:\n%s", role, want, p)
			}
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

// Every field of a column's hand-in is named in the protocol its lead and
// members read; the auditor reads its own.
func TestItemsPromptPerColumn(t *testing.T) {
	for _, col := range []string{board.Grounding, board.Hypotheses, board.Experimentation} {
		k, _ := handin.KindFor("", "agent", col)
		p := handInIntro + itemsPrompt(col)
		for _, f := range schemaFields(t, k) {
			if !strings.Contains(p, f) {
				t.Errorf("%s: the hand-in field %s is not described:\n%s", col, f, p)
			}
		}
		for _, role := range []string{"leader", "agent", "Curie"} {
			if !strings.Contains(Protocol(role, col, "", nil), p) {
				t.Errorf("%s on %s is not told the hand-in's fields", role, col)
			}
		}
		if strings.Contains(Protocol("auditor", col, "", nil), p) {
			t.Errorf("the auditor on %s is told the lead's hand-in fields", col)
		}
		a := Protocol("auditor", col, "", nil)
		for _, f := range schemaFields(t, handin.Auditor) {
			if !strings.Contains(a, f) {
				t.Errorf("the auditor on %s is not told the field %s:\n%s", col, f, a)
			}
		}
		if !strings.Contains(p, "same id or text") || !strings.Contains(p, "so also withdraw the old one") {
			t.Errorf("the %s prompt does not say how a sent-back item is answered", col)
		}
	}
	for _, s := range []string{"retracted", "no_qualifying", "count against"} {
		if !strings.Contains(itemsPrompt(board.Grounding), s) {
			t.Errorf("the Grounding prompt does not say %q", s)
		}
	}
	if !strings.Contains(itemsPrompt(board.Hypotheses), "never look up this study's own answer") {
		t.Errorf("the Hypotheses prompt does not forbid looking up the answer")
	}
	if !strings.Contains(itemsPrompt(board.Hypotheses), "Grounding papers and datasets") {
		t.Errorf("the Hypotheses prompt does not say a hypothesis may cite Grounding papers")
	}
	if !strings.Contains(itemsPrompt(board.Experimentation), "run_script") {
		t.Errorf("the Experimentation prompt does not tie numbers to run_script runs")
	}
	if p := itemsPrompt(board.Validation); p != "" {
		t.Errorf("Validation declares no items yet, got %q", p)
	}

	crit := []string{"Prospective cohort", "Adult ICU patients"}
	raw, _ := json.Marshal(crit)
	c := db.Card{Number: 1, BoardColumn: board.Grounding, Question: "q?", GroundingCriteria: raw}
	want := "Grounding criteria (every source must meet each):\n- Prospective cohort\n- Adult ICU patients\n"
	if p := prompt(c, "agent", nil, "", nil); !strings.Contains(p, want) {
		t.Errorf("the Grounding prompt does not list the criteria verbatim:\n%s", p)
	}
	c.BoardColumn = board.Hypotheses
	if p := prompt(c, "agent", nil, "", nil); strings.Contains(p, "Grounding criteria") {
		t.Errorf("a Hypotheses prompt lists the Grounding criteria:\n%s", p)
	}
}

// schemaFields are the top-level fields of kind k's hand-in schema.
func schemaFields(t *testing.T, k handin.Kind) []string {
	t.Helper()
	var s struct{ Required []string }
	if err := json.Unmarshal([]byte(handin.Schema(k)), &s); err != nil || len(s.Required) == 0 {
		t.Fatalf("%s schema: %v", k, err)
	}
	return s.Required
}

func TestAuditorProtocolNoLongerFeedsJev(t *testing.T) {
	for _, col := range board.Stages {
		p := Protocol("auditor", col, "", nil)
		if strings.Contains(p, "go to Jev") || strings.Contains(p, "Jev scores") {
			t.Errorf("the auditor on %s is told its findings reach Jev:\n%s", col, p)
		}
		if !strings.Contains(p, "never reach Jev") {
			t.Errorf("the auditor on %s is not told its findings stay out of Jev's review:\n%s", col, p)
		}
	}
}

// Claude asks before shell redirection writes a file, and nobody answers an
// unattended run, so every run is told to write files with its file tools.
// Runs may be Claude or Codex, so no CLI's tool is named.
func TestThePromptSaysHowToWriteFiles(t *testing.T) {
	for _, col := range board.Stages {
		p := prompt(db.Card{Number: 1, BoardColumn: col, Question: "q?"}, "agent", nil, "", nil)
		if !strings.Contains(p, "Write scratch files with your file tools, under $TMPDIR or the run folder, not with shell redirection.\n") || strings.Contains(p, "(Write)") {
			t.Fatalf("the prompt doesn't say how to write files:\n%s", p)
		}
	}
}
