package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

func csWork(t *testing.T, s *server, a, daemon string, ref service.RuntimeRef) coresearch.Work {
	t.Helper()
	var out coresearch.Work
	if code := s.call(t, "POST", "/api/daemon/coresearch/claim", s.personalToken(t, a), "", map[string]any{"daemon_id": daemon, "runtimes": []service.RuntimeRef{ref}}, &out); code != 200 {
		t.Fatalf("claim %d", code)
	}
	return out
}
func TestCellIDStableEndToEnd(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	var c db.CoresearchCell
	if code := s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "made-up-9", "source": "1+1"}, &c); code != 201 {
		t.Fatal(code)
	}
	ref := csRegister(t, s, a, ws, "work", "claude")
	got := csWork(t, s, a, "work", ref)
	if len(got.Cells) != 1 || got.Cells[0].ID != c.ID || got.Cells[0].Kernel != "made-up-9" {
		t.Fatalf("%+v", got)
	}
	if code := s.call(t, "POST", "/api/daemon/coresearch/cells/"+c.ID.String()+"/finish?daemon_id=work", s.personalToken(t, a), ws, map[string]any{"status": "ok", "outputs": []any{}}, nil); code != 204 {
		t.Fatal(code)
	}
}
func TestCellsRunInQueueOrder(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	for i := 0; i < 2; i++ {
		if code := s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "python3", "source": "1"}, nil); code != 201 {
			t.Fatal(code)
		}
	}
	ref := csRegister(t, s, a, ws, "work", "claude")
	first := csWork(t, s, a, "work", ref)
	if len(first.Cells) != 1 {
		t.Fatal(first)
	}
	if next := csWork(t, s, a, "work", ref); len(next.Cells) != 0 {
		t.Fatal(next)
	}
	if code := s.call(t, "POST", "/api/daemon/coresearch/cells/"+first.Cells[0].ID.String()+"/finish?daemon_id=work", s.personalToken(t, a), ws, map[string]any{"status": "ok", "outputs": []any{}}, nil); code != 204 {
		t.Fatal(code)
	}
	if next := csWork(t, s, a, "work", ref); len(next.Cells) != 1 || next.Cells[0].ID == first.Cells[0].ID {
		t.Fatal(next)
	}
}
func TestNonOwnerCannotRunCells(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	csAdd(t, s, a, b, ws, p.ID, "member")
	path, c := csChat(t, s, a, ws, p.ID)
	for _, v := range []struct {
		url  string
		body any
	}{{path + "/cells", map[string]string{"kernel": "x", "source": "x"}}, {path + "/kernel", map[string]string{"op": "interrupt"}}, {csProjects + "/" + p.ID + "/runs", map[string]any{"chat_id": c.ID, "script": "x"}}, {csProjects + "/" + p.ID + "/environment", map[string]any{"chat_id": c.ID, "python": "3.12"}}} {
		if code := s.call(t, "POST", v.url, b, ws, v.body, nil); code != 403 {
			t.Fatalf("%s %d", v.url, code)
		}
	}
}
func TestUnknownKernelPassesThrough(t *testing.T) { TestCellIDStableEndToEnd(t) }
func TestOnlyOwnersDaemonClaimsWork(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	csAdd(t, s, a, b, ws, p.ID, "member")
	path, _ := csChat(t, s, a, ws, p.ID)
	s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "x", "source": "secret"}, nil)
	other := csRegister(t, s, b, ws, "other", "claude")
	if got := csWork(t, s, b, "other", other); len(got.Cells) != 0 {
		t.Fatal(got)
	}
	own := csRegister(t, s, a, ws, "own", "claude")
	if got := csWork(t, s, a, "other", own); len(got.Cells) != 0 {
		t.Fatal(got)
	}
	first := csWork(t, s, a, "own", own)
	if len(first.Cells) != 1 {
		t.Fatal(first)
	}
	second := csRegister(t, s, a, ws, "second", "claude")
	s.call(t, "POST", path+"/kernel", a, ws, map[string]string{"op": "interrupt"}, nil)
	if got := csWork(t, s, a, "second", second); len(got.Controls) != 0 {
		t.Fatal(got)
	}
	if code := s.call(t, "GET", "/api/daemon/coresearch/cells/"+first.Cells[0].ID.String()+"/status?daemon_id=second", s.personalToken(t, a), ws, nil, nil); code != 404 {
		t.Fatal(code)
	}
}
func TestRunPinsHeadVersions(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	_, c := csChat(t, s, a, ws, p.ID)
	if code, _ := csUpload(t, s, a, ws, p.ID, "main.py", 0, false, []byte("print(1)")); code != 201 {
		t.Fatal(code)
	}
	_, old := csUpload(t, s, a, ws, p.ID, "data.txt", 0, false, []byte("old"))
	var run db.CoresearchRun
	if code := s.call(t, "POST", csProjects+"/"+p.ID+"/runs", a, ws, map[string]any{"chat_id": c.ID, "script": "main.py", "inputs": []string{"data.txt"}}, &run); code != 201 {
		t.Fatal(code)
	}
	csUpload(t, s, a, ws, p.ID, "data.txt", 1, false, []byte("new"))
	ref := csRegister(t, s, a, ws, "work", "claude")
	got := csWork(t, s, a, "work", ref)
	if len(got.Runs) != 1 || len(got.Runs[0].Inputs) != 1 || got.Runs[0].Inputs[0].SHA256 != old.Sha256 || got.Runs[0].Inputs[0].N != 1 {
		t.Fatal(got)
	}
}
func TestUnsavedInputRefused(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	_, c := csChat(t, s, a, ws, p.ID)
	csUpload(t, s, a, ws, p.ID, "main.py", 0, false, []byte("1"))
	var out map[string]string
	if code := s.call(t, "POST", csProjects+"/"+p.ID+"/runs", a, ws, map[string]any{"chat_id": c.ID, "script": "main.py", "inputs": []string{"missing.csv"}}, &out); code != 400 || out["error"] != "missing.csv is not a saved project file; save it first" {
		t.Fatalf("%d %v", code, out)
	}
}
func TestRunKeysPerProject(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	for i := 0; i < 2; i++ {
		if i == 1 {
			if code := s.call(t, "POST", csProjects, a, ws, map[string]string{"name": "Second"}, &p); code != 201 {
				t.Fatal(code)
			}
		}
		_, c := csChat(t, s, a, ws, p.ID)
		csUpload(t, s, a, ws, p.ID, "main.py", 0, false, []byte("1"))
		for j := 1; j <= 2; j++ {
			var run db.CoresearchRun
			if code := s.call(t, "POST", csProjects+"/"+p.ID+"/runs", a, ws, map[string]any{"chat_id": c.ID, "script": "main.py"}, &run); code != 201 || run.Key != fmt.Sprintf("R%d", j) {
				t.Fatalf("%d %+v", code, run)
			}
		}
	}
}
func TestKernelRequestClearedOnlyForItsSeq(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, c := csChat(t, s, a, ws, p.ID)
	ref := csRegister(t, s, a, ws, "work", "claude")
	for _, op := range []string{"interrupt", "restart"} {
		if code := s.call(t, "POST", path+"/kernel", a, ws, map[string]string{"op": op}, nil); code != 204 {
			t.Fatal(code)
		}
	}
	got := csWork(t, s, a, "work", ref)
	if len(got.Controls) != 1 || got.Controls[0].Seq != 2 || got.Controls[0].Op != "restart" {
		t.Fatal(got)
	}
	url := "/api/daemon/coresearch/chats/" + c.ID.String() + "/kernel?daemon_id=work"
	for _, seq := range []int{1, 2} {
		if code := s.call(t, "POST", url, s.personalToken(t, a), ws, map[string]any{"state": "idle", "seq_done": seq}, nil); code != 204 {
			t.Fatal(code)
		}
		got = csWork(t, s, a, "work", ref)
		if seq == 1 && len(got.Controls) != 1 || seq == 2 && len(got.Controls) != 0 {
			t.Fatal(got)
		}
	}
}
func csRunToken(t *testing.T, s *server, a, ws, path string, ref service.RuntimeRef) string {
	t.Helper()
	csTurn(t, s, a, ws, path)
	got := csClaim(t, s, a, ref)
	if len(got) != 1 {
		t.Fatal(got)
	}
	ref.Attempt = got[0].AgentRun.Attempt
	if e := s.H.AgentRuns.Start(t.Context(), uuid.MustParse(s.userID(t, a)), ref, got[0].AgentRun.ID); e != nil {
		t.Fatal(e)
	}
	return got[0].Token
}
func TestRoleCellRouteScopedToItsChat(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	other, _ := csChat(t, s, a, ws, p.ID)
	ref := csRegister(t, s, a, ws, "work", "claude")
	token := csRunToken(t, s, a, ws, path, ref)
	var foreign db.CoresearchCell
	s.call(t, "POST", other+"/cells", a, ws, map[string]string{"kernel": "x", "source": "private"}, &foreign)
	if code := s.call(t, "GET", "/api/run/coresearch/cells/"+foreign.ID.String(), token, "", nil, nil); code != 404 {
		t.Fatal(code)
	}
	var own db.CoresearchCell
	if code := s.call(t, "POST", "/api/run/coresearch/cells", token, "", map[string]string{"kernel": "x", "source": "own"}, &own); code != 201 || own.AgentRunID == nil || own.AuthorUserID != nil {
		t.Fatalf("%d %+v", code, own)
	}
	if code := s.call(t, "GET", "/api/run/coresearch/cells/"+own.ID.String(), token, "", nil, nil); code != 200 {
		t.Fatal(code)
	}
}
func TestCellOutputLimits(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	ref := csRegister(t, s, a, ws, "work", "claude")
	token := csRunToken(t, s, a, ws, path, ref)
	for _, n := range []int{1 << 20, (1 << 20) + 1, (20 << 20) + 1} {
		var c db.CoresearchCell
		s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "x", "source": "x"}, &c)
		csWork(t, s, a, "work", ref)
		raw := json.RawMessage(`"` + strings.Repeat("x", n-2) + `"`)
		code := s.call(t, "POST", "/api/daemon/coresearch/cells/"+c.ID.String()+"/finish?daemon_id=work", s.personalToken(t, a), ws, map[string]any{"status": "ok", "outputs": raw}, nil)
		if n > 20<<20 {
			if code != 413 {
				t.Fatal(code)
			}
			continue
		}
		if code != 204 {
			t.Fatal(code)
		}
		var rows []db.CoresearchCell
		s.call(t, "GET", path+"/cells", a, ws, nil, &rows)
		cell := rows[len(rows)-1]
		if n == 1<<20 && (len(cell.Outputs) != n || cell.OutputsBlob != nil) || n > 1<<20 && (string(cell.Outputs) != "null" || cell.OutputsBlob == nil) {
			t.Fatalf("storage outputs=%d blob=%v", len(cell.Outputs), cell.OutputsBlob)
		}
		var hydrated db.CoresearchCell
		if code = s.call(t, "GET", "/api/run/coresearch/cells/"+c.ID.String(), token, "", nil, &hydrated); code != 200 || len(hydrated.Outputs) != n {
			t.Fatalf("hydrate %d size=%d", code, len(hydrated.Outputs))
		}
	}
}
func TestCoresearchJSONOutputsAreStructured(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	var c db.CoresearchCell
	s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "x", "source": "x"}, &c)
	ref := csRegister(t, s, a, ws, "work", "claude")
	csWork(t, s, a, "work", ref)
	s.call(t, "POST", "/api/daemon/coresearch/cells/"+c.ID.String()+"/finish?daemon_id=work", s.personalToken(t, a), ws, map[string]any{"status": "ok", "outputs": []any{map[string]string{"text": "hi"}}}, nil)
	var rows []map[string]any
	s.call(t, "GET", path+"/cells", a, ws, nil, &rows)
	if _, ok := rows[0]["outputs"].([]any); !ok {
		t.Fatalf("outputs is %T, want array", rows[0]["outputs"])
	}
}
func TestRemovedMemberWorkIsCancelled(t *testing.T) {
	for _, mode := range []string{"project", "workspace", "leave"} {
		t.Run(mode, func(t *testing.T) {
			s, a, b, _, ws, p := csFixture(t)
			csAdd(t, s, a, b, ws, p.ID, "member")
			path, c := csChat(t, s, b, ws, p.ID)
			csUpload(t, s, a, ws, p.ID, "main.py", 0, false, []byte("1"))
			ref := csRegister(t, s, b, ws, "work", "claude")
			token := csRunToken(t, s, b, ws, path, ref)
			var cells []db.CoresearchCell
			for i := 0; i < 2; i++ {
				var cell db.CoresearchCell
				s.call(t, "POST", path+"/cells", b, ws, map[string]string{"kernel": "x", "source": "x"}, &cell)
				cells = append(cells, cell)
			}
			var run db.CoresearchRun
			s.call(t, "POST", csProjects+"/"+p.ID+"/runs", b, ws, map[string]any{"chat_id": c.ID, "script": "main.py"}, &run)
			var setup db.CoresearchSetup
			s.call(t, "POST", csProjects+"/"+p.ID+"/environment", b, ws, map[string]any{"chat_id": c.ID, "python": "3.12"}, &setup)
			got := csWork(t, s, b, "work", ref)
			if len(got.Cells) != 1 || len(got.Runs) != 1 || len(got.Setups) != 1 {
				t.Fatal(got)
			}
			url := "/api/daemon/coresearch/cells/" + cells[0].ID.String() + "/status?daemon_id=work"
			personal := s.personalToken(t, b)
			if code := s.call(t, "GET", url, personal, ws, nil, nil); code != 200 {
				t.Fatal(code)
			}
			switch mode {
			case "project":
				if code := s.call(t, "DELETE", csProjects+"/"+p.ID+"/members/"+s.userID(t, b), a, ws, nil, nil); code != 204 {
					t.Fatal(code)
				}
			case "workspace":
				if code := s.call(t, "DELETE", "/api/workspaces/"+ws+"/members/"+s.userID(t, b), a, "", nil, nil); code != 204 {
					t.Fatal(code)
				}
			case "leave":
				if code := s.call(t, "POST", "/api/workspaces/"+ws+"/leave", b, "", nil, nil); code != 204 {
					t.Fatal(code)
				}
			}
			for _, table := range []string{"coresearch_cell", "coresearch_run", "coresearch_setup", "agent_run"} {
				var count int
				filter := "chat_id"
				if table == "agent_run" {
					filter = "coresearch_chat_id"
				}
				if e := s.Env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM "+table+" WHERE "+filter+"=$1 AND (status<>'cancelled' OR failure_reason IS DISTINCT FROM 'coresearch_access_revoked')", c.ID).Scan(&count); e != nil || count != 0 {
					t.Fatalf("%s live=%d err=%v", table, count, e)
				}
			}
			if code := s.call(t, "GET", url, personal, ws, nil, nil); code != 404 {
				t.Fatal(code)
			}
			if code := s.call(t, "GET", "/api/daemon/coresearch/chats/"+c.ID.String()+"/status?daemon_id=work", personal, ws, nil, nil); code != 404 {
				t.Fatal(code)
			}
			if code := s.call(t, "GET", "/api/run/coresearch/cells/"+cells[0].ID.String(), token, "", nil, nil); code != 401 && code != 404 {
				t.Fatal(code)
			}
			if mode != "project" {
				if _, e := s.Env.Owner.Exec(t.Context(), `INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member')`, ws, s.userID(t, b)); e != nil {
					t.Fatal(e)
				}
			}
			csAdd(t, s, a, b, ws, p.ID, "member")
			if got := csWork(t, s, b, "work", ref); len(got.Cells)+len(got.Runs)+len(got.Setups)+len(got.Controls) != 0 {
				t.Fatal(got)
			}
		})
	}
}
func csFinishRun(t *testing.T, s *server, token, ws, id string, facts map[string]any, files map[string][]byte) int {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	raw, e := json.Marshal(facts)
	if e != nil {
		t.Fatal(e)
	}
	mw.WriteField("facts", string(raw))
	for name, data := range files {
		f, e := mw.CreateFormFile("out/"+name, name)
		if e != nil {
			t.Fatal(e)
		}
		f.Write(data)
	}
	mw.Close()
	req, e := http.NewRequest("POST", s.URL+"/api/daemon/coresearch/runs/"+id+"/finish?daemon_id=work", &body)
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Workspace-ID", ws)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 204 {
		t.Logf("finish response %s", b)
	}
	return resp.StatusCode
}
func TestOutputsBecomeResultVersions(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	_, c := csChat(t, s, a, ws, p.ID)
	csUpload(t, s, a, ws, p.ID, "main.py", 0, false, []byte("1"))
	ref := csRegister(t, s, a, ws, "work", "claude")
	var original db.CoresearchRun
	if code := s.call(t, "POST", csProjects+"/"+p.ID+"/runs", a, ws, map[string]any{"chat_id": c.ID, "script": "main.py"}, &original); code != 201 {
		t.Fatal(code)
	}
	csWork(t, s, a, "work", ref)
	env := strings.Repeat("a", 64)
	if code := csFinishRun(t, s, s.personalToken(t, a), ws, original.ID.String(), map[string]any{"status": "ok", "env_sha": env, "outputs": map[string]string{"results/fig.png": "forged"}, "matches_original": true}, map[string][]byte{"fig.png": {0, 1, 2}}); code != 204 {
		t.Fatal(code)
	}
	var files []coresearchFileView
	s.call(t, "GET", csProjects+"/"+p.ID+"/files", a, ws, nil, &files)
	var output coresearchFileView
	for _, f := range files {
		if f.Path == "results/fig.png" {
			output = f
		}
	}
	if output.Head.RunID == nil || *output.Head.RunID != original.ID {
		t.Fatalf("%+v", output)
	}
	var stored db.CoresearchRun
	s.call(t, "GET", csProjects+"/"+p.ID+"/runs/"+original.Key, a, ws, nil, &stored)
	var facts map[string]any
	json.Unmarshal(stored.Facts, &facts)
	hashes := facts["outputs"].(map[string]any)
	if hashes["results/fig.png"] != output.Head.Sha256 || facts["matches_original"] != nil {
		t.Fatal(facts)
	}
	for i, data := range [][]byte{{0, 1, 2}, {0, 1, 3}} {
		var rerun db.CoresearchRun
		if code := s.call(t, "POST", csProjects+"/"+p.ID+"/runs/"+original.Key+"/rerun", a, ws, nil, &rerun); code != 201 {
			t.Fatal(code)
		}
		got := csWork(t, s, a, "work", ref)
		if len(got.Runs) != 1 || got.Runs[0].ExpectedEnvSHA == nil || *got.Runs[0].ExpectedEnvSHA != env || got.Runs[0].Seed != original.Seed || rerun.ScriptN != original.ScriptN {
			t.Fatal(got)
		}
		if code := csFinishRun(t, s, s.personalToken(t, a), ws, rerun.ID.String(), map[string]any{"status": "ok", "env_sha": strings.Repeat("b", 64)}, nil); code != 409 {
			t.Fatal(code)
		}
		if code := csFinishRun(t, s, s.personalToken(t, a), ws, rerun.ID.String(), map[string]any{"status": "ok", "env_sha": env, "outputs": hashes, "matches_original": i == 1}, map[string][]byte{"fig.png": data}); code != 204 {
			t.Fatal(code)
		}
		s.call(t, "GET", csProjects+"/"+p.ID+"/runs/"+rerun.Key, a, ws, nil, &stored)
		json.Unmarshal(stored.Facts, &facts)
		if facts["matches_original"] != (i == 0) {
			t.Fatal(facts)
		}
	}
}
func TestDaemonBlobOnlyForOwnProjects(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	csAdd(t, s, a, b, ws, p.ID, "member")
	path, _ := csChat(t, s, a, ws, p.ID)
	_, v := csUpload(t, s, a, ws, p.ID, "x.txt", 0, false, []byte("secret"))
	own := csRegister(t, s, a, ws, "work", "claude")
	csRegister(t, s, b, ws, "other", "claude")
	s.call(t, "POST", path+"/kernel", a, ws, map[string]string{"op": "restart"}, nil)
	csWork(t, s, a, "work", own)
	url := "/api/daemon/coresearch/blobs/" + v.Sha256
	if code := s.call(t, "GET", url+"?daemon_id=other", s.personalToken(t, b), ws, nil, nil); code != 404 {
		t.Fatal(code)
	}
	req, _ := http.NewRequest("GET", s.URL+url+"?daemon_id=work", nil)
	req.Header.Set("Authorization", "Bearer "+s.personalToken(t, a))
	req.Header.Set("X-Workspace-ID", ws)
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(data) != "secret" {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
}
func TestCoresearchSetupReservation(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	_, c := csChat(t, s, a, ws, p.ID)
	ref := csRegister(t, s, a, ws, "work", "claude")
	var setup db.CoresearchSetup
	if code := s.call(t, "POST", csProjects+"/"+p.ID+"/environment", a, ws, map[string]any{"chat_id": c.ID, "python": "3.12"}, &setup); code != 201 {
		t.Fatal(code)
	}
	csWork(t, s, a, "work", ref)
	url := "/api/daemon/coresearch/setups/" + setup.ID.String() + "/finish?daemon_id=work"
	for i, v := range []string{"3.12", "3.12", "3.13"} {
		want := 204
		if i == 2 {
			want = 409
		}
		if code := s.call(t, "POST", url, s.personalToken(t, a), ws, map[string]string{"status": "running", "python_version": v}, nil); code != want {
			t.Fatalf("reserve %s: %d", v, code)
		}
	}
	var state, version string
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT s.status,p.python_version FROM coresearch_setup s JOIN coresearch_project p ON p.id=s.project_id WHERE s.id=$1`, setup.ID).Scan(&state, &version); e != nil || state != "running" || version != "3.12" {
		t.Fatalf("%s %s %v", state, version, e)
	}
	if code := s.call(t, "POST", url, s.personalToken(t, a), ws, map[string]string{"status": "ok", "python_version": "3.13"}, nil); code != 409 {
		t.Fatal(code)
	}
	if code := s.call(t, "POST", url, s.personalToken(t, a), ws, map[string]string{"status": "failed", "log": "install failed"}, nil); code != 204 {
		t.Fatal(code)
	}
}
func TestCoresearchRunTokenFiles(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	ref := csRegister(t, s, a, ws, "work", "claude")
	token := csRunToken(t, s, a, ws, path, ref)
	upload := func(base string, data []byte) (int, db.CoresearchFileVersion) {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		mw.WriteField("path", "agent.txt")
		mw.WriteField("base", base)
		f, _ := mw.CreateFormFile("file", "agent.txt")
		f.Write(data)
		mw.Close()
		req, _ := http.NewRequest("POST", s.URL+"/api/run/coresearch/files", &body)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		var v db.CoresearchFileVersion
		if resp.StatusCode == 201 {
			if e = json.NewDecoder(resp.Body).Decode(&v); e != nil {
				t.Fatal(e)
			}
		}
		return resp.StatusCode, v
	}
	code, v := upload("0", []byte("saved by agent"))
	if code != 201 || v.AgentRunID == nil || v.AuthorUserID != nil {
		t.Fatalf("%d %+v", code, v)
	}
	var refs []coresearch.FileRef
	if code := s.call(t, "GET", "/api/run/coresearch/files", token, "", nil, &refs); code != 200 || len(refs) != 1 || refs[0].SHA256 != v.Sha256 {
		t.Fatalf("%d %v", code, refs)
	}
	req, _ := http.NewRequest("GET", s.URL+"/api/run/coresearch/files/content?path=agent.txt&n=1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(data) != "saved by agent" {
		t.Fatalf("%d %s", resp.StatusCode, data)
	}
	if code, _ := upload("0", []byte("stale agent bytes")); code != 409 {
		t.Fatal(code)
	}
	sum := sha256.Sum256([]byte("stale agent bytes"))
	if rc, e := s.H.Blobs.Get(t.Context(), coresearch.BlobKey(uuid.MustParse(ws), hex.EncodeToString(sum[:]))); e == nil {
		rc.Close()
		t.Fatal("stale run-token upload blob retained")
	}
}
func TestCoresearchRunFinishMultipleLargeFiles(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	_, c := csChat(t, s, a, ws, p.ID)
	csUpload(t, s, a, ws, p.ID, "main.py", 0, false, []byte("1"))
	ref := csRegister(t, s, a, ws, "work", "claude")
	var run db.CoresearchRun
	s.call(t, "POST", csProjects+"/"+p.ID+"/runs", a, ws, map[string]any{"chat_id": c.ID, "script": "main.py"}, &run)
	csWork(t, s, a, "work", ref)
	first := make([]byte, 26<<20)
	second := make([]byte, 26<<20)
	second[0] = 1
	if code := csFinishRun(t, s, s.personalToken(t, a), ws, run.ID.String(), map[string]any{"status": "ok", "env_sha": strings.Repeat("a", 64)}, map[string][]byte{"first.bin": first, "second.bin": second}); code != 204 {
		t.Fatal(code)
	}
	var count int
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_file_version WHERE run_id=$1 AND size=$2`, run.ID, 26<<20).Scan(&count); e != nil || count != 2 {
		t.Fatalf("%d %v", count, e)
	}
}
func TestCoresearchRevocationHelperAuthority(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	csAdd(t, s, a, b, ws, p.ID, "member")
	path, c := csChat(t, s, a, ws, p.ID)
	s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "x", "source": "x"}, nil)
	for _, project := range []*uuid.UUID{nil, ptrUUID(uuid.MustParse(p.ID))} {
		e := s.H.Store.InWorkspace(t.Context(), uuid.MustParse(ws), uuid.MustParse(s.userID(t, b)), func(tx *store.Tx) error {
			_, e := tx.Q.RevokeCoresearchWork(t.Context(), db.RevokeCoresearchWorkParams{Target: uuid.MustParse(s.userID(t, a)), Project: project})
			return e
		})
		if e == nil {
			t.Fatal("nonowner could revoke work")
		}
	}
	var status string
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT status FROM coresearch_cell WHERE chat_id=$1`, c.ID).Scan(&status); e != nil || status != "queued" {
		t.Fatalf("%s %v", status, e)
	}
}
func ptrUUID(v uuid.UUID) *uuid.UUID { return &v }
func TestCoresearchConcurrentSetupReservation(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	_, c := csChat(t, s, a, ws, p.ID)
	ref := csRegister(t, s, a, ws, "work", "claude")
	var setup db.CoresearchSetup
	s.call(t, "POST", csProjects+"/"+p.ID+"/environment", a, ws, map[string]any{"chat_id": c.ID}, &setup)
	csWork(t, s, a, "work", ref)
	token := s.personalToken(t, a)
	start := make(chan struct{})
	results := make(chan int, 2)
	for _, version := range []string{"3.11", "3.12"} {
		go func(version string) {
			<-start
			results <- s.call(t, "POST", "/api/daemon/coresearch/setups/"+setup.ID.String()+"/finish?daemon_id=work", token, ws, map[string]string{"status": "running", "python_version": version}, nil)
		}(version)
	}
	close(start)
	one, two := <-results, <-results
	if !(one == 204 && two == 409 || one == 409 && two == 204) {
		t.Fatalf("%d %d", one, two)
	}
}
func TestCoresearchRunRejectsAliasedOutputs(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	_, c := csChat(t, s, a, ws, p.ID)
	csUpload(t, s, a, ws, p.ID, "main.py", 0, false, []byte("1"))
	ref := csRegister(t, s, a, ws, "work", "claude")
	var run db.CoresearchRun
	s.call(t, "POST", csProjects+"/"+p.ID+"/runs", a, ws, map[string]any{"chat_id": c.ID, "script": "main.py"}, &run)
	csWork(t, s, a, "work", ref)
	if code := csFinishRun(t, s, s.personalToken(t, a), ws, run.ID.String(), map[string]any{"status": "ok", "env_sha": strings.Repeat("a", 64)}, map[string][]byte{"./fig.png": {0, 1}, "fig.png": {0, 2}}); code != 400 {
		t.Fatal(code)
	}
	var count int
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_file_version WHERE run_id=$1`, run.ID).Scan(&count); e != nil || count != 0 {
		t.Fatalf("%d %v", count, e)
	}
}

