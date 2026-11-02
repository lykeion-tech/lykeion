package flow

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/sources"
	"lykeion/server/internal/storage"
	db "lykeion/server/pkg/db/generated"
)

func valueRec(key, kind string, body map[string]any) db.Record {
	b, _ := json.Marshal(body)
	return db.Record{Key: key, Kind: kind, Body: b}
}

// scriptOut prints no 0.125, which rounds to 0.13 at two places; that case
// is checked on its own below.
const scriptOut = "effect 0.123456\nn 42\np 1e-3\nshare 12.5%\ncount 1,234\nzero -0.0\n"

func TestValueMatchingRules(t *testing.T) {
	runs := map[string]Run{"x1": {Tool: "run_script", Stdout: scriptOut}}
	for _, tc := range []struct {
		name  string
		value any
		want  bool
	}{
		{"rounded to the value's places", json.Number("0.12"), true},
		{"a different rounding", json.Number("0.13"), false},
		{"a numeric string", "0.12", true},
		{"an integer", json.Number("42"), true},
		{"an integer written with a place", json.Number("42.0"), true},
		{"an integer is never a prefix", json.Number("4"), false},
		{"an integer is never a rounding", json.Number("13"), false},
		{"an exponent token", json.Number("0.001"), true},
		{"a percentage", json.Number("12.5"), true},
		{"thousands are not joined", json.Number("1234"), false},
		{"a group after a comma", json.Number("234"), true},
		{"negative zero", json.Number("0"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := valueRec("r1", "result", map[string]any{"cites": []string{"run:x1"}, "value": tc.value})
			got := MatchValues([]db.Record{rec}, runs)
			if len(got) != 1 || got[0].Matched != tc.want || got[0].Record != "r1" || got[0].Value != strings.Trim(mustJSON(tc.value), `"`) {
				t.Fatalf("got %+v, want matched=%v", got, tc.want)
			}
			if tc.want && got[0].Run != "x1" {
				t.Fatalf("matched run %q", got[0].Run)
			}
		})
	}
	t.Run("half rounds up", func(t *testing.T) {
		got := MatchValues([]db.Record{valueRec("r1", "result", map[string]any{"cites": []string{"run:x2"}, "value": 0.13})},
			map[string]Run{"x2": {Tool: "run_script", Stdout: "half 0.125\n"}})
		if len(got) != 1 || !got[0].Matched {
			t.Fatalf("0.125 did not round to 0.13: %+v", got)
		}
	})
	t.Run("a nested row uses its own cites", func(t *testing.T) {
		runs := map[string]Run{"x1": {Tool: "run_script", Stdout: "a 1.5\n"}, "x2": {Tool: "run_script", Stdout: "b 2.5\n"}}
		rec := valueRec("r1", "result", map[string]any{"cites": []string{"run:x1"}, "rows": []any{
			map[string]any{"value": 1.5},
			map[string]any{"values": []any{2.5}, "cites": []string{"run:x2"}},
			map[string]any{"value": 2.5},
		}})
		got := MatchValues([]db.Record{rec}, runs)
		want := []ValueMatch{{Record: "r1", Value: "1.5", Run: "x1", Matched: true}, {Record: "r1", Value: "2.5", Run: "x2", Matched: true}, {Record: "r1", Value: "2.5"}}
		for _, w := range want {
			found := false
			for _, m := range got {
				found = found || m == w
			}
			if !found {
				t.Fatalf("no %+v in %+v", w, got)
			}
		}
	})
}

