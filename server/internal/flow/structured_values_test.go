package flow

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/board"
	"lykeion/server/internal/review"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func TestStructuredValueChecksUseAttestedField(t *testing.T) {
	const path = "outputs/metrics.json"
	const contents = `{"decoy":{"value":0.1245,"measure":"decoy","unit":"percent"},"effect":{"value":0.1245,"measure":"effect","unit":"fraction"},"scalar":0.1245,"other":0.987,"reversed":-0.1245}`
	producer := uuid.New()
	runBody := fmt.Appendf(nil, `{"tool":"run_script","exit_status":0,"output_hashes":{"%s":"%x"}}`, path, sha256.Sum256([]byte(contents)))
	outputBody := fmt.Appendf(nil, `{"name":"%s","sha256":"%x"}`, path, sha256.Sum256([]byte(contents)))
	measure := review.Measure{ID: "effect", Name: "Treatment effect", Unit: "fraction"}
	for _, tc := range []struct {
		name, field, value, id, cite, fail string
	}{
		{"clean", "/effect/value", "0.125", "effect", "run:x1", ""},
		{"untagged field", "/scalar", "0.125", "effect", "run:x1", ""},
		{"wrong field", "/other", "0.125", "effect", "run:x1", "bound"},
		{"wrong value", "/effect/value", "0.987", "effect", "run:x1", "bound"},
		{"missing field", "/missing", "0.125", "effect", "run:x1", "bound"},
		{"unrelated script", "/effect/value", "0.125", "effect", "run:x2", "bound"},
		{"unknown measure", "/other", "0.987", "absent", "run:x1", "measure"},
		{"fraction as percent", "/effect/value", "12.45", "effect", "run:x1", "scale"},
		{"percent as fraction", "/effect/value", "0.001245", "effect", "run:x1", "scale"},
		{"reversed sign", "/effect/value", "-0.1245", "effect", "run:x1", "bound"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runs := []db.Record{
				{Kind: "run", BoardColumn: board.Experimentation, Key: "x1", Body: runBody, AgentRunID: &producer, Round: 2},
				{Kind: "run", BoardColumn: board.Experimentation, Key: "x2", Body: json.RawMessage(`{"tool":"run_script","exit_status":0,"output":"0.125"}`), AgentRunID: &producer, Round: 2},
			}
			work := []db.Record{{Kind: "output", BoardColumn: board.Experimentation, Key: "o1", Body: outputBody, AgentRunID: &producer, Round: 2}}
			value := review.ValueBody{Name: tc.name, File: path, Value: tc.value, Field: &tc.field, Measure: &tc.id, Cites: []string{tc.cite}}
			b, _ := json.Marshal(value)
			work = append(work, db.Record{Kind: "value", Key: "r1", Body: b})
			values := experimentationValues(work, runs, nil, map[string]string{"o1": contents}, map[string]review.Measure{"effect": measure})
			res, err := review.Experimentation(t.Context(), nil, review.ExperimentationInput{Values: values})
			if err != nil || len(res.Items) != 1 {
				t.Fatalf("expected one reviewed value: %+v, %v", res, err)
			}
			item := res.Items[0]
			if tc.fail == "" {
				if item.Status != review.Pass || item.Output != "o1" || item.Measure == nil {
					t.Fatalf("expected clean binding: %+v", item)
				}
				want := fmt.Sprintf(`JSON Pointer %q resolves to 0.1245. Accepted hypothesis measure "Treatment effect" (ID "effect", unit "fraction").`, tc.field)
				if got := values[0].Context; got != want {
					t.Fatalf("Jev context = %q, want %q", got, want)
				}
			} else if item.Status != review.Fail || !slices.ContainsFunc(item.Checks, func(c review.Check) bool {
				return c.ID == tc.fail && c.Status == review.Fail && c.By == "server"
			}) {
				t.Fatalf("wanted a blocking %s check: %+v", tc.fail, item)
			}
		})
	}
}

