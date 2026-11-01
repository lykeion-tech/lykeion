package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"

	"lykeion/server/internal/sources"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// blob reads a blob through the role's blobs route, which serves only blobs
// whose blob.created was committed.
func (l *teamLab) blob(t *testing.T, token, sum string) string {
	t.Helper()
	req, _ := http.NewRequest("GET", l.s.URL+"/api/role/blobs/"+sum, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("blob %s: %d %s", sum, resp.StatusCode, b)
	}
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
	for _, in := range []map[string]any{
		{"tool": "run_code", "language": "python", "code": "print(1)", "output": "1\n", "exit_status": 0},
		{"tool": "run_script", "language": "r", "path": "a.R", "script": "cat(1)", "output": strings.Repeat("o", 3000), "error": "warn", "exit_status": 1},
	} {
		want := map[string]string{"run_code": "x1", "run_script": "x2"}[in["tool"].(string)]
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
	if r := runs[0]; r["key"] != "x1" || r["tool"] != "run_code" || r["language"] != "python" || r["exit_status"] != 0.0 || r["at"] == nil || r["truncated"] != false {
		t.Fatalf("x1: %+v", r)
	}
	if r := runs[1]; r["key"] != "x2" || r["tool"] != "run_script" || r["language"] != "r" || r["path"] != "a.R" || r["exit_status"] != 1.0 ||
		r["output"] != strings.Repeat("o", 2000) || r["truncated"] != true {
		t.Fatalf("x2: %+v", r)
	}
	for _, r := range runs {
		for _, f := range []string{"script", "error", "code", "packages_study", "agent_run_id"} {
			if _, ok := r[f]; ok {
				t.Fatalf("the listing carries %s: %+v", f, r)
			}
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
	if rec.BlobKey != l.ws+"/"+l.card.ID+"/"+hexSHA(output) || l.blob(t, leader.token, hexSHA(output)) != output {
		t.Fatalf("blob %q does not hold the whole output", rec.BlobKey)
	}
	if body["output_sha256"] != hexSHA(output) {
		t.Fatalf("output_sha256 %v", body["output_sha256"])
	}
	if _, ok := body["code"]; ok {
		t.Fatal("a run_script record keeps code")
	}
}

func TestALongRunErrorGoesWholeToABlob(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	errText := strings.Repeat("é", sources.MaxCallAnswer)
	in := map[string]any{"tool": "run_code", "language": "python", "code": "raise", "output": "short", "error": errText, "exit_status": 1}
	if c := l.s.call(t, "POST", "/api/role/runs", leader.callToken, "", in, nil); c != 201 {
		t.Fatalf("run %d", c)
	}
	rec, body := l.runRecord(t, leader.token, "x1")
	kept, _ := body["error"].(string)
	if !utf8.ValidString(kept) || len(kept) > sources.MaxCallAnswer || !strings.HasPrefix(errText, kept) || kept == "" {
		t.Fatalf("error keeps %d bytes", len(kept))
	}
	if body["error_truncated"] != true || body["truncated"] != false {
		t.Fatalf("error_truncated %v, truncated %v", body["error_truncated"], body["truncated"])
	}
	if body["error_sha256"] != hexSHA(errText) || body["error_blob"] != l.ws+"/"+l.card.ID+"/"+hexSHA(errText) {
		t.Fatalf("error_sha256 %v, error_blob %v", body["error_sha256"], body["error_blob"])
	}
	if l.blob(t, leader.token, hexSHA(errText)) != errText {
		t.Fatal("the error blob does not hold the whole error")
	}
	if rec.BlobKey != "" {
		t.Fatalf("an uncut output has blob %q", rec.BlobKey)
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
	if body["packages_sha256"] != hexSHA(packages) || l.blob(t, leader.token, hexSHA(packages)) != packages {
		t.Fatalf("packages blob: %v", body["packages_sha256"])
	}
	if body["packages_study"] != "scanpy==1.10.0\n" {
		t.Fatalf("packages_study %q", body["packages_study"])
	}
	if body["truncated"] != false || body["script"] != script || body["path"] != "analysis/x.py" {
		t.Fatalf("body %+v", body)
	}
	bare := map[string]any{"tool": "run_code", "language": "python", "code": "1", "output": "1", "exit_status": 0}
	if c := l.s.call(t, "POST", "/api/role/runs", leader.callToken, "", bare, nil); c != 201 {
		t.Fatalf("run without packages %d", c)
	}
	if _, body := l.runRecord(t, leader.token, "x2"); body["packages_sha256"] != nil {
		t.Fatalf("a run with no package list names one: %v", body["packages_sha256"])
	}
	for _, c := range []struct {
		field string
		value any
		want  string
	}{
		{"script", strings.Repeat("a", 256<<10+1), "the script is over 256 KiB"},
		{"code", strings.Repeat("a", 256<<10+1), "the code is over 256 KiB"},
		{"exit_status", nil, "exit_status is required"},
		{"exit_status", "zero", "invalid run: "},
	} {
		bad := map[string]any{}
		for k, v := range in {
			bad[k] = v
		}
		bad[c.field] = c.value
		if c.value == nil {
			delete(bad, c.field)
		}
		var e struct{ Error string }
		if code := l.s.call(t, "POST", "/api/role/runs", leader.callToken, "", bad, &e); code != 400 || !strings.HasPrefix(e.Error, c.want) {
			t.Fatalf("%s: %d %q", c.field, code, e.Error)
		}
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

func TestARecordMayCiteARun(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	if c := l.s.call(t, "POST", "/api/role/calls", leader.callToken, "", map[string]any{"server": "pubmed", "tool": "search", "answer": "hit"}, nil); c != 201 {
		t.Fatalf("call %d", c)
	}
	if c := l.s.call(t, "POST", "/api/role/runs", leader.callToken, "", map[string]any{"tool": "run_code", "language": "python", "code": "print(1)", "output": "1\n", "exit_status": 0}, nil); c != 201 {
		t.Fatalf("run %d", c)
	}
	post := func(cites ...string) (int, string) {
		var e struct{ Error string }
		c := l.s.call(t, "POST", "/api/role/records", leader.token, "", map[string]any{"kind": "rival", "body": map[string]any{"cites": cites}}, &e)
		return c, e.Error
	}
	if c, e := post("run:x1", "call:c1"); c != 201 {
		t.Fatalf("a run and a call cited: %d %s", c, e)
	}
	if c, e := post("run:x9"); c != 400 || e != "no recorded call, run or record has the key x9" {
		t.Fatalf("an unrecorded run: %d %q", c, e)
	}
	if c, _ := post("run:c1"); c != 400 {
		t.Fatalf("a call key under run: %d", c)
	}
}

func TestCardBlobKeysListEachKeyOnce(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	in := map[string]any{"tool": "run_code", "language": "python", "code": "1", "output": "1", "exit_status": 0, "packages": "# python base\nnumpy==2.1.0\n"}
	for range 2 {
		if c := l.s.call(t, "POST", "/api/role/runs", leader.callToken, "", in, nil); c != 201 {
			t.Fatalf("run %d", c)
		}
	}
	var keys []string
	err := l.s.H.Store.InWorkspace(t.Context(), uuid.MustParse(l.ws), uuid.Nil, func(tx *store.Tx) error {
		var err error
		keys, err = tx.Q.CardBlobKeys(t.Context(), db.CardBlobKeysParams{WorkspaceID: tx.WS, CardID: uuid.MustParse(l.card.ID)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			t.Fatalf("key %s listed twice in %v", k, keys)
		}
		seen[k] = true
	}
	if len(keys) == 0 {
		t.Fatal("no keys")
	}
}

func postScriptRun(t *testing.T, l *teamLab, extra map[string]any, tool string) map[string]any {
	t.Helper()
	leader := l.claim(t)
	in := map[string]any{"tool": tool, "language": "python", "path": "a.py", "script": "1", "code": "1", "output": "", "exit_status": 0}
	for k, v := range extra {
		in[k] = v
	}
	if c := l.s.call(t, "POST", "/api/role/runs", leader.callToken, "", in, nil); c != 201 {
		t.Fatalf("run %d", c)
	}
	_, body := l.runRecord(t, leader.token, "x1")
	return body
}

func TestPostRunIgnoresResultsFromOlderDaemons(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	in := map[string]any{"tool": "run_code", "language": "python", "code": "1", "output": "1", "exit_status": 0,
		"results": []any{map[string]any{"name": "x"}}, "results_error": "old"}
	if c := l.s.call(t, "POST", "/api/role/runs", leader.callToken, "", in, nil); c != 201 {
		t.Fatalf("run %d", c)
	}
	if _, body := l.runRecord(t, leader.token, "x1"); body["results"] != nil || body["results_error"] != nil {
		t.Fatalf("the record keeps results: %+v", body)
	}
}

func TestAHypothesisMayCiteAGroundingRecord(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	// The paper Grounding declared, as that column left it.
	err := l.s.H.Store.InWorkspace(t.Context(), uuid.MustParse(l.ws), uuid.Nil, func(tx *store.Tx) error {
		_, err := tx.Q.InsertRecord(t.Context(), db.InsertRecordParams{WorkspaceID: tx.WS, CardID: uuid.MustParse(l.card.ID), BoardColumn: "grounding",
			Kind: "paper", Role: "leader", Key: "r1", Body: json.RawMessage(`{"id":"arxiv:2106.01234","text":"a paper","cites":[]}`)})
		if err != nil {
			return err
		}
		// An agent's own prose in the Hypotheses column.
		_, err = tx.Q.InsertRecord(t.Context(), db.InsertRecordParams{WorkspaceID: tx.WS, CardID: uuid.MustParse(l.card.ID), BoardColumn: "hypotheses",
			Kind: "explanation", Role: "leader", Key: "r2", Body: json.RawMessage(`{"text":"telomeres explain it"}`)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	post := func(cites ...string) (int, string) {
		var e struct{ Error string }
		body := map[string]any{"text": "h", "prediction": map[string]any{"shape": "direction", "direction": "increase"},
			"control": map[string]any{"none": "no baseline"}, "cites": cites}
		c := l.s.call(t, "POST", "/api/role/records", leader.token, "", map[string]any{"kind": "hypothesis", "body": body}, &e)
		return c, e.Error
	}
	if c, e := post("r1"); c != 201 {
		t.Fatalf("a hypothesis citing the Grounding paper: %d %s", c, e)
	}
	if c, e := post("r999"); c != 400 || e != "no recorded call, run or record has the key r999" {
		t.Fatalf("an unknown record key: %d %q", c, e)
	}
	if c, e := post("r1", "r2"); c != 400 || e != "a hypothesis cites r2, which is not a Grounding paper or dataset; cite those, or a call:KEY" {
		t.Fatalf("a hypothesis citing an explanation: %d %q", c, e)
	}
	if c, _ := post("paper"); c != 400 {
		t.Fatalf("a cite that is no key: %d", c)
	}
}

func TestAWithdrawalNamesAnItemOrAReportedValue(t *testing.T) {
	l := newTeamLab(t, 0)
	leader := l.claim(t)
	err := l.s.H.Store.InWorkspace(t.Context(), uuid.MustParse(l.ws), uuid.Nil, func(tx *store.Tx) error {
		for _, r := range []db.InsertRecordParams{
			{BoardColumn: "hypotheses", Kind: "hypothesis", Key: "r1", Body: json.RawMessage(`{"text":"h"}`)},
			{BoardColumn: "hypotheses", Kind: "result", Key: "r2", Body: json.RawMessage(`{"text":"The effect is 0.12.","value":0.12}`)},
			{BoardColumn: "hypotheses", Kind: "explanation", Key: "r3", Body: json.RawMessage(`{"text":"telomeres explain it"}`)},
		} {
			r.WorkspaceID, r.CardID, r.Role = tx.WS, uuid.MustParse(l.card.ID), "leader"
			if _, err := tx.Q.InsertRecord(t.Context(), r); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	post := func(item string) (int, string) {
		var e struct{ Error string }
		c := l.s.call(t, "POST", "/api/role/records", leader.token, "", map[string]any{"kind": "withdrawn", "body": map[string]any{"item": item, "reason": "dropped"}}, &e)
		return c, e.Error
	}
	for _, k := range []string{"r1", "r2"} {
		if c, e := post(k); c != 201 {
			t.Fatalf("withdrawing %s: %d %s", k, c, e)
		}
	}
	if c, e := post("r3"); c != 400 || e != "withdrawn names r3, of kind explanation: name an item record or a record that reports values" {
		t.Fatalf("withdrawing an explanation: %d %q", c, e)
	}
	if c, e := post("r999"); c != 400 || e != "no record has the key r999" {
		t.Fatalf("withdrawing an unknown key: %d %q", c, e)
	}
	if c, _ := post("arxiv:2106.01234"); c != 400 {
		t.Fatalf("withdrawing what is no key: %d", c)
	}
}