func TestValueJoinedToNames(t *testing.T) {
	for _, tc := range []struct {
		out   string
		value any
		want  bool
	}{
		{"H2O\n", json.Number("2"), false},
		{"log10(x)\n", json.Number("10"), false},
		{"x_2 done\n", json.Number("2"), false},
		{"3rd place\n", json.Number("3"), true},
		{"2x\n", json.Number("2"), true},
		{"1e3x\n", json.Number("1000"), true},
		{"0.5mg\n", json.Number("0.5"), true},
		{"5.3ms\n", json.Number("5.3"), true},
		{"5µm\n", json.Number("5"), true},
		{"µ5\n", json.Number("5"), false},
		{"p=0.04\n", json.Number("0.04"), true},
		{"(0.12)\n", json.Number("0.12"), true},
		{"0.12, 0.3\n", json.Number("0.12"), true},
		{"t-stat 5.3\n", json.Number("5.3"), true},
		{"a,-0.5\n", json.Number("-0.5"), true},
		{"it was 7. Next\n", json.Number("7"), true},
		{"12.5%\n", json.Number("12.5"), true},
		{"1e-3\n", json.Number("0.001"), true},
	} {
		t.Run(tc.out, func(t *testing.T) {
			rec := valueRec("r1", "result", map[string]any{"cites": []string{"run:x1"}, "value": tc.value})
			got := MatchValues([]db.Record{rec}, map[string]Run{"x1": {Tool: "run_script", Stdout: tc.out}})
			if len(got) != 1 || got[0].Matched != tc.want {
				t.Fatalf("got %+v, want matched=%v", got, tc.want)
			}
		})
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestAValueOnlyInExplorationIsFlagged(t *testing.T) {
	runs := map[string]Run{"x1": {Tool: "run_code", Stdout: "mean 3.14\n"}, "x2": {Tool: "run_script", Stdout: "mean 2.71\n"}}
	got := MatchValues([]db.Record{valueRec("r1", "result", map[string]any{"cites": []string{"run:x1", "run:x2"}, "value": 3.14})}, runs)
	if len(got) != 1 || got[0].Matched || !got[0].Exploration || got[0].Run != "x1" || got[0].Uncited {
		t.Fatalf("got %+v", got)
	}
}

func TestFreeAndServerKindsAreNotChecked(t *testing.T) {
	got := MatchValues([]db.Record{
		valueRec("r1", "prediction", map[string]any{"value": 0.5}),
		valueRec("s1", "score", map[string]any{"value": 0.5}),
		valueRec("s2", "summary", map[string]any{"value": 0.5}),
		valueRec("r2", "result", map[string]any{"value": 0.5}),
		valueRec("r3", "result", map[string]any{"value": 0.5, "cites": []string{"call:c1"}}),
	}, nil)
	if len(got) != 2 || got[0].Record != "r2" || !got[0].Uncited || got[1].Record != "r3" || got[1].Uncited || !got[1].CallsOnly {
		t.Fatalf("got %+v", got)
	}
}

func TestAValueBeyondTheInlineCutStillMatches(t *testing.T) {
	ctx := context.Background()
	blobs := storage.Local{Dir: t.TempDir()}
	full := strings.Repeat("x", sources.MaxCallAnswer+10) + "\nresult 7.25\n"
	if err := blobs.Put(ctx, "ws/card/x1", strings.NewReader(full), int64(len(full))); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"tool": "run_script", "output": full[:sources.MaxCallAnswer], "truncated": true, "exit_status": 0})
	runs, err := LoadRuns(ctx, blobs, []db.Record{
		{Key: "x1", Kind: "run", Body: body, BlobKey: "ws/card/x1"},
		{Key: "x2", Kind: "run", Body: body, BlobKey: "ws/card/gone"},
		{Key: "c1", Kind: "call", Body: body},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs["x1"].Unreadable || !runs["x2"].Unreadable || runs["x1"].Tool != "run_script" {
		t.Fatalf("runs %+v", runs)
	}
	got := MatchValues([]db.Record{valueRec("r1", "result", map[string]any{"cites": []string{"run:x1"}, "value": 7.25})}, runs)
	if len(got) != 1 || !got[0].Matched {
		t.Fatalf("got %+v", got)
	}
	got = MatchValues([]db.Record{valueRec("r1", "result", map[string]any{"cites": []string{"run:x2"}, "value": 7.25})}, runs)
	if len(got) != 1 || got[0].Matched || got[0].Unchecked != "x2" {
		t.Fatalf("got %+v", got)
	}
}

func TestTheProtocolTellsAgentsAboutRuns(t *testing.T) {
	s := Protocol("agent", "experimentation", "", nil)
	for _, want := range []string{"run_script", "run_code", "`file` names the saved output file or the `run_script` script"} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in:\n%s", want, s)
		}
	}
	if a := Protocol("auditor", "experimentation", "", nil); !strings.Contains(a, "that each reported value appears in the output file or script run it names") {
		t.Errorf("the auditor is not told to check values:\n%s", a)
	}
}

func TestANegativeAfterACommaKeepsItsSign(t *testing.T) {
	runs := map[string]Run{"x1": {Tool: "run_script", Stdout: "a,-0.5\nrange 3-4\n"}}
	for v, want := range map[string]bool{"-0.5": true, "0.5": false, "4": true, "-4": false} {
		got := MatchValues([]db.Record{valueRec("r1", "result", map[string]any{"cites": []string{"run:x1"}, "value": json.Number(v)})}, runs)
		if len(got) != 1 || got[0].Matched != want {
			t.Errorf("%s: got %+v, want matched=%v", v, got, want)
		}
	}
}