func TestLegacyValueGetsOnlyAnUnboundWarning(t *testing.T) {
	work := []db.Record{{Kind: "value", Key: "r1", Body: json.RawMessage(`{"name":"old","value":"0.12","cites":["run:x1"]}`)}}
	values := experimentationValues(work, nil, map[string]Run{"x1": {Tool: "run_script", Stdout: "0.12"}}, nil, nil)
	res, err := review.Experimentation(t.Context(), nil, review.ExperimentationInput{Values: values})
	if err != nil || len(res.Items) != 1 || res.Items[0].Status != review.Pass ||
		!slices.ContainsFunc(res.Items[0].Checks, func(c review.Check) bool { return c.ID == "unbound" && c.Warning }) {
		t.Fatalf("legacy value should pass with a warning: %+v, %v", res, err)
	}
}

func TestBoundReplayDoesNotUseAnotherFieldOrStdout(t *testing.T) {
	path, pointer, id := "outputs/metrics.json", "/effect/value", "effect"
	v := valueRecord{Name: "effect", Value: "0.12", File: path, Cites: []string{"run:x1"}, Field: &pointer, Measure: &id}
	m := review.Measure{ID: id, Name: "Effect", Unit: "fraction"}
	for _, tc := range []struct{ file, expect string }{
		{`{"effect":{"value":0.1249,"measure":"effect","unit":"fraction"}}`, "0.1249"},
		{`{"other":0.12,"effect":{"value":0.11}}`, ""},
		{`{"effect":{"value":0.1249,"unit":"percent"}}`, ""},
	} {
		files := map[string]string{path: tc.file}
		steps := map[string]RerunStepResult{"x1": {Output: "0.12", OutputHashes: map[string]string{path: hashText(tc.file)}}}
		got, note := replayBound(v, steps, files, m, 0)
		if got != tc.expect || (note == "") != (tc.expect != "") {
			t.Fatalf("replay of %s got %q, %q", tc.file, got, note)
		}
	}
}

