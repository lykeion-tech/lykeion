package handler

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"lykeion/server/internal/sources"
	db "lykeion/server/pkg/db/generated"
)

func (l *teamLab) blob(t *testing.T, key string) string {
	t.Helper()
	rc, err := l.s.H.Blobs.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("blob %s: %v", key, err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return string(b)
}

// runRecord is the study's live run record under key.
func (l *teamLab) runRecord(t *testing.T, token, key string) (db.Record, map[string]any) {
	t.Helper()
	var recs []db.Record
	if c := l.s.call(t, "GET", "/api/role/records?kind=run", token, "", nil, &recs); c != 200 {
		t.Fatalf("records %d", c)
	}
	for _, rec := range recs {
		if rec.Key == key {
			var body map[string]any
			json.Unmarshal(rec.Body, &body)
			return rec, body
		}
	}
	t.Fatalf("no run record %s in %d", key, len(recs))
	return db.Record{}, nil
}

func TestRunKeysAreSequentialAndApartFromCalls(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	var out struct{ Key string }
	if c := l.s.call(t, "POST", "/api/role/calls", leader.callToken, "", map[string]any{"server": "pubmed", "tool": "search", "answer": "hit"}, &out); c != 201 || out.Key != "c1" {
		t.Fatalf("call %d %+v", c, out)
	}
	for _, want := range []string{"x1", "x2"} {
		in := map[string]any{"tool": "run_code", "language": "python", "code": "print(1)", "output": "1\n", "exit_status": 0}
		if c := l.s.call(t, "POST", "/api/role/runs", leader.callToken, "", in, &out); c != 201 || out.Key != want {
			t.Fatalf("run %d %+v, want %s", c, out, want)
		}
	}
	for _, in := range []map[string]any{
		{"tool": "run_shell", "language": "python", "code": "x"},
		{"tool": "run_code", "language": "fortran", "code": "x"},
	} {
		if c := l.s.call(t, "POST", "/api/role/runs", leader.callToken, "", in, nil); c != 400 {
			t.Fatalf("%v: %d", in, c)
		}
	}
	var runs []map[string]any
	if c := l.s.call(t, "GET", "/api/role/runs", leader.token, "", nil, &runs); c != 200 || len(runs) != 2 {
		t.Fatalf("runs %d %+v", c, runs)
	}
	for i, want := range []string{"x1", "x2"} {
		if runs[i]["key"] != want || runs[i]["tool"] != "run_code" || runs[i]["language"] != "python" {
			t.Fatalf("run %d: %+v", i, runs[i])
		}
	}
}

func TestALongRunOutputGoesWholeToABlob(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	output := strings.Repeat("é", sources.MaxCallAnswer)
	in := map[string]any{"tool": "run_script", "language": "python", "path": "analysis/x.py", "script": "print('é')", "output": output, "exit_status": 0}
	if c := l.s.call(t, "POST", "/api/role/runs", leader.callToken, "", in, nil); c != 201 {
		t.Fatalf("run %d", c)
	}
	rec, body := l.runRecord(t, leader.token, "x1")
	kept, _ := body["output"].(string)
	if !utf8.ValidString(kept) || len(kept) > sources.MaxCallAnswer || !strings.HasPrefix(output, kept) || kept == "" {
		t.Fatalf("output keeps %d bytes", len(kept))
	}
	if body["truncated"] != true {
		t.Fatal("a cut output is not marked truncated")
	}
	if rec.BlobKey == "" || l.blob(t, rec.BlobKey) != output {
		t.Fatalf("blob %q does not hold the whole output", rec.BlobKey)
	}
	if body["output_sha256"] != hexSHA(output) {
		t.Fatalf("output_sha256 %v", body["output_sha256"])
	}
	if _, ok := body["code"]; ok {
		t.Fatal("a run_script record keeps code")
	}
}

func TestARunRecordsItsScriptAndPackages(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	script := "import numpy\nprint(numpy.pi)\n"
	packages := "# python base\nnumpy==2.1.0\n# python study\nscanpy==1.10.0\n"
	in := map[string]any{"tool": "run_script", "language": "python", "path": "analysis/x.py", "script": script, "output": "3.14\n", "exit_status": 0, "packages": packages}
	if c := l.s.call(t, "POST", "/api/role/runs", leader.callToken, "", in, nil); c != 201 {
		t.Fatalf("run %d", c)
	}
	_, body := l.runRecord(t, leader.token, "x1")
	if body["script_sha256"] != hexSHA(script) {
		t.Fatalf("script_sha256 %v", body["script_sha256"])
	}
	if body["packages_sha256"] != hexSHA(packages) || l.blob(t, l.ws+"/"+l.card.ID+"/"+hexSHA(packages)) != packages {
		t.Fatalf("packages blob: %v", body["packages_sha256"])
	}
	if body["packages_study"] != "scanpy==1.10.0\n" {
		t.Fatalf("packages_study %q", body["packages_study"])
	}
	if body["truncated"] != false || body["script"] != script || body["path"] != "analysis/x.py" {
		t.Fatalf("body %+v", body)
	}
	in["script"] = strings.Repeat("a", 256<<10+1)
	var e struct{ Error string }
	if c := l.s.call(t, "POST", "/api/role/runs", leader.callToken, "", in, &e); c != 400 || e.Error != "the script is over 256 KiB" {
		t.Fatalf("long script: %d %q", c, e.Error)
	}
}

func TestMembersCannotWriteRunRecords(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	var e struct{ Error string }
	in := map[string]any{"kind": "run", "body": map[string]any{"tool": "run_script", "output": "42"}}
	if c := l.s.call(t, "POST", "/api/role/records", leader.token, "", in, &e); c != 400 || e.Error != "run records are written by the server" {
		t.Fatalf("put run: %d %q", c, e.Error)
	}
}

func TestTheRoleTokenCannotPostRuns(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	in := map[string]any{"tool": "run_code", "language": "python", "code": "print(1)", "output": "1\n"}
	if c := l.s.call(t, "POST", "/api/role/runs", leader.token, "", in, nil); c != 403 {
		t.Fatalf("role token posts a run: %d", c)
	}
}
