package flow

import (
	"context"
	"encoding/json"
	"maps"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// rerunStudy is a study that handed in Experimentation round 1 with the given
// runs and a value auc cited to the first, now at Validation round round.
func rerunStudy(t *testing.T, l *lab, round int32, runs ...map[string]any) db.Card {
	t.Helper()
	c := l.study(t, "experimentation", true, nil)
	var cites []string
	for _, r := range runs {
		cites = append(cites, "run:"+l.put(t, c.ID, "experimentation", 1, "run", "leader", r))
	}
	l.put(t, c.ID, "experimentation", 1, "value", "leader", map[string]any{"name": "auc", "value": "0.873", "file": "fit.py", "cites": cites})
	return l.move(t, c.ID, "validation", round)
}

func (l *lab) startRerun(t *testing.T, id uuid.UUID) {
	t.Helper()
	c := l.card(t, id)
	l.in(t, func(tx *store.Tx) error { return l.e.startRerun(context.Background(), tx, c) })
}

func script(exit int) map[string]any {
	return map[string]any{"tool": "run_script", "language": "python", "path": "fit.py", "script": "print(0.873)", "exit_status": exit, "output": "0.873"}
}

func TestRerunEnqueuedOncePerValidationRound(t *testing.T) {
	l := newLab(t)
	c := rerunStudy(t, l, 1, script(0))
	l.startRerun(t, c.ID)
	l.startRerun(t, c.ID)
	jobs := func() int {
		return l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND kind = 'rerun' AND role = 'rerun' AND harness = '' AND board_column = 'validation'", c.ID)
	}
	if n := jobs(); n != 1 {
		t.Fatalf("two starts of one round queued %d rerun jobs", n)
	}
	l.move(t, c.ID, "validation", 2)
	l.startRerun(t, c.ID)
	if n := jobs(); n != 2 {
		t.Fatalf("a new round queued %d rerun jobs in all, want 2", n)
	}
	if n := l.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'rerun'", c.ID); n != 0 {
		t.Fatalf("a queued job wrote %d rerun records", n)
	}
}

func TestRoundWithNoScriptsNeedsNoJob(t *testing.T) {
	l := newLab(t)
	c := rerunStudy(t, l, 1,
		map[string]any{"tool": "run_code", "language": "python", "code": "print(0.873)", "exit_status": 0, "output": "0.873"},
		script(1))
	l.startRerun(t, c.ID)
	l.startRerun(t, c.ID)
	if n := l.count(t, "SELECT count(*) FROM agent_run WHERE card_id = $1 AND kind = 'rerun'", c.ID); n != 0 {
		t.Fatalf("a round with nothing to replay queued %d jobs", n)
	}
	var bodies [][]byte
	rows, err := l.env.Owner.Query(context.Background(), "SELECT body FROM record WHERE card_id = $1 AND kind = 'rerun' AND board_column = 'validation' AND round = 1 AND role = 'server'", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, b)
	}
	if len(bodies) != 1 {
		t.Fatalf("%d rerun records, want one", len(bodies))
	}
	var body struct {
		Reproduced map[string]bool
		Notes      map[string]string
	}
	if err := json.Unmarshal(bodies[0], &body); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(body.Reproduced, map[string]bool{"auc": false}) || body.Notes["auc"] != "no recorded script run produced this value" {
		t.Fatalf("record %s", bodies[0])
	}
}

func TestToleranceWidensTheDigitRule(t *testing.T) {
	values := []db.Record{{Kind: "value", Body: []byte(`{"name":"acc","value":"0.912","file":"fit.py","cites":["run:x1"]}`)}}
	steps := map[string]RerunStepResult{"x1": {Key: "x1", Output: "epoch 3 acc 0.874"}}
	if ok, notes := reproduce(values, steps, nil, 0); ok["acc"] || notes["acc"] != "rerun printed 0.874, reported 0.912" {
		t.Fatalf("no tolerance: %v %v", ok, notes)
	}
	if ok, notes := reproduce(values, steps, nil, 0.05); !ok["acc"] || len(notes) != 0 {
		t.Fatalf("5%% tolerance: %v %v", ok, notes)
	}
	if ok, _ := reproduce(values, steps, nil, 0.04); ok["acc"] {
		t.Fatalf("4%% tolerance reproduced a value 4.2%% off")
	}
}

func TestAValueFoundInAnyFileTheRerunSavedIsReproduced(t *testing.T) {
	values := []db.Record{{Kind: "value", Body: []byte(`{"name":"f1","value":"1.5","file":"fit.py","cites":["run:x1"]}`)}}
	steps := map[string]RerunStepResult{"x1": {Key: "x1", Output: "done"}}
	if ok, notes := reproduce(values, steps, map[string]string{"out/metrics.txt": "f1 1.50"}, 0); !ok["f1"] {
		t.Fatalf("a saved file of another name: %v %v", ok, notes)
	}
	// A value citing no replayed script is not found in an unrelated file.
	values = []db.Record{{Kind: "value", Body: []byte(`{"name":"f1","value":"1.5","file":"other.txt","cites":[]}`)}}
	if ok, _ := reproduce(values, steps, map[string]string{"out/metrics.txt": "f1 1.50"}, 0); ok["f1"] {
		t.Fatal("a value no replayed script made was found in another file")
	}
}
