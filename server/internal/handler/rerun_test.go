package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"mime/multipart"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
)

type rerunLab struct {
	s                 *server
	jwt, ws, personal string
	rts               string
	owner, card       uuid.UUID
}

// newRerunLab is a study at Validation round 1 whose Experimentation round 1
// is seeded by the test, and a registered daemon to replay it.
func newRerunLab(t *testing.T) *rerunLab {
	t.Helper()
	s := newServer(t)
	l := &rerunLab{s: s, jwt: s.signIn(t, "pi@lab.org")}
	l.ws = s.workspace(t, l.jwt, "lab").ID
	l.personal = s.personalToken(t, l.jwt)
	var rts []struct{ ID string }
	if c := s.call(t, "POST", "/api/daemon/register", l.personal, "", map[string]any{"workspace_id": l.ws, "daemon_id": "d1", "harnesses": []string{"claude"}}, &rts); c != 200 || len(rts) != 1 {
		t.Fatalf("register %d %+v", c, rts)
	}
	l.rts = rts[0].ID
	l.card = uuid.MustParse(s.idleCard(t, l.jwt, l.ws, "Why do cells age?").ID)
	l.owner = uuid.MustParse(s.userID(t, l.jwt))
	return l
}

// record stores a record on the study as the server would.
func (l *rerunLab) record(t *testing.T, column string, kind, key string, body any, run *uuid.UUID) {
	t.Helper()
	b, _ := json.Marshal(body)
	if _, err := l.s.Env.Owner.Exec(t.Context(), `INSERT INTO record (workspace_id, card_id, board_column, round, kind, role, key, body, agent_run_id)
		VALUES ($1, $2, $3, 1, $4, 'server', $5, $6, $7)`, l.ws, l.card, column, kind, key, b, run); err != nil {
		t.Fatal(err)
	}
}

func (l *rerunLab) script(t *testing.T, key, path string, exit int, packages string) {
	t.Helper()
	body := map[string]any{"tool": "run_script", "language": "python", "path": path, "script": "# " + path, "exit_status": exit, "output": "0.873"}
	if packages != "" {
		sum := hexSHA(packages)
		if err := l.s.H.Blobs.Put(t.Context(), l.ws+"/"+l.card.String()+"/"+sum, strings.NewReader(packages), int64(len(packages))); err != nil {
			t.Fatal(err)
		}
		body["packages_sha256"] = sum
	}
	l.record(t, "experimentation", "run", key, body, nil)
}