func TestPilotStructuredValueMutations(t *testing.T) {
	// Numbers and opaque case IDs come from the saved pilot. The archive did
	// not declare bindings, so each fixture supplies one explicitly. A type 8
	// mutation changes scale and a type 9 mutation reverses the numeric sign.
	raw, err := os.ReadFile("testdata/structured-value-pilot.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ ID, Type, Name, Unit, Reported, Other, Planted string }
	if err := json.Unmarshal(raw, &cases); err != nil || len(cases) != 6 {
		t.Fatalf("unexpected pilot fixtures: %+v, %v", cases, err)
	}
	for _, tc := range cases {
		const path = "outputs/pilot.json"
		contents := fmt.Sprintf(`{"metric":{"value":%s,"unit":%q,"measure":"metric"}}`, tc.Reported, tc.Unit)
		if tc.Other != "" {
			contents = fmt.Sprintf(`{"metric":{"value":%s,"unit":%q,"measure":"metric"},"other":%s}`, tc.Reported, tc.Unit, tc.Other)
		}
		runID := uuid.New()
		run := db.Record{Kind: "run", BoardColumn: board.Experimentation, Key: "x1", AgentRunID: &runID, Body: fmt.Appendf(nil,
			`{"tool":"run_script","exit_status":0,"output_hashes":{%q:%q}}`, path, hashText(contents))}
		output := db.Record{Kind: "output", BoardColumn: board.Experimentation, Key: "o1", AgentRunID: &runID, Body: fmt.Appendf(nil,
			`{"name":%q,"sha256":%q}`, path, hashText(contents))}
		id := "metric"
		measure := review.Measure{ID: id, Name: tc.Name, Unit: tc.Unit}
		plantedField := "/metric/value"
		if tc.Type == "7" {
			plantedField = "/other"
		}
		for _, variant := range []struct{ name, field, number, expected string }{
			{"clean", "/metric/value", tc.Reported, review.Pass},
			{"prose-only", "/metric/value", tc.Reported, review.Pass},
			{"planted", plantedField, tc.Planted, review.Fail},
		} {
			t.Run(tc.ID+"/"+variant.name, func(t *testing.T) {
				name := tc.Name
				if variant.name == "prose-only" {
					name = "Misleading free-text label"
				}
				v := review.ValueBody{Name: name, Value: variant.number, File: path, Field: &variant.field, Measure: &id, Cites: []string{"run:x1"}}
				body, _ := json.Marshal(v)
				work := []db.Record{output, {Kind: "value", Key: "v1", Body: body}}
				check := experimentationValues(work, []db.Record{run}, nil, map[string]string{"o1": contents}, map[string]review.Measure{id: measure})
				res, err := review.Experimentation(t.Context(), nil, review.ExperimentationInput{Values: check})
				if err != nil || len(res.Items) != 1 || res.Items[0].Status != variant.expected {
					t.Fatalf("want %s: %+v %v", variant.expected, res, err)
				}
				if variant.expected == review.Fail && review.Decide(res, 0) != review.DecideRedo {
					t.Fatalf("planted error should enter the redo gate: %+v", res)
				}
			})
		}
	}
}

func TestKeylessExperimentationBindingGate(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(fmt.Sprintf("valid=%t", valid), func(t *testing.T) {
			l := newLab(t)
			l.e.Jev = nil
			blobs := storage.Local{Dir: t.TempDir()}
			l.e.Blobs = blobs
			a := l.agent(t, "Curie", "claude", "", "You are Curie.")
			c := l.scoped(t, board.Experimentation, board.Validation, a)
			l.e.Kick(l.ws, c.ID)
			const path = "outputs/result.json"
			const content = `{"m":0.125,"other":0.25}`
			const blob = "test/result.json"
			if err := blobs.Put(t.Context(), blob, strings.NewReader(content), int64(len(content))); err != nil {
				t.Fatal(err)
			}
			l.in(t, func(tx *store.Tx) error {
				runs, err := tx.Q.RoundAgentRuns(t.Context(), db.RoundAgentRunsParams{
					WorkspaceID: l.ws, CardID: c.ID, BoardColumn: c.BoardColumn, Round: c.Round,
				})
				if err != nil {
					return err
				}
				if len(runs) == 0 {
					return fmt.Errorf("expected an Experimentation agent run")
				}
				producer := runs[0].ID
				write := func(column, kind, role, key, blob string, body []byte, producer *uuid.UUID) error {
					_, err := tx.Q.InsertRecord(t.Context(), db.InsertRecordParams{
						WorkspaceID: l.ws, CardID: c.ID, BoardColumn: column, Round: c.Round, Kind: kind, Role: role,
						Key: key, BlobKey: blob, Body: body, AgentRunID: producer,
					})
					return err
				}
				for _, rec := range []struct{ column, kind, role, key, blob, body string }{
					{board.Hypotheses, "hypothesis", "agent", "hyp-test", "", `{"text":"We expect a change.","measure":{"id":"effect","name":"Effect","unit":"fraction"}}`},
					{board.Hypotheses, "review", "server", "review-test", "", `{"items":[{"key":"hyp-test","kind":"hypothesis","status":"pass"}]}`},
					{board.Hypotheses, "outcome", "server", "outcome-test", "", `{"verdict":"pass"}`},
					{board.Experimentation, "run", "server", "x1", "", fmt.Sprintf(`{"tool":"run_script","exit_status":0,"output_hashes":{%q:%q}}`, path, hashText(content))},
					{board.Experimentation, "output", "agent", "output-test", blob, fmt.Sprintf(`{"name":%q,"sha256":%q}`, path, hashText(content))},
				} {
					var owner *uuid.UUID
					if rec.column == board.Experimentation {
						owner = &producer
					}
					if err := write(rec.column, rec.kind, rec.role, rec.key, rec.blob, []byte(rec.body), owner); err != nil {
						return err
					}
				}
				return nil
			})
			reported := "0.25"
			if valid {
				reported = "0.125"
			}
			field, measure := "/m", "effect"
			l.item(t, c.ID, "value", review.ValueBody{Name: "Effect", Value: reported, File: path, Cites: []string{"run:x1"}, Field: &field, Measure: &measure})
			l.done(t, c.ID)
			l.e.Kick(l.ws, c.ID)
			inbox := captureChecks(t, l, c.ID, "binding review", nil)
			if !valid {
				assertServerFailure(t, l, c.ID, board.Experimentation, c.Round, "bound")
			}
			offered, err := tryCheckApproval(t, l, c.ID, inbox, nil)
			if valid && (!offered || err != nil) || !valid && offered {
				t.Fatalf("valid=%t, approval offered=%t, error=%v", valid, offered, err)
			}
			got := l.card(t, c.ID)
			if valid && got.BoardColumn != board.Validation || !valid && (got.BoardColumn != board.Experimentation || got.Round != c.Round+1 || got.Redos != 1) {
				t.Fatalf("valid=%t, unexpected stage or round: %+v", valid, got)
			}
		})
	}
}

