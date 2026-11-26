package flow

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/review"
	"lykeion/server/internal/storage"
	db "lykeion/server/pkg/db/generated"
)

func TestExperimentationRejectsPilotNumbersAbsentFromCitedEvidence(t *testing.T) {
	// Preserve each value's precision, attested field and citation shape.
	// The stdout fixtures retain only the relevant numeric evidence. The
	// Awareness scorer printed threshold labels, so its clean values need
	// the saved file even on the legacy tracing path.
	const awarenessStdout = "AUC outcome: At or below 0.5\nBrier outcome: At or above 0.25\n"
	for _, tc := range []struct {
		name, metric, unit, original, absent, stdout string
	}{
		{"length/matched-gap", "primary-bound-minus-exact", "fraction", "0.5625", "0.5313",
			`{"primary-bound-minus-exact":{"value":0.5625,"measure":"primary-bound-minus-exact","unit":"fraction"}}`},
		{"premise/first-minus-last", "primary-first-minus-last", "fraction", "0.0", "-0.1",
			`{"splits":{"primary":{"accuracy_difference_first_minus_last":0.0}}}`},
		{"premise/anchor-first-accuracy", "primary-anchor-first-accuracy", "fraction", "0.5833333333333334", "0.6250000000000000",
			`{"splits":{"primary":{"accuracy_first":0.5833333333333334}}}`},
		{"awareness/pooled-roc-auc", "primary-pooled-roc-auc", "unitless", "0.24691358024691357", "0.24537037037037037", awarenessStdout},
		{"awareness/brier-score", "primary-brier-score", "unitless", "0.5137277777777778", "0.5087277777777778", awarenessStdout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			measure := review.Measure{ID: tc.metric, Name: tc.metric, Unit: tc.unit}
			fixture := newValueProvenanceFixture(t, measure)
			const path = "outputs/bound-metrics.json"
			fixture.addRun(t, "x1", path, tc.original, tc.stdout)
			pointer := "/" + measure.ID + "/value"
			for _, binding := range []struct {
				name, check string
				field, id   *string
			}{
				{name: "bound", check: "bound", field: &pointer, id: &measure.ID},
				{name: "unbound", check: "traced"},
			} {
				for _, sample := range []struct{ name, number, status string }{
					{"original", tc.original, review.Pass},
					{"absent", tc.absent, review.Fail},
				} {
					t.Run(binding.name+"/"+sample.name, func(t *testing.T) {
						value := review.ValueBody{
							Name: measure.Name, Value: sample.number, File: path, Cites: []string{"run:x1"},
							Field: binding.field, Measure: binding.id,
						}
						res := fixture.review(t, value)
						assertProvenanceCheck(t, res, binding.check, sample.status)
						if binding.field == nil && !slices.ContainsFunc(res.Items[0].Checks, func(c review.Check) bool {
							return c.ID == "unbound" && c.Warning
						}) {
							t.Fatalf("legacy value lost its unbound warning: %+v", res.Items[0])
						}
					})
				}
			}
		})
	}
}

func TestExperimentationWrongRunBindingBlocksButLegacyMultiCitationPasses(t *testing.T) {
	measure := review.Measure{ID: "accuracy", Name: "Accuracy", Unit: "fraction"}
	fixture := newValueProvenanceFixture(t, measure)
	const path = "outputs/metrics.json"
	fixture.addRun(t, "x1", path, "0.71", "accuracy=0.71\n")
	fixture.addRun(t, "x2", path, "0.83", "accuracy=0.83\n")

	t.Run("bound_to_run_A_blocks_run_B_value", func(t *testing.T) {
		field := "/accuracy/value"
		res := fixture.review(t, review.ValueBody{
			Name: measure.Name, Value: "0.83", File: path, Cites: []string{"run:x1"}, Field: &field, Measure: &measure.ID,
		})
		check := assertProvenanceCheck(t, res, "bound", review.Fail)
		if !strings.Contains(check.Detail, "differs from the saved field") {
			t.Fatalf("expected an attested field mismatch: %+v", check)
		}
	})

	t.Run("known_gap_unbound_accepts_any_cited_run", func(t *testing.T) {
		res := fixture.review(t, review.ValueBody{
			Name: measure.Name, Value: "0.83", File: path, Cites: []string{"run:x1", "run:x2"},
		})
		check := assertProvenanceCheck(t, res, "traced", review.Pass)
		if check.Detail != "run:x2" {
			t.Fatalf("expected legacy tracing to accept the second citation's stdout: %+v", check)
		}
	})
}

