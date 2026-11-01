package flow

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"lykeion/server/internal/board"
	"lykeion/server/internal/review"
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

// putExample matches a one-line `lyk record put KIND --content '<json>'` example.
var putExample = regexp.MustCompile(`lyk record put (\w+)(?: --cites \S+)? --content '([^']*)'`)

func TestItemsPromptPerColumn(t *testing.T) {
	kinds := map[string][]string{
		board.Grounding:       {"paper", "dataset", "no_qualifying"},
		board.Hypotheses:      {"hypothesis"},
		board.Experimentation: {"outputs"},
	}
	for col, want := range kinds {
		p := itemsPrompt(col)
		examples := map[string]bool{}
		for _, m := range putExample.FindAllStringSubmatch(p, -1) {
			if err := review.Validate(m[1], col, []byte(m[2])); err != nil {
				t.Errorf("%s: the example %s does not validate: %v", col, m[0], err)
			}
			examples[m[1]] = true
		}
		for _, k := range want {
			if !examples[k] {
				t.Errorf("%s: no one-line example of %s:\n%s", col, k, p)
			}
		}
		for _, role := range []string{"leader", "agent", "Curie"} {
			if !strings.Contains(Protocol(role, col, "", nil), p) {
				t.Errorf("%s on %s is not told how to declare items", role, col)
			}
		}
		if strings.Contains(Protocol("auditor", col, "", nil), p) {
			t.Errorf("the auditor on %s is told to declare items", col)
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
	if !strings.Contains(itemsPrompt(board.Hypotheses), "a Grounding record by its key") {
		t.Errorf("the Hypotheses prompt does not say a hypothesis may cite Grounding records")
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