// enqueue moves the study to Validation and queues its rerun job.
func (l *rerunLab) enqueue(t *testing.T) {
	t.Helper()
	if _, err := l.s.Env.Owner.Exec(t.Context(), "UPDATE card SET board_column = 'validation', round = 1 WHERE id = $1", l.card); err != nil {
		t.Fatal(err)
	}
	if err := l.s.H.Store.InWorkspace(t.Context(), uuid.MustParse(l.ws), l.owner, func(tx *store.Tx) error {
		_, err := l.s.H.AgentRuns.EnqueueRerun(t.Context(), tx, l.owner, l.card, 1)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

type rerunClaim struct {
	AgentRun struct {
		ID      string
		Kind    string
		Attempt int32
	} `json:"agent_run"`
	Token string
	Rerun *struct {
		Steps []struct {
			Key, Path, Language, Script, Packages string
			OutputPaths                           []string `json:"output_paths"`
		}
		Python      string
		Attachments []any
		Inputs      []service.ReplayInput
		Saved       []struct{ Name, Sha256 string }
	}
	ref map[string]any
}

// claim claims and starts the queued rerun on the lab's runtime.
func (l *rerunLab) claim(t *testing.T) rerunClaim {
	t.Helper()
	ref := map[string]any{"workspace_id": l.ws, "runtime_id": l.rts}
	var claims []rerunClaim
	if c := l.s.call(t, "POST", "/api/daemon/agent-runs/claim", l.personal, "", map[string]any{"runtimes": []any{ref}, "slots": 1}, &claims); c != 200 || len(claims) != 1 {
		t.Fatalf("claim %d %+v", c, claims)
	}
	cl := claims[0]
	ref["attempt"] = cl.AgentRun.Attempt
	if c := l.s.call(t, "POST", "/api/daemon/agent-runs/"+cl.AgentRun.ID+"/start", l.personal, "", ref, nil); c != 204 {
		t.Fatalf("start %d", c)
	}
	cl.ref = ref
	return cl
}

func (l *rerunLab) result(t *testing.T, cl rerunClaim, in map[string]any) int {
	t.Helper()
	body := maps.Clone(cl.ref)
	maps.Copy(body, in)
	return l.s.call(t, "POST", "/api/daemon/reruns/"+cl.AgentRun.ID+"/result", l.personal, "", body, nil)
}

type rerunRecord struct {
	Steps []struct {
		Of           string
		ExitStatus   int    `json:"exit_status"`
		OutputSHA256 string `json:"output_sha256"`
	}
	Outputs    []map[string]string
	Reproduced map[string]bool
	Notes      map[string]string
	Failure    *string
}

func (l *rerunLab) rerunRecords(t *testing.T) []rerunRecord {
	t.Helper()
	rows, err := l.s.Env.Owner.Query(t.Context(), "SELECT body FROM record WHERE card_id = $1 AND kind = 'rerun' AND board_column = 'validation' ORDER BY created_at", l.card)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []rerunRecord
	for rows.Next() {
		var b []byte
		var r rerunRecord
		if err := rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func TestRerunClaimCarriesScriptsInOrder(t *testing.T) {
	l := newRerunLab(t)
	l.record(t, "experimentation", "python_version", "", map[string]string{"version": "3.12"}, nil)
	l.script(t, "x1", "prep.py", 0, "# python base\nnumpy==2.0\n# python study\nscipy==1.13\n")
	l.record(t, "experimentation", "run", "x2", map[string]any{"tool": "run_code", "language": "python", "code": "print(1)", "exit_status": 0, "output": "1"}, nil)
	l.script(t, "x3", "fit.py", 0, "# python base\nnumpy==2.0\n# python study\nscipy==1.14\n")
	l.script(t, "x4", "broken.py", 1, "")
	l.script(t, "x5", "unused.py", 0, "")
	// Only the scripts the standing values rest on are replayed, and only
	// the runs of them that exited 0.
	for i, run := range []string{"x1", "x3", "x4"} {
		l.record(t, "experimentation", "value", fmt.Sprintf("r%d", i+1), map[string]any{"name": run, "value": "1", "file": "f", "cites": []string{"run:" + run}}, nil)
	}
	l.enqueue(t)
	cl := l.claim(t)
	if cl.AgentRun.Kind != "rerun" || cl.Rerun == nil {
		t.Fatalf("claim %+v", cl)
	}
	var got []string
	for _, s := range cl.Rerun.Steps {
		got = append(got, s.Key+" "+s.Path+" "+s.Language+" "+s.Script)
	}
	if want := []string{"x1 prep.py python # prep.py", "x3 fit.py python # fit.py"}; !slices.Equal(got, want) {
		t.Fatalf("steps %q, want %q", got, want)
	}
	if cl.Rerun.Python != "3.12" || !strings.Contains(cl.Rerun.Steps[0].Packages, "scipy==1.13") || !strings.Contains(cl.Rerun.Steps[1].Packages, "scipy==1.14") {
		t.Fatalf("python %q steps %+v", cl.Rerun.Python, cl.Rerun.Steps)
	}
	if cl.Rerun.Saved == nil || len(cl.Rerun.Saved) != 0 {
		t.Fatalf("a first attempt has saved %+v", cl.Rerun.Saved)
	}
}

func TestRerunReadsOnlyItsPinnedInputOutput(t *testing.T) {
	l := newRerunLab(t)
	var producer uuid.UUID
	if err := l.s.Env.Owner.QueryRow(t.Context(), `INSERT INTO agent_run
		(workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status)
		VALUES ($1, $2, $3, 'experimentation', 1, 'leader', 'codex', 'score', 'completed') RETURNING id`,
		l.ws, l.card, l.owner).Scan(&producer); err != nil {
		t.Fatal(err)
	}
	l.record(t, "experimentation", "run", "x1", map[string]any{
		"tool": "run_script", "language": "python", "path": "outputs/score.py", "script": "# score", "exit_status": 0,
	}, &producer)
	l.record(t, "experimentation", "value", "r1", map[string]any{
		"name": "accuracy", "value": "1", "file": "outputs/score.json", "cites": []string{"run:x1"},
	}, &producer)
	content := "VALUE = 1\n"
	sum := hexSHA(content)
	l.record(t, "experimentation", "output", "o1", map[string]any{"name": "outputs/helper.py", "sha256": sum, "size_bytes": len(content)}, &producer)
	l.record(t, "experimentation", "output", "o2", map[string]any{"name": "outputs/score.json", "sha256": sum, "size_bytes": len(content)}, &producer)
	blob := l.ws + "/" + l.card.String() + "/" + sum
	if err := l.s.H.Blobs.Put(t.Context(), blob, strings.NewReader(content), int64(len(content))); err != nil {
		t.Fatal(err)
	}
	l.s.mustUpdate(t, "UPDATE record SET blob_key = $1 WHERE card_id = $2 AND kind = 'output'", blob, l.card)
	l.enqueue(t)
	cl := l.claim(t)
	if got := cl.Rerun.Inputs; len(got) != 1 || got[0].Key != "o1" || got[0].Sha256 != sum {
		t.Fatalf("inputs %+v", got)
	}
	if status := l.s.call(t, "GET", "/api/role/outputs/o1", cl.Token, "", nil, nil); status != 200 {
		t.Fatalf("pinned input status %d", status)
	}
	if status := l.s.call(t, "GET", "/api/role/outputs/o2", cl.Token, "", nil, nil); status != 404 {
		t.Fatalf("measured output accessible as an input: %d", status)
	}
}

// upload saves text as an output of the rerun, as its daemon does.
func (l *rerunLab) upload(t *testing.T, token, name, text string) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	_ = mw.WriteField("path", name)
	part, _ := mw.CreateFormFile("file", "upload")
	_, _ = part.Write([]byte(text))
	_ = mw.Close()
	req, _ := http.NewRequest("POST", l.s.URL+"/api/role/outputs", &b)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("upload %d", resp.StatusCode)
	}
}

func TestRerunResultMarksReproduced(t *testing.T) {
	l := newRerunLab(t)
	l.script(t, "x1", "fit.py", 0, "")
	l.script(t, "x2", "eval.py", 0, "")
	l.record(t, "experimentation", "value", "r1", map[string]any{"name": "auc", "value": "0.873", "file": "fit.py", "cites": []string{"run:x1"}}, nil)
	l.record(t, "experimentation", "value", "r2", map[string]any{"name": "acc", "value": "0.912", "file": "eval.py", "cites": []string{"run:x2"}}, nil)
	const metrics = "outputs/metrics.txt"
	l.record(t, "experimentation", "value", "r3", map[string]any{"name": "f1", "value": "1.5", "file": metrics, "cites": []string{"run:x1"}}, nil)
	l.enqueue(t)
	cl := l.claim(t)
	if !slices.Equal(cl.Rerun.Steps[0].OutputPaths, []string{metrics}) || len(cl.Rerun.Steps[1].OutputPaths) != 0 {
		t.Fatalf("wrong per-script evidence requested: %+v", cl.Rerun.Steps)
	}
	l.upload(t, cl.Token, metrics, "f1 = 1.50\n")
	if c := l.s.call(t, "POST", "/api/role/records", cl.Token, "", map[string]any{"kind": "value", "body": map[string]any{"name": "acc", "value": "0.912"}}, nil); c != 403 {
		t.Fatalf("a run wrote its own rerun record: %d", c)
	}
	out := map[string]any{"steps": []map[string]any{
		{"key": "x1", "exit_status": 0, "output": "auc=0.8731\n", "error": "", "output_hashes": map[string]string{metrics: hexSHA("f1 = 1.50\n")}},
		{"key": "x2", "exit_status": 0, "output": "acc 0.874\n", "error": ""},
		{"key": "x9", "exit_status": 0, "output": "0.912\n", "error": ""},
	}, "outputs": []map[string]string{{"name": metrics, "text_sha256": hexSHA("f1 = 1.50\n")}}, "failure": nil}
	if c := l.result(t, cl, out); c != 200 {
		t.Fatalf("result %d", c)
	}
	recs := l.rerunRecords(t)
	if len(recs) != 1 {
		t.Fatalf("%d rerun records", len(recs))
	}
	r := recs[0]
	if !maps.Equal(r.Reproduced, map[string]bool{"auc": true, "acc": false, "f1": true}) {
		t.Errorf("reproduced %v", r.Reproduced)
	}
	if !maps.Equal(r.Notes, map[string]string{"acc": "rerun printed 0.874, reported 0.912"}) {
		t.Errorf("notes %v", r.Notes)
	}
	if len(r.Steps) != 2 || r.Steps[0].Of != "x1" || r.Steps[0].OutputSHA256 != hexSHA("auc=0.8731\n") || r.Failure != nil {
		t.Errorf("record %+v", r)
	}
	if n := l.s.count(t, "SELECT count(*) FROM agent_run WHERE id = $1 AND status = 'completed'", cl.AgentRun.ID); n != 1 {
		t.Errorf("the rerun did not complete")
	}
	// The daemon posting the same result again writes nothing new.
	if c := l.result(t, cl, out); c != 200 {
		t.Fatalf("replay %d", c)
	}
	if n := len(l.rerunRecords(t)); n != 1 {
		t.Fatalf("a replay left %d records", n)
	}
	// An output the rerun did not save is refused.
	l2 := newRerunLab(t)
	l2.script(t, "x1", "fit.py", 0, "")
	l2.enqueue(t)
	cl2 := l2.claim(t)
	if c := l2.result(t, cl2, map[string]any{"steps": []any{}, "outputs": []map[string]string{{"name": "made-up.txt", "text_sha256": hexSHA("x")}}, "failure": nil}); c != 400 {
		t.Fatalf("an unsaved output gave %d", c)
	}
}

func TestRerunInfraFailureIsRetried(t *testing.T) {
	l := newRerunLab(t)
	l.script(t, "x1", "fit.py", 0, "")
	l.record(t, "experimentation", "value", "r1", map[string]any{"name": "auc", "value": "0.873", "file": "fit.py", "cites": []string{"run:x1"}}, nil)
	l.enqueue(t)
	for _, failure := range []string{"machine_offline", "sandbox"} {
		cl := l.claim(t)
		if c := l.result(t, cl, map[string]any{"steps": []any{}, "outputs": []any{}, "failure": failure}); c != 200 {
			t.Fatalf("%s: result %d", failure, c)
		}
		if n := len(l.rerunRecords(t)); n != 0 {
			t.Fatalf("%s wrote %d rerun records", failure, n)
		}
		if n := l.s.count(t, "SELECT count(*) FROM agent_run WHERE id = $1 AND status = 'queued' AND retry_after IS NOT NULL AND infra_failures > 0", cl.AgentRun.ID); n != 1 {
			t.Fatalf("%s: the rerun was not parked for a retry", failure)
		}
		if _, err := l.s.Env.Owner.Exec(t.Context(), "UPDATE agent_run SET retry_after = NULL WHERE id = $1", cl.AgentRun.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := l.s.Env.Owner.Exec(t.Context(), "UPDATE runtime SET unhealthy_reason = NULL WHERE id = $1", l.rts); err != nil {
			t.Fatal(err)
		}
	}
	cl := l.claim(t)
	if c := l.result(t, cl, map[string]any{"steps": []any{}, "outputs": []any{}, "failure": "environment"}); c != 200 {
		t.Fatalf("environment: result %d", c)
	}
	recs := l.rerunRecords(t)
	if len(recs) != 1 || !maps.Equal(recs[0].Reproduced, map[string]bool{"auc": false}) ||
		recs[0].Notes["auc"] != "the saved package list could not be rebuilt" || recs[0].Failure == nil || *recs[0].Failure != "environment" {
		t.Fatalf("records %+v", recs)
	}
	if c := l.result(t, cl, map[string]any{"steps": []any{}, "outputs": []any{}, "failure": "bored"}); c != 400 {
		t.Fatalf("an unknown failure gave %d", c)
	}
}

func TestRerunResultRefusedForOtherRun(t *testing.T) {
	l := newRerunLab(t)
	l.script(t, "x1", "fit.py", 0, "")
	l.enqueue(t)
	cl := l.claim(t)
	ok := map[string]any{"steps": []any{}, "outputs": []any{}, "failure": nil}
	// A colleague's own workspace and daemon on the same server.
	otherJWT := l.s.signIn(t, "colleague@lab.org")
	otherWS := l.s.workspace(t, otherJWT, "elsewhere").ID
	otherPersonal := l.s.personalToken(t, otherJWT)
	var rts []struct{ ID string }
	if c := l.s.call(t, "POST", "/api/daemon/register", otherPersonal, "", map[string]any{"workspace_id": otherWS, "daemon_id": "d2", "harnesses": []string{"claude"}}, &rts); c != 200 || len(rts) != 1 {
		t.Fatalf("register %d", c)
	}
	wrongWS := maps.Clone(cl.ref)
	wrongWS["workspace_id"] = otherWS
	theirs := map[string]any{"workspace_id": otherWS, "runtime_id": rts[0].ID, "attempt": 1}
	for name, path := range map[string]string{"unknown run": uuid.NewString(), "not a rerun": ""} {
		if path == "" {
			var id string
			if err := l.s.Env.Owner.QueryRow(t.Context(), `INSERT INTO agent_run (workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt)
				VALUES ($1, $2, $3, 'validation', 1, 'leader', 'claude', 'p') RETURNING id`, l.ws, l.card, l.owner).Scan(&id); err != nil {
				t.Fatal(err)
			}
			path = id
		}
		body := maps.Clone(cl.ref)
		maps.Copy(body, ok)
		if c := l.s.call(t, "POST", "/api/daemon/reruns/"+path+"/result", l.personal, "", body, nil); c != 404 {
			t.Errorf("%s: %d, want 404", name, c)
		}
	}
	for name, call := range map[string]struct {
		token string
		ref   map[string]any
	}{"wrong workspace": {l.personal, wrongWS}, "a colleague's daemon on its own runtime": {otherPersonal, theirs}, "a colleague's daemon on this runtime": {otherPersonal, cl.ref}} {
		body := maps.Clone(call.ref)
		maps.Copy(body, ok)
		if c := l.s.call(t, "POST", "/api/daemon/reruns/"+cl.AgentRun.ID+"/result", call.token, "", body, nil); c != 404 {
			t.Errorf("%s: %d, want 404", name, c)
		}
	}
	if n := len(l.rerunRecords(t)); n != 0 {
		t.Fatalf("refused results wrote %d records", n)
	}
}

func TestAFailedRerunRecordsNothingReproduced(t *testing.T) {
	l := newRerunLab(t)
	l.script(t, "x1", "fit.py", 0, "")
	l.record(t, "experimentation", "value", "r1", map[string]any{"name": "auc", "value": "0.873", "file": "fit.py", "cites": []string{"run:x1"}}, nil)
	l.enqueue(t)
	cl := l.claim(t)
	body := maps.Clone(cl.ref)
	body["failure_reason"], body["error"] = "agent_error", "the daemon crashed"
	if c := l.s.call(t, "POST", "/api/daemon/agent-runs/"+cl.AgentRun.ID+"/fail", l.personal, "", body, nil); c != 200 {
		t.Fatalf("fail %d", c)
	}
	recs := l.rerunRecords(t)
	if len(recs) != 1 || !maps.Equal(recs[0].Reproduced, map[string]bool{"auc": false}) || recs[0].Notes["auc"] != "the rerun failed before reporting a result" {
		t.Fatalf("records %+v", recs)
	}
	// A rerun cancelled with its study records nothing.
	l2 := newRerunLab(t)
	l2.script(t, "x1", "fit.py", 0, "")
	l2.enqueue(t)
	cl2 := l2.claim(t)
	if c := l2.s.call(t, "POST", "/api/agent-runs/"+cl2.AgentRun.ID+"/cancel", l2.jwt, l2.ws, nil, nil); c >= 300 {
		t.Fatalf("cancel %d", c)
	}
	if n := len(l2.rerunRecords(t)); n != 0 {
		t.Fatalf("a cancelled rerun wrote %d records", n)
	}
}

func TestARetriedRerunKnowsItsSavedOutputs(t *testing.T) {
	l := newRerunLab(t)
	l.script(t, "x1", "fit.py", 0, "")
	l.enqueue(t)
	cl := l.claim(t)
	l.upload(t, cl.Token, "outputs/auc.txt", "0.873\n")
	if c := l.result(t, cl, map[string]any{"steps": []any{}, "outputs": []any{}, "failure": "machine_offline"}); c != 200 {
		t.Fatalf("result %d", c)
	}
	for _, sql := range []string{"UPDATE agent_run SET retry_after = NULL WHERE id = $1", "UPDATE runtime SET unhealthy_reason = NULL WHERE id = $1"} {
		id := cl.AgentRun.ID
		if strings.Contains(sql, "runtime") {
			id = l.rts
		}
		if _, err := l.s.Env.Owner.Exec(t.Context(), sql, id); err != nil {
			t.Fatal(err)
		}
	}
	again := l.claim(t)
	if again.AgentRun.ID != cl.AgentRun.ID || again.AgentRun.Attempt == cl.AgentRun.Attempt {
		t.Fatalf("claimed %+v", again.AgentRun)
	}
	if len(again.Rerun.Saved) != 1 || again.Rerun.Saved[0].Name != "outputs/auc.txt" || again.Rerun.Saved[0].Sha256 != hexSHA("0.873\n") {
		t.Fatalf("saved %+v", again.Rerun.Saved)
	}
}

func TestARerunGetsTheStudysServices(t *testing.T) {
	l := newRerunLab(t)
	_, up := l.s.serviceUpload(t, l.jwt, l.ws, "s.tar", serviceTar(t), false)
	if _, err := l.s.Env.Owner.Exec(t.Context(), "UPDATE attachment SET card_id = $1, service_name = 'evaluator', service_start = 'python server.py' WHERE id = $2", l.card, up["id"]); err != nil {
		t.Fatal(err)
	}
	l.script(t, "x1", "fit.py", 0, "")
	l.enqueue(t)
	ref := map[string]any{"workspace_id": l.ws, "runtime_id": l.rts}
	var claims []map[string]json.RawMessage
	if c := l.s.call(t, "POST", "/api/daemon/agent-runs/claim", l.personal, "", map[string]any{"runtimes": []any{ref}, "slots": 1}, &claims); c != 200 || len(claims) != 1 {
		t.Fatalf("claim %d %v", c, claims)
	}
	if !strings.Contains(string(claims[0]["services"]), `"name":"evaluator"`) {
		t.Fatalf("services %s", claims[0]["services"])
	}
	var run struct {
		ID      string
		Attempt int
	}
	json.Unmarshal(claims[0]["agent_run"], &run)
	ref["attempt"] = run.Attempt
	if c := l.s.call(t, "POST", "/api/daemon/agent-runs/"+run.ID+"/start", l.personal, "", ref, nil); c != 204 {
		t.Fatalf("start %d", c)
	}
	req, _ := http.NewRequest("GET", l.s.URL+"/api/daemon/runs/"+run.ID+"/services/"+up["id"].(string), nil)
	req.Header.Set("Authorization", "Bearer "+l.personal)
	req.Header.Set("X-Workspace-ID", l.ws)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("the rerun's daemon could not fetch the service: %d", resp.StatusCode)
	}
}

func TestARerunsFilesAreNotTheStudysFiles(t *testing.T) {
	l := newRerunLab(t)
	l.script(t, "x1", "fit.py", 0, "")
	l.enqueue(t)
	cl := l.claim(t)
	l.upload(t, cl.Token, "metrics.txt", "f1 = 1.50\n")
	if n := l.s.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'output'", l.card); n != 0 {
		t.Fatalf("the rerun's file is listed among the study's %d files", n)
	}
	if n := l.s.count(t, "SELECT count(*) FROM record WHERE card_id = $1 AND kind = 'rerun_output' AND key = 'o1'", l.card); n != 1 {
		t.Fatalf("the rerun's file was not kept as its own: %d", n)
	}
}