type valueProvenanceFixture struct {
	card    db.Card
	measure review.Measure
	blobs   storage.Local
	runs    []db.Record
	outputs []db.Record
}

func newValueProvenanceFixture(t *testing.T, m review.Measure) *valueProvenanceFixture {
	t.Helper()
	return &valueProvenanceFixture{
		card:    db.Card{WorkspaceID: uuid.New(), ID: uuid.New(), BoardColumn: board.Experimentation, Round: 3},
		measure: m,
		blobs:   storage.Local{Dir: t.TempDir()},
	}
}

func (f *valueProvenanceFixture) addRun(t *testing.T, key, path, number, stdout string) {
	t.Helper()
	producer := uuid.New()
	m := f.measure
	text := fmt.Sprintf(`{%q:{"value":%s,"measure":%q,"unit":%q}}`, m.ID, number, m.ID, m.Unit)
	digest := hashText(text)
	blob := "fixtures/" + key + ".json"
	if err := f.blobs.Put(t.Context(), blob, strings.NewReader(text), int64(len(text))); err != nil {
		t.Fatal(err)
	}
	base := db.Record{
		WorkspaceID: f.card.WorkspaceID, CardID: f.card.ID, BoardColumn: f.card.BoardColumn,
		Round: f.card.Round, AgentRunID: &producer,
	}
	run := base
	run.Kind, run.Key = "run", key
	run.Body = fmt.Appendf(nil, `{"tool":"run_script","exit_status":0,"output":%q,"output_hashes":{%q:%q}}`, stdout, path, digest)
	f.runs = append(f.runs, run)
	output := base
	output.Kind, output.Key, output.BlobKey = "output", "output-"+key, blob
	output.Body = fmt.Appendf(nil, `{"name":%q,"sha256":%q,"size_bytes":%d}`, path, digest, len(text))
	f.outputs = append(f.outputs, output)
}

func (f *valueProvenanceFixture) review(t *testing.T, value review.ValueBody) review.Result {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	record := db.Record{
		WorkspaceID: f.card.WorkspaceID, CardID: f.card.ID, BoardColumn: f.card.BoardColumn,
		Round: f.card.Round, Kind: "value", Key: "value", Body: body,
	}
	engine := Engine{Blobs: f.blobs}
	check, err := engine.prepareReview(t.Context(), f.card.WorkspaceID, scoring{card: f.card, attempt: 1}, reviewing{
		runs: f.runs, work: append(slices.Clone(f.outputs), record),
		measures: map[string]review.Measure{f.measure.ID: f.measure},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := check(nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func assertProvenanceCheck(t *testing.T, res review.Result, id, status string) review.Check {
	t.Helper()
	if len(res.Items) != 1 || res.Items[0].Key != "value" || res.Items[0].Status != status {
		t.Fatalf("expected one value with status %s: %+v", status, res)
	}
	first, afterRedo := review.Decide(res, 0), review.Decide(res, 1)
	if status == review.Fail {
		if first != review.DecideRedo || afterRedo != review.DecideBlock {
			t.Fatalf("expected redo then block, got %s then %s: %+v", first, afterRedo, res)
		}
	} else if first != review.DecidePass || afterRedo != review.DecidePass || !res.NeedsJudgment {
		t.Fatalf("clean server pass should only await judgment: %+v", res)
	}
	for _, c := range res.Items[0].Checks {
		if c.ID == id && c.By == "server" && c.Status == status && !c.Warning {
			return c
		}
	}
	t.Fatalf("expected server check %s=%s: %+v", id, status, res.Items[0].Checks)
	return review.Check{}
}
