package flow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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

func TestAFreeKindIsNotChecked(t *testing.T) {
	got := MatchValues([]db.Record{
		valueRec("r1", "prediction", map[string]any{"value": 0.5}),
		valueRec("r2", "result", map[string]any{"value": 0.5}),
		valueRec("r3", "result", map[string]any{"value": 0.5, "cites": []string{"call:c1"}}),
	}, nil)
	if len(got) != 2 || got[0].Record != "r2" || !got[0].Uncited || got[1].Record != "r3" || !got[1].Uncited {
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
	for _, want := range []string{"run_script", "run_code", "lyk runs", `"cites": ["run:KEY"]`} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in:\n%s", want, s)
		}
	}
	if a := Protocol("auditor", "experimentation", "", nil); !strings.Contains(a, "that each reported value appears in the script run it cites") {
		t.Errorf("the auditor is not told to check values:\n%s", a)
	}
}