func TestRowObjectsInAValuesListUseTheirOwnCites(t *testing.T) {
	runs := map[string]Run{"x1": {Tool: "run_script", Stdout: "a 0.5\n"}, "x2": {Tool: "run_script", Stdout: "b 0.7\n"}}
	rec := valueRec("r1", "result", map[string]any{"cites": []string{"run:x2"}, "values": []any{
		map[string]any{"value": 0.5, "cites": []string{"run:x1"}},
		0.7,
	}})
	got := MatchValues([]db.Record{rec}, runs)
	want := []ValueMatch{{Record: "r1", Value: "0.5", Run: "x1", Matched: true}, {Record: "r1", Value: "0.7", Run: "x2", Matched: true}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestARunWhoseBodyCannotBeReadIsUnreadable(t *testing.T) {
	runs, err := LoadRuns(context.Background(), storage.Local{Dir: t.TempDir()}, []db.Record{{Key: "x1", Kind: "run", Body: []byte(`not json`)}})
	if err != nil || len(runs) != 1 || !runs["x1"].Unreadable {
		t.Fatalf("runs %+v, err %v", runs, err)
	}
}

func TestValuesComeInRecordAndValueOrder(t *testing.T) {
	recs := []db.Record{
		valueRec("r2", "result", map[string]any{"value": 1}),
		valueRec("r1", "result", map[string]any{"b": map[string]any{"value": 3}, "a": map[string]any{"value": 2}, "c": map[string]any{"value": 1}}),
	}
	for range 20 {
		var got []string
		for _, m := range MatchValues(recs, nil) {
			got = append(got, m.Record+"="+m.Value)
		}
		if !slices.Equal(got, []string{"r1=1", "r1=2", "r1=3", "r2=1"}) {
			t.Fatalf("order %v", got)
		}
	}
}

func TestTheValueLinesJevReads(t *testing.T) {
	s := scoringState(scoring{card: db.Card{Number: 1, BoardColumn: "experimentation"}}, nil, []ValueMatch{
		{Record: "r1", Value: "1.5", Run: "x1", Exploration: true},
		{Record: "r2", Value: "2", Uncited: true},
		{Record: "r3", Value: "3"},
		{Record: "r4", Value: "4", Unchecked: "x9"},
		{Record: "r5", Value: "5", CallsOnly: true},
		{Record: "r6", Value: "6", Run: "x2", Failed: true, ExitStatus: -1},
	}, maxJevInput)
	for _, want := range []string{
		`- r1: found only in exploration (run_code) x1, not in a script run: "1.5"`,
		`- r2: cites no run: "2"`,
		`- r3: not found in any run it cites: "3"`,
		`- r4: could not be checked: run x9's full output is unreadable: "4"`,
		`- r5: cites only database calls; values are checked against script runs only: "5"`,
		`- r6: found only in a script run that failed (exit status -1) x2: "6"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "No record reports a value.") {
		t.Error("values reported and none at once")
	}
	if s := scoringState(scoring{card: db.Card{Number: 1, BoardColumn: "experimentation"}}, nil, nil, maxJevInput); !strings.Contains(s, "Value check, done by Lykeion's code:") || !strings.Contains(s, "No record reports a value.") {
		t.Errorf("no empty value check in:\n%s", s)
	}
}

func TestOnlyAScriptRunThatSucceededCounts(t *testing.T) {
	runs := map[string]Run{
		"bad":        {Tool: "run_script", Stdout: "mean 3.14\n", ExitStatus: 1},
		"timeout":    {Tool: "run_script", Stdout: "mean 3.14\n", ExitStatus: -1},
		"good":       {Tool: "run_script", Stdout: "mean 3.14\n"},
		"cell":       {Tool: "run_code", Stdout: "mean 3.14\n"},
		"lost":       {Tool: "run_code", Unreadable: true},
		"lostscript": {Tool: "run_script", Unreadable: true},
		"elsewise":   {Tool: "run_script", Stdout: "mean 2.71\n"},
	}
	for _, tc := range []struct {
		cites []string
		want  ValueMatch
	}{
		{[]string{"run:bad", "run:good"}, ValueMatch{Run: "good", Matched: true}},
		{[]string{"run:bad"}, ValueMatch{Run: "bad", Failed: true, ExitStatus: 1}},
		{[]string{"run:timeout"}, ValueMatch{Run: "timeout", Failed: true, ExitStatus: -1}},
		{[]string{"run:cell", "run:bad"}, ValueMatch{Run: "bad", Failed: true, ExitStatus: 1}},
		{[]string{"run:lost", "run:bad"}, ValueMatch{Run: "bad", Failed: true, ExitStatus: 1}},
		{[]string{"run:cell", "run:elsewise"}, ValueMatch{Run: "cell", Exploration: true}},
		{[]string{"run:elsewise"}, ValueMatch{}},
		{[]string{"run:lostscript", "run:bad"}, ValueMatch{Unchecked: "lostscript"}},
		{[]string{"run:lost", "run:elsewise"}, ValueMatch{}},
	} {
		got := MatchValues([]db.Record{valueRec("r1", "result", map[string]any{"cites": tc.cites, "value": 3.14})}, runs)
		tc.want.Record, tc.want.Value = "r1", "3.14"
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%v: got %+v, want %+v", tc.cites, got, tc.want)
		}
	}
}

func TestAValueCitingOnlyCallsIsNotUncited(t *testing.T) {
	got := MatchValues([]db.Record{
		valueRec("r1", "result", map[string]any{"value": 1, "cites": []string{"call:c1"}}),
		valueRec("r2", "result", map[string]any{"value": 1, "cites": []string{"call:c1", "run:x1"}}),
		valueRec("r3", "result", map[string]any{"value": 1, "cites": []string{}}),
	}, map[string]Run{"x1": {Tool: "run_script", Stdout: "1"}})
	want := []ValueMatch{{Record: "r1", Value: "1", CallsOnly: true}, {Record: "r2", Value: "1", Run: "x1", Matched: true}, {Record: "r3", Value: "1", Uncited: true}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestValueFoundInSavedFile(t *testing.T) {
	agent, other := uuid.New(), uuid.New()
	withRun := func(r db.Record, id uuid.UUID) db.Record { r.AgentRunID = &id; return r }
	work := []db.Record{
		valueRec("r1", "result", map[string]any{"cites": []string{"run:x1"}, "value": 0.91}),
		withRun(valueRec("o1", "output", map[string]any{"name": "metrics.csv"}), agent),
		withRun(valueRec("o2", "output", map[string]any{"name": "other.csv"}), other),
	}
	runs := map[string]Run{"x1": {Tool: "run_script", Stdout: "saved metrics\n", AgentRunID: agent}}
	files := map[string]string{"o1": "metric,value\nauc,0.912\n", "o2": "auc,0.77\n"}
	got := MatchValuesIn(work, runs, files)
	if len(got) != 1 || !got[0].Matched || got[0].File != "o1" || got[0].Run != "x1" {
		t.Fatalf("value in the run's saved file: %+v", got)
	}
	// A cited run that does not count is passed over, not the end of the search.
	runs["x0"] = Run{Tool: "run_code", Stdout: "", AgentRunID: other}
	work[0] = valueRec("r1", "result", map[string]any{"cites": []string{"run:x0", "run:x1"}, "value": 0.91})
	if got := MatchValuesIn(work, runs, files); !got[0].Matched || got[0].File != "o1" {
		t.Fatalf("value after a run that does not count: %+v", got)
	}
	// A file saved by another agent run does not count.
	work[0] = valueRec("r1", "result", map[string]any{"cites": []string{"run:x1"}, "value": 0.77})
	if got := MatchValuesIn(work, runs, files); got[0].Matched || got[0].File != "" {
		t.Fatalf("value only in another run's file: %+v", got)
	}
	// A run that exited non-zero leaves its saved files unchecked.
	runs["x1"] = Run{Tool: "run_script", Stdout: "saved metrics\n", ExitStatus: 1, AgentRunID: agent}
	work[0] = valueRec("r1", "result", map[string]any{"cites": []string{"run:x1"}, "value": 0.91})
	if got := MatchValuesIn(work, runs, files); got[0].Matched {
		t.Fatalf("file of a failed run counted: %+v", got)
	}
	loaded, err := LoadRuns(context.Background(), storage.Local{Dir: t.TempDir()},
		[]db.Record{withRun(valueRec("x1", "run", map[string]any{"tool": "run_script"}), agent)})
	if err != nil || loaded["x1"].AgentRunID != agent {
		t.Fatalf("LoadRuns keeps the agent run: %+v, %v", loaded, err)
	}
}

func TestAValueNamingAFileMatchesThatOutputFromAnyRun(t *testing.T) {
	member, leader := uuid.New(), uuid.New()
	withRun := func(r db.Record, id uuid.UUID) db.Record { r.AgentRunID = &id; return r }
	work := []db.Record{
		withRun(valueRec("o1", "output", map[string]any{"name": "fit.txt"}), member),
		withRun(valueRec("o2", "output", map[string]any{"name": "other.txt"}), member),
		withRun(valueRec("r1", "value", map[string]any{"name": "slope", "value": "0.42", "file": "fit.txt", "cites": []string{}}), leader),
	}
	files := map[string]string{"o1": "slope 0.4213\n", "o2": "slope 0.77\n"}
	got := MatchValuesIn(work, nil, files)
	if len(got) != 1 || !got[0].Matched || got[0].File != "o1" {
		t.Fatalf("value in the named output: %+v", got)
	}
	// Another output holding the number is not the named file.
	work[2] = withRun(valueRec("r1", "value", map[string]any{"name": "slope", "value": "0.77", "file": "fit.txt", "cites": []string{}}), leader)
	if got := MatchValuesIn(work, nil, files); got[0].Matched {
		t.Fatalf("value only in another file: %+v", got)
	}
}