func TestFailedRerunNeverMatchesOriginal(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	_, chat := csChat(t, s, a, ws, p.ID)
	csUpload(t, s, a, ws, p.ID, "main.py", 0, false, []byte("pass"))
	ref := csRegister(t, s, a, ws, "work", "claude")
	var original db.CoresearchRun
	if code := s.call(t, "POST", csProjects+"/"+p.ID+"/runs", a, ws, map[string]any{"chat_id": chat.ID, "script": "main.py"}, &original); code != 201 {
		t.Fatal(code)
	}
	csWork(t, s, a, "work", ref)
	env := strings.Repeat("a", 64)
	if code := csFinishRun(t, s, s.personalToken(t, a), ws, original.ID.String(), map[string]any{"status": "ok", "env_sha": env}, nil); code != 204 {
		t.Fatal(code)
	}
	for _, actual := range []string{strings.Repeat("b", 64), env, ""} {
		var rerun db.CoresearchRun
		if code := s.call(t, "POST", csProjects+"/"+p.ID+"/runs/"+original.Key+"/rerun", a, ws, nil, &rerun); code != 201 {
			t.Fatal(code)
		}
		csWork(t, s, a, "work", ref)
		if code := csFinishRun(t, s, s.personalToken(t, a), ws, rerun.ID.String(), map[string]any{"status": "failed", "env_sha": actual, "error": "the original run environment is unavailable; restore it before rerunning", "matches_original": true}, nil); code != 204 {
			t.Fatal(code)
		}
		var stored db.CoresearchRun
		if code := s.call(t, "GET", csProjects+"/"+p.ID+"/runs/"+rerun.Key, a, ws, nil, &stored); code != 200 {
			t.Fatal(code)
		}
		var facts map[string]any
		if e := json.Unmarshal(stored.Facts, &facts); e != nil {
			t.Fatal(e)
		}
		if _, present := facts["matches_original"]; present {
			t.Fatalf("failed rerun acquired badge with env %q: %v", actual, facts)
		}
	}
}
