package flow

import (
	"encoding/json"
	"strings"
	"testing"

	"lykeion/server/internal/board"
	"lykeion/server/internal/handin"
	"lykeion/server/internal/pack"
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
	if p = prompt(c, "agent", nil, "", files); !strings.Contains(p, "Attached files (read-only) are in ./attachments: data.csv, notes.md\n") {
		t.Fatalf("a Validation prompt doesn't list the files as the other columns do:\n%s", p)
	}
	if strings.Contains(p, "../Data.csv") {
		t.Fatalf("the prompt names a file by its display name, not its name on disk:\n%s", p)
	}
	if p := prompt(c, "agent", nil, "", nil); strings.Contains(p, "attachments") {
		t.Fatalf("a study without files mentions attachments:\n%s", p)
	}
	c.BoardColumn = board.Hypotheses
	if p := prompt(c, "agent", nil, "", files); !strings.Contains(p, `"data.csv" (cite as "../Data.csv")`) {
		t.Fatalf("the Hypotheses prompt must distinguish the local path from the citation name:\n%s", p)
	}
}

func TestStageReportTemplatesForEveryStudyRole(t *testing.T) {
	stages := map[string]string{
		board.Grounding:       "research question and scope",
		board.Hypotheses:      "testable prediction",
		board.Experimentation: "data and method",
		board.Validation:      "assessment or verdict",
	}
	for column, guidance := range stages {
		for role, want := range map[string]string{"leader": guidance, "agent": guidance, "member": "carry forward", "auditor": "audit conclusion"} {
			template := DefaultReportTemplate(role, column)
			if !strings.Contains(template, want) || !strings.Contains(template, "never invent evidence") || strings.Contains(template, "##") {
				t.Errorf("%s/%s has the wrong reporting instructions: %s", column, role, template)
			}
			if !strings.Contains(Protocol(role, column, column, nil), template) {
				t.Errorf("%s/%s Settings template differs from the run protocol", column, role)
			}
		}
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
	for _, col := range []string{"grounding", "hypotheses", "validation"} {
		for _, role := range []string{"leader", "agent", "Curie"} {
			if p := Protocol(role, col, "", nil); !strings.Contains(p, put) || !strings.Contains(p, "Never use a path in another run's folder") || !strings.Contains(p, "list_outputs") || strings.Contains(p, install) {
				t.Errorf("%s on %s is not told how files are kept, or is told to install:\n%s", role, col, p)
			}
		}
	}
}

// Every hand-in role, the auditor included, is told the sources server's
// tools by name, since find_tools lists only other connectors; the auditor
// never passes an item it could not check.
func TestHandInRolesAreToldTheSourcesTools(t *testing.T) {
	for _, col := range []string{board.Grounding, board.Hypotheses, board.Experimentation} {
		for _, role := range []string{"leader", "agent", "auditor", "Curie"} {
			p := Protocol(role, col, "", nil)
			for _, want := range []string{`"server": "sources"`, "search_literature", "fetch_text", "search_datasets", "open_dataset"} {
				if !strings.Contains(p, want) {
					t.Errorf("%s on %s is not told %q", role, col, want)
				}
			}
		}
		if p := Protocol("auditor", col, "", nil); !strings.Contains(p, "An item you could not check against a source is a concern, never a pass.") {
			t.Errorf("the auditor on %s may pass an unchecked item:\n%s", col, p)
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
	if !strings.Contains(itemsPrompt(board.Hypotheses), "cite its original filename") {
		t.Errorf("the Hypotheses prompt does not say how to cite an attachment")
	}
	if !strings.Contains(itemsPrompt(board.Experimentation), "At least one is required") {
		t.Errorf("the Experimentation prompt does not ask for an output file")
	}
	if !strings.Contains(itemsPrompt(board.Experimentation), "run_script") {
		t.Errorf("the Experimentation prompt does not tie numbers to run_script runs")
	}
	for _, want := range []string{"`assessments`", "`verdicts`", "`flagged_errors`", "`replies`", "you set no status"} {
		if !strings.Contains(itemsPrompt(board.Validation), want) {
			t.Errorf("the Validation prompt does not name %s", want)
		}
	}

	c := db.Card{Number: 1, BoardColumn: board.Grounding, Question: "q?", GroundingCriteria: []byte(`["historical"]`)}
	if p := prompt(c, "agent", nil, "", nil); strings.Contains(p, "historical") || strings.Contains(p, "Grounding criteria") {
		t.Errorf("removed criteria leaked into prompt: %s", p)
	}
	if strings.Contains(itemsPrompt(board.Grounding), "criteri") {
		t.Errorf("the Grounding hand-in still cites removed criteria")
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

func TestHypothesesAuditorChecksEstimatesAsPlans(t *testing.T) {
	p := Protocol("auditor", board.Hypotheses, board.Grounding, nil)
	for _, want := range []string{"an estimate's quantity, measure, and reproducible method", "Require a falsifier only for a prediction", "cite a study attachment by its original filename"} {
		if !strings.Contains(p, want) {
			t.Errorf("audit protocol lacks %q", want)
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

func TestTheValidationHandInNamesValuesAsTheReviewReadsThem(t *testing.T) {
	p := itemsPrompt(board.Validation)
	for _, want := range []string{
		"null when `measurement` is already the effect",
		"`df2` (null when the test has none)",
		"the name of the recorded value whose text is the outcome seen",
		"a test no run printed makes the hypothesis invalid",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("the Validation hand-in does not say %q:\n%s", want, p)
		}
	}
	var s struct {
		Properties struct {
			Assessments struct {
				Items struct {
					Properties map[string]struct {
						Description string
					}
				}
			}
		}
	}
	if err := json.Unmarshal([]byte(handin.Schema(handin.Validation)), &s); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"measurement", "control", "test", "observed"} {
		if s.Properties.Assessments.Items.Properties[f].Description == "" {
			t.Errorf("the schema does not describe %s", f)
		}
	}
}

// Leads and members are told which questions reach the researcher, that
// delegated methods and tools stay with the agent, and that a member's
// question goes through its leader.
func TestTheProtocolSaysWhenToAsk(t *testing.T) {
	for _, role := range []string{"leader", "agent", "Curie"} {
		p := Protocol(role, board.Hypotheses, "", nil)
		for _, want := range []string{
			"`scope`", "`change_question`", "`ethics`", "`cost`", "`access`",
			"Never ask about method, tools, evidence or retries that the researcher left to you",
			"the researcher explicitly reserves a choice or an approval for Decisions",
			"`status: ask` and wait for the actual answer",
			"does not substitute for the researcher's recorded Decision answer",
		} {
			if !strings.Contains(p, want) {
				t.Errorf("%s is not told %s", role, want)
			}
		}
	}
	if p := Protocol("leader", board.Hypotheses, "", nil); !strings.Contains(p, "naming the member in `from_member`") {
		t.Errorf("the leader is not told to raise a member's question")
	}
	if p := Protocol("Curie", board.Hypotheses, "", nil); !strings.Contains(p, "goes to the leader") {
		t.Errorf("a member is not told its question goes to the leader")
	}
}

func TestStudyRolesReceiveExactSourceToolArguments(t *testing.T) {
	for _, role := range []string{"leader", "agent", "member", "auditor"} {
		p := Protocol(role, board.Grounding, "", nil)
		for _, want := range []string{`"database": "europepmc"`, `"query":`, `"source_id":`, `list_source_tools`, "no other arguments"} {
			if !strings.Contains(p, want) {
				t.Errorf("%s missing source contract %q", role, want)
			}
		}
	}
}

func TestStudyInstructionsExplainReferenceTypesAndHandInExample(t *testing.T) {
	for _, col := range board.Stages {
		for _, role := range []string{"leader", "agent", "member", "auditor"} {
			p := Protocol(role, col, col, nil)
			for _, want := range []string{"exact returned source ID", "already returned text", "no-text result", "sections returned by `find_tools`", "never a source ID, skill path or receipt", "siblings of `answer`"} {
				if !strings.Contains(p, want) {
					t.Errorf("%s/%s lacks %q", col, role, want)
				}
			}
			_, rest, ok := strings.Cut(p, "```json\n")
			example, _, end := strings.Cut(rest, "\n```")
			if !ok || !end {
				t.Errorf("%s/%s missing example", col, role)
				continue
			}
			k := handin.Kind(col)
			if role == "auditor" {
				k = handin.Auditor
			}
			var fields map[string]any
			if err := json.Unmarshal([]byte(example), &fields); err != nil {
				t.Fatal(err)
			}
			var schema struct{ Required []string }
			if err := json.Unmarshal([]byte(handin.Schema(k)), &schema); err != nil {
				t.Fatal(err)
			}
			for _, key := range schema.Required {
				if _, ok := fields[key]; !ok {
					t.Errorf("%s/%s example missing %s", col, role, key)
				}
			}
			if _, probs := handin.Parse(k, []byte(example)); len(probs) != 0 {
				t.Errorf("%s/%s example: %v", col, role, probs)
			}
			if col == board.Experimentation && role != "auditor" {
				for _, want := range []string{"Placeholder prints", "documented code input", "/dev/stdin"} {
					if !strings.Contains(p, want) {
						t.Errorf("missing %s", want)
					}
				}
			}
		}
	}
}

func TestEveryShippedRoleExplainsEvidenceAndUsesItsSchema(t *testing.T) {
	for _, name := range pack.ShippedNames(pack.Shipped()) {
		_, p, err := pack.ReadShipped(pack.Shipped(), name)
		if err != nil {
			t.Fatal(err)
		}
		roles := append([]pack.Member{p.Manifest.Leader, p.Manifest.Auditor}, p.Manifest.Members...)
		for _, role := range roles {
			instructions := role.Agent.Instructions
			for _, want := range []string{"exact returned source ID", "already returned text", "no-text result", "sections returned by `find_tools`", "never a source ID, skill path or receipt", "siblings of `answer`", "Placeholder prints", "documented code input"} {
				if !strings.Contains(instructions, want) {
					t.Errorf("%s/%s missing %s", name, role.Name, want)
				}
			}
			_, rest, ok := strings.Cut(instructions, "```json\n")
			example, _, end := strings.Cut(rest, "\n```")
			if !ok || !end {
				t.Fatalf("%s/%s lacks example", name, role.Name)
			}
			k := handin.Kind(p.Manifest.Stage)
			if role.Name == p.Manifest.Auditor.Name {
				k = handin.Auditor
			}
			if _, probs := handin.Parse(k, []byte(example)); len(probs) != 0 {
				t.Errorf("%s/%s: %v", name, role.Name, probs)
			}
		}
	}
}

func TestCustomReportTemplateReplacesDefaultFormat(t *testing.T) {
	for _, role := range []string{"agent", "leader", "member", "auditor"} {
		p := reportTemplate(role, board.Grounding, "Start with one sentence stating the finding.")
		if !strings.Contains(p, "Start with one sentence stating the finding.") || strings.Contains(p, "without required headings") || !strings.Contains(p, "never invent evidence") {
			t.Errorf("%s: unexpected template: %s", role, p)
		}
	}
}

func TestReportGuidanceForResearchers(t *testing.T) {
	for _, role := range []string{"agent", "leader", "member", "auditor"} {
		for _, custom := range []string{"", "## My findings"} {
			p := Protocol(role, board.Grounding, board.Grounding, nil, custom)
			for _, want := range []string{"focus on findings, evidence, limitations and decisions", "internal tools, routing, prompts, hand-in formats or retries", "unless the researcher asks or the detail explains a material limitation", "Keep source attribution and meaningful failures clear", "do not mention a recovered error", "Do not suggest the researcher needs to fix a problem that no longer blocks the answer", "omit HTTP status, cache details and database field names"} {
				if !strings.Contains(p, want) {
					t.Errorf("%s custom=%t: missing %q", role, custom != "", want)
				}
			}
		}
	}
}

func TestConnectorRecoveryGuidance(t *testing.T) {
	p := Protocol("agent", board.Grounding, board.Grounding, nil)
	for _, want := range []string{"search again by its exact name", "query parameters in `query`", "do not bypass sandbox or approval requirements", "report that limitation", "If discovery itself fails", "retry the corrected call once"} {
		if !strings.Contains(p, want) {
			t.Errorf("missing connector recovery guidance %q", want)
		}
	}
}

// A planning column cannot run code. When the researcher's question asks for
// an analysis, its runs must defer that analysis to Experimentation rather
// than hand in could_not_do and block the study (seen live on BixBench tasks).
func TestPlanningColumnsDeferTheAnalysisInsteadOfGivingUp(t *testing.T) {
	for _, col := range []string{"grounding", "hypotheses"} {
		for _, role := range []string{"leader", "agent", "Curie"} {
			p := Protocol(role, col, "", nil)
			for _, want := range []string{"runs in Experimentation", "never a reason for `could_not_do`"} {
				if !strings.Contains(p, want) {
					t.Errorf("%s on %s is not told %q", role, col, want)
				}
			}
		}
	}
	for _, col := range []string{"experimentation", "validation"} {
		if p := Protocol("leader", col, "", nil); strings.Contains(p, "never a reason for `could_not_do`") {
			t.Errorf("%s is told code unavailability is never a reason to stop", col)
		}
	}
}