// A unit mismatch blocked two live studies after their redo: the check named
// neither unit, so the agent could not copy the declared one. It names both.
func TestUnitMismatchNamesBothUnits(t *testing.T) {
	const path = "outputs/metrics.json"
	const contents = `{"share":{"value":36.65,"measure":"share","unit":"percent"}}`
	producer := uuid.New()
	runBody := fmt.Appendf(nil, `{"tool":"run_script","exit_status":0,"output_hashes":{"%s":"%x"}}`, path, sha256.Sum256([]byte(contents)))
	outputBody := fmt.Appendf(nil, `{"name":"%s","sha256":"%x"}`, path, sha256.Sum256([]byte(contents)))
	runs := []db.Record{{Kind: "run", BoardColumn: board.Experimentation, Key: "x1", Body: runBody, AgentRunID: &producer, Round: 2}}
	field, id := "/share/value", "share"
	b, _ := json.Marshal(review.ValueBody{Name: "share", File: path, Value: "36.65", Field: &field, Measure: &id, Cites: []string{"run:x1"}})
	work := []db.Record{
		{Kind: "output", BoardColumn: board.Experimentation, Key: "o1", Body: outputBody, AgentRunID: &producer, Round: 2},
		{Kind: "value", Key: "r1", Body: b},
	}
	measure := review.Measure{ID: "share", Name: "Share significant", Unit: "percent of miRNAs tested"}
	values := experimentationValues(work, runs, nil, map[string]string{"o1": contents}, map[string]review.Measure{"share": measure})
	var detail string
	for _, c := range values[0].Binding {
		if c.ID == "scale" && c.Status == review.Fail {
			detail = c.Detail
		}
	}
	if !strings.Contains(detail, `"percent"`) || !strings.Contains(detail, `"percent of miRNAs tested"`) {
		t.Fatalf("scale detail = %q", detail)
	}
}

// Live (bix-37): the script saved 72896133.2946858 (Python's repr) and the
// agent reported 72896133.294685796 (numpy's 17-digit repr of the same
// number); padding the saved value to the reported places failed a correct
// answer on every redo. More reported digits that round to the saved field
// now bind; ones that do not still fail.
func TestBoundValueWithMoreDigitsThanTheSavedField(t *testing.T) {
	const path = "outputs/eno1.json"
	const contents = `{"eno1":{"value":72896133.2946858,"measure":"eno1","unit":"intensity"}}`
	producer := uuid.New()
	runBody := fmt.Appendf(nil, `{"tool":"run_script","exit_status":0,"output_hashes":{"%s":"%x"}}`, path, sha256.Sum256([]byte(contents)))
	outputBody := fmt.Appendf(nil, `{"name":"%s","sha256":"%x"}`, path, sha256.Sum256([]byte(contents)))
	runs := []db.Record{{Kind: "run", BoardColumn: board.Experimentation, Key: "x1", Body: runBody, AgentRunID: &producer, Round: 2}}
	measure := review.Measure{ID: "eno1", Name: "ENO1 level", Unit: "intensity"}
	for reported, want := range map[string]string{
		"72896133.294685796": review.Pass, // same number, more digits
		"72896133.2946858":   review.Pass,
		"72896133.294685896": review.Fail, // rounds to ...2946859
		"72896133.30":        review.Fail, // a rounded report must round from the saved field
	} {
		field, id := "/eno1/value", "eno1"
		b, _ := json.Marshal(review.ValueBody{Name: "eno1", File: path, Value: reported, Field: &field, Measure: &id, Cites: []string{"run:x1"}})
		work := []db.Record{
			{Kind: "output", BoardColumn: board.Experimentation, Key: "o1", Body: outputBody, AgentRunID: &producer, Round: 2},
			{Kind: "value", Key: "r1", Body: b},
		}
		values := experimentationValues(work, runs, nil, map[string]string{"o1": contents}, map[string]review.Measure{"eno1": measure})
		var got string
		for _, c := range values[0].Binding {
			if c.ID == "bound" {
				got = c.Status
			}
		}
		if got != want {
			t.Errorf("reported %s: bound %s, want %s", reported, got, want)
		}
	}
}
