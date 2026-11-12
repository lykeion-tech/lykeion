package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
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
	if _, ok := facts["env_fingerprint_version"]; !ok {
		facts["env_fingerprint_version"] = coresearch.EnvironmentFingerprintVersion
	}
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
	req, e := http.NewRequest("POST", s.URL+csContinuation(t, s, "/api/daemon/coresearch/runs/"+id+"/finish?daemon_id=work"), &body)
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
	// Simulate a lost HTTP acknowledgement: the same generation and receipt
	// must acknowledge without creating a second output version.
	if code := csFinishRun(t, s, s.personalToken(t, a), ws, original.ID.String(), map[string]any{"status": "ok", "env_sha": env}, map[string][]byte{"fig.png": {0, 1, 2}}); code != 204 {
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
	if output.Head.N != 1 || output.Head.RunID == nil || *output.Head.RunID != original.ID {
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

type coreFDStorage struct {
	storage.Storage
	baseline int
	t        *testing.T
}

func coreFDCount(t *testing.T) int {
	f, e := os.Open("/dev/fd")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	names, e := f.Readdirnames(-1)
	if e != nil {
		t.Fatal(e)
	}
	return len(names)
}
func (s *coreFDStorage) Put(ctx context.Context, key string, r io.Reader, n int64) error {
	if delta := coreFDCount(s.t) - s.baseline; delta > 32 {
		return fmt.Errorf("retained %d output descriptors", delta)
	}
	return s.Storage.Put(ctx, key, r, n)
}
func TestRunFinishManyOutputsKeepsDescriptorsBounded(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	_, chat := csChat(t, s, a, ws, p.ID)
	csUpload(t, s, a, ws, p.ID, "main.py", 0, false, []byte("1"))
	ref := csRegister(t, s, a, ws, "work", "claude")
	var run db.CoresearchRun
	if code := s.call(t, "POST", csProjects+"/"+p.ID+"/runs", a, ws, map[string]any{"chat_id": chat.ID, "script": "main.py"}, &run); code != 201 {
		t.Fatal(code)
	}
	csWork(t, s, a, "work", ref)
	s.H.Blobs = &coreFDStorage{Storage: s.H.Blobs, baseline: coreFDCount(t), t: t}
	files := map[string][]byte{}
	for i := 0; i < 240; i++ {
		files[fmt.Sprint(i)] = nil
	}
	if code := csFinishRun(t, s, s.personalToken(t, a), ws, run.ID.String(), map[string]any{"status": "ok", "env_sha": strings.Repeat("a", 64)}, files); code != 204 {
		t.Fatalf("many outputs finish %d", code)
	}
}

func TestRerunRejectsHistoricalInventoryFormat(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	_, chat := csChat(t, s, a, ws, p.ID)
	csUpload(t, s, a, ws, p.ID, "main.py", 0, false, []byte("1"))
	var run db.CoresearchRun
	if code := s.call(t, "POST", csProjects+"/"+p.ID+"/runs", a, ws, map[string]any{"chat_id": chat.ID, "script": "main.py"}, &run); code != 201 {
		t.Fatal(code)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE coresearch_run SET status='ok',env_sha=$1,facts='{}' WHERE id=$2", strings.Repeat("a", 64), run.ID); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", csProjects+"/"+p.ID+"/runs/"+run.Key+"/rerun", a, ws, nil, nil); code != 400 {
		t.Fatalf("historic environment rerun accepted %d", code)
	}
}

func TestCoresearchClaimLeaseFencesAndRecoversLostClaim(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	for i := 0; i < 2; i++ {
		if code := s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "x", "source": "1"}, nil); code != 201 {
			t.Fatal(code)
		}
	}
	ref := csRegister(t, s, a, ws, "work", "claude")
	var raw struct {
		Cells []map[string]any `json:"cells"`
	}
	if code := s.call(t, "POST", "/api/daemon/coresearch/claim", s.personalToken(t, a), "", map[string]any{"daemon_id": "work", "runtimes": []service.RuntimeRef{ref}}, &raw); code != 200 {
		t.Fatal(code)
	}
	if len(raw.Cells) != 1 || raw.Cells[0]["claim_token"] == nil {
		t.Fatalf("claim missing generation: %+v", raw)
	}
	id := raw.Cells[0]["id"].(string)
	token := raw.Cells[0]["claim_token"].(string)
	url := "/api/daemon/coresearch/cells/" + id + "/start?daemon_id=work&claim_token="
	if code := s.call(t, "POST", url+uuid.NewString(), s.personalToken(t, a), ws, nil, nil); code != 409 {
		t.Fatalf("stale generation accepted %d", code)
	}
	if code := s.call(t, "POST", url+token, s.personalToken(t, a), ws, nil, nil); code != 204 {
		t.Fatal(code)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE coresearch_cell SET lease_expires_at=now()-interval '1 second' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	next := csWork(t, s, a, "work", ref)
	if len(next.Cells) != 1 || next.Cells[0].ID.String() == id {
		t.Fatalf("lost claim blocks successor: %+v", next)
	}
	if code := s.call(t, "POST", url+token, s.personalToken(t, a), ws, nil, nil); code != 409 {
		t.Fatalf("expired generation accepted %d", code)
	}
}

func TestCoresearchClaimReturnsOnlyCommittedWorkspaceWork(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		t.Run(fmt.Sprintf("deferred=%t", deferred), func(t *testing.T) {
			s, a, _, outsider, firstWS, firstProject := csFixture(t)
			secondWS := s.workspace(t, a, "claim-failure").ID
			foreignWS := s.workspace(t, outsider, "foreign-claim").ID
			var secondProject csProject
			if code := s.call(t, "POST", csProjects, a, secondWS, map[string]string{"name": "Other"}, &secondProject); code != 201 {
				t.Fatalf("second project: %d", code)
			}
			var foreignProject csProject
			if code := s.call(t, "POST", csProjects, outsider, foreignWS, map[string]string{"name": "Foreign"}, &foreignProject); code != 201 {
				t.Fatal(code)
			}
			firstPath, _ := csChat(t, s, a, firstWS, firstProject.ID)
			secondPath, _ := csChat(t, s, a, secondWS, secondProject.ID)
			foreignPath, _ := csChat(t, s, outsider, foreignWS, foreignProject.ID)
			var firstCell, secondCell db.CoresearchCell
			if code := s.call(t, "POST", firstPath+"/cells", a, firstWS, map[string]string{"kernel": "python3", "source": "first"}, &firstCell); code != 201 {
				t.Fatal(code)
			}
			if code := s.call(t, "POST", secondPath+"/cells", a, secondWS, map[string]string{"kernel": "python3", "source": "second"}, &secondCell); code != 201 {
				t.Fatal(code)
			}
			if code := s.call(t, "POST", foreignPath+"/cells", outsider, foreignWS, map[string]string{"kernel": "python3", "source": "private"}, nil); code != 201 {
				t.Fatal(code)
			}
			firstRef := csRegister(t, s, a, firstWS, "work", "claude")
			secondRef := csRegister(t, s, a, secondWS, "work", "claude")
			foreignRef := csRegister(t, s, outsider, foreignWS, "work", "claude")
			trigger := "CREATE TRIGGER reject_claim BEFORE UPDATE ON coresearch_cell"
			if deferred {
				trigger = "CREATE CONSTRAINT TRIGGER reject_claim AFTER UPDATE ON coresearch_cell DEFERRABLE INITIALLY DEFERRED"
			}
			_, err := s.Env.Owner.Exec(t.Context(), fmt.Sprintf(`CREATE FUNCTION reject_claim_for_workspace() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.workspace_id = '%s'::uuid THEN RAISE EXCEPTION 'injected claim failure'; END IF; RETURN NEW; END $$; %s FOR EACH ROW EXECUTE FUNCTION reject_claim_for_workspace()`, secondWS, trigger))
			if err != nil {
				t.Fatal(err)
			}
			var got coresearch.Work
			code := s.call(t, "POST", "/api/daemon/coresearch/claim", s.personalToken(t, a), "", map[string]any{"daemon_id": "work", "runtimes": []service.RuntimeRef{firstRef, secondRef, foreignRef}}, &got)
			if code != 200 || len(got.Cells) != 1 || got.Cells[0].ID != firstCell.ID || got.Cells[0].ClaimToken == uuid.Nil {
				t.Fatalf("claim returned %d: %+v", code, got)
			}
			var status string
			if err := s.Env.Owner.QueryRow(t.Context(), "SELECT status FROM coresearch_cell WHERE id=$1", secondCell.ID).Scan(&status); err != nil || status != "queued" {
				t.Fatalf("failed workspace work was not rolled back: status=%q err=%v", status, err)
			}
			if code := s.call(t, "POST", "/api/daemon/coresearch/claim", s.personalToken(t, a), "", map[string]any{"daemon_id": "work", "runtimes": []service.RuntimeRef{secondRef}}, nil); code != 500 {
				t.Fatalf("failure without committed work returned %d", code)
			}
		})
	}
}
func TestCoresearchCellFinishAcknowledgesIdenticalReceipt(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "x", "source": "1"}, nil)
	ref := csRegister(t, s, a, ws, "work", "claude")
	var raw struct {
		Cells []map[string]any `json:"cells"`
	}
	s.call(t, "POST", "/api/daemon/coresearch/claim", s.personalToken(t, a), "", map[string]any{"daemon_id": "work", "runtimes": []service.RuntimeRef{ref}}, &raw)
	if len(raw.Cells) != 1 || raw.Cells[0]["claim_token"] == nil {
		t.Fatal("claim has no fencing token")
	}
	url := "/api/daemon/coresearch/cells/" + raw.Cells[0]["id"].(string) + "/finish?daemon_id=work&claim_token=" + raw.Cells[0]["claim_token"].(string)
	result := map[string]any{"status": "ok", "outputs": []any{}}
	for i := 0; i < 2; i++ {
		if code := s.call(t, "POST", url, s.personalToken(t, a), ws, result, nil); code != 204 {
			t.Fatalf("receipt attempt %d: %d", i, code)
		}
	}
	result["status"] = "error"
	if code := s.call(t, "POST", url, s.personalToken(t, a), ws, result, nil); code != 409 {
		t.Fatalf("conflicting receipt accepted %d", code)
	}
}

// Existing API fixtures supply the generation obtained by their claimed work.
// Explicit claim_token (including empty) always wins for negative coverage.
func csContinuation(t *testing.T, s *server, path string) string {
	u, e := url.Parse(path)
	if e != nil || u.Query().Has("claim_token") {
		return path
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 6 || parts[0] != "api" || parts[1] != "daemon" || parts[2] != "coresearch" {
		return path
	}
	table := ""
	switch parts[3] {
	case "cells":
		table = "coresearch_cell"
	case "runs":
		table = "coresearch_run"
	case "setups":
		table = "coresearch_setup"
	default:
		return path
	}
	id, e := uuid.Parse(parts[4])
	if e != nil {
		return path
	}
	var token *uuid.UUID
	if e = s.Env.Owner.QueryRow(t.Context(), "SELECT claim_token FROM "+table+" WHERE id=$1", id).Scan(&token); e != nil || token == nil {
		return path
	}
	q := u.Query()
	q.Set("claim_token", token.String())
	u.RawQuery = q.Encode()
	return u.String()
}

func TestExpiredSetupClaimsUnblockSuccessorAndRelease(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, chat := csChat(t, s, a, ws, p.ID)
	for i := 0; i < 2; i++ {
		if code := s.call(t, "POST", csProjects+"/"+p.ID+"/environment", a, ws, map[string]any{"chat_id": chat.ID, "python": "3.13"}, nil); code != 201 {
			t.Fatal(code)
		}
	}
	ref := csRegister(t, s, a, ws, "work", "claude")
	first := csWork(t, s, a, "work", ref)
	if len(first.Setups) != 1 {
		t.Fatal(first)
	}
	if _, e := s.Env.Owner.Exec(t.Context(), "UPDATE coresearch_setup SET lease_expires_at=now()-interval '1 second' WHERE id=$1", first.Setups[0].ID); e != nil {
		t.Fatal(e)
	}
	second := csWork(t, s, a, "work", ref)
	if len(second.Setups) != 1 || first.Setups[0].ID == second.Setups[0].ID {
		t.Fatal(second)
	}
	if _, e := s.Env.Owner.Exec(t.Context(), "UPDATE coresearch_setup SET lease_expires_at=now()-interval '1 second' WHERE id=$1", second.Setups[0].ID); e != nil {
		t.Fatal(e)
	}
	if e := coresearch.Sweep(t.Context(), s.H.Store); e != nil {
		t.Fatal(e)
	}
	if code := s.call(t, "POST", path+"/release", a, ws, nil, nil); code != 204 {
		t.Fatalf("expired setup prevented release %d", code)
	}
}
func TestCoresearchHeartbeatKeepsOwnerAndTokenBoundary(t *testing.T) {
	s, a, b, c, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "x", "source": "1"}, nil)
	ref := csRegister(t, s, a, ws, "work", "claude")
	work := csWork(t, s, a, "work", ref)
	cell := work.Cells[0]
	url := "/api/daemon/coresearch/cells/" + cell.ID.String() + "/heartbeat?daemon_id=work&claim_token=" + cell.ClaimToken.String()
	for _, tok := range []string{b, c} {
		if code := s.call(t, "POST", url, s.personalToken(t, tok), ws, nil, nil); code != 404 {
			t.Fatalf("foreign heartbeat %d", code)
		}
	}
	if code := s.call(t, "POST", strings.Replace(url, cell.ClaimToken.String(), "", 1), s.personalToken(t, a), ws, nil, nil); code != 409 {
		t.Fatalf("missing token heartbeat %d", code)
	}
	// A short but live lease: long enough to outlast a slow request, far
	// shorter than the minute a heartbeat grants.
	if _, e := s.Env.Owner.Exec(t.Context(), "UPDATE coresearch_cell SET lease_expires_at=now()+interval '10 seconds' WHERE id=$1", cell.ID); e != nil {
		t.Fatal(e)
	}
	if code := s.call(t, "POST", url, s.personalToken(t, a), ws, nil, nil); code != 200 {
		t.Fatal(code)
	}
	var expiry time.Time
	if e := s.Env.Owner.QueryRow(t.Context(), "SELECT lease_expires_at FROM coresearch_cell WHERE id=$1", cell.ID).Scan(&expiry); e != nil || expiry.Before(time.Now().Add(time.Minute)) {
		t.Fatal(expiry, e)
	}
}

func TestCoresearchRunningStatusRequiresLiveClaimLease(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	path, chat := csChat(t, s, a, ws, p.ID)
	if code := s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "python3", "source": "1"}, nil); code != 201 {
		t.Fatalf("cell: %d", code)
	}
	if code, _ := csUpload(t, s, a, ws, p.ID, "main.py", 0, false, []byte("print(1)")); code != 201 {
		t.Fatalf("upload: %d", code)
	}
	if code := s.call(t, "POST", csProjects+"/"+p.ID+"/runs", a, ws, map[string]any{"chat_id": chat.ID, "script": "main.py"}, nil); code != 201 {
		t.Fatalf("run: %d", code)
	}
	if code := s.call(t, "POST", csProjects+"/"+p.ID+"/environment", a, ws, map[string]any{"chat_id": chat.ID, "python": "3.13"}, nil); code != 201 {
		t.Fatalf("setup: %d", code)
	}
	ref := csRegister(t, s, a, ws, "work", "claude")
	work := csWork(t, s, a, "work", ref)
	if len(work.Cells) != 1 || len(work.Runs) != 1 || len(work.Setups) != 1 {
		t.Fatalf("claimed: %+v", work)
	}
	items := []struct {
		kind, table string
		id, token   uuid.UUID
	}{
		{"cells", "coresearch_cell", work.Cells[0].ID, work.Cells[0].ClaimToken},
		{"runs", "coresearch_run", work.Runs[0].ID, work.Runs[0].ClaimToken},
		{"setups", "coresearch_setup", work.Setups[0].ID, work.Setups[0].ClaimToken},
	}
	for _, item := range items {
		t.Run(item.kind, func(t *testing.T) {
			url := "/api/daemon/coresearch/" + item.kind + "/" + item.id.String() + "/status?daemon_id=work&claim_token=" + item.token.String()
			var before, after time.Time
			if err := s.Env.Owner.QueryRow(t.Context(), "SELECT lease_expires_at FROM "+item.table+" WHERE id=$1", item.id).Scan(&before); err != nil {
				t.Fatal(err)
			}
			var body map[string]string
			if code := s.call(t, "GET", url, s.personalToken(t, a), ws, nil, &body); code != 200 || body["status"] != "running" {
				t.Fatalf("live: %d %v", code, body)
			}
			if err := s.Env.Owner.QueryRow(t.Context(), "SELECT lease_expires_at FROM "+item.table+" WHERE id=$1", item.id).Scan(&after); err != nil || !before.Equal(after) {
				t.Fatalf("GET renewed lease: %v %v %v", before, after, err)
			}
			if code := s.call(t, "GET", strings.Replace(url, item.token.String(), uuid.NewString(), 1), s.personalToken(t, a), ws, nil, nil); code != 409 {
				t.Fatalf("wrong token: %d", code)
			}
			if code := s.call(t, "GET", url, s.personalToken(t, b), ws, nil, nil); code != 404 {
				t.Fatalf("foreign owner: %d", code)
			}
			if code := s.call(t, "GET", strings.Replace(url, "daemon_id=work", "daemon_id=foreign", 1), s.personalToken(t, a), ws, nil, nil); code != 404 {
				t.Fatalf("foreign daemon: %d", code)
			}
			if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE "+item.table+" SET claim_token=NULL WHERE id=$1", item.id); err != nil {
				t.Fatal(err)
			}
			if code := s.call(t, "GET", url, s.personalToken(t, a), ws, nil, nil); code != 409 {
				t.Fatalf("nil token: %d", code)
			}
			if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE "+item.table+" SET claim_token=$2 WHERE id=$1", item.id, item.token); err != nil {
				t.Fatal(err)
			}
			for _, expiry := range []string{"now()-interval '1 second'", "NULL"} {
				if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE "+item.table+" SET lease_expires_at="+expiry+" WHERE id=$1", item.id); err != nil {
					t.Fatal(err)
				}
				if code := s.call(t, "GET", url, s.personalToken(t, a), ws, nil, nil); code != 409 {
					t.Errorf("running with expiry %s: %d", expiry, code)
				}
			}
			for _, status := range []string{"cancelled", "ok"} {
				if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE "+item.table+" SET status=$2, lease_expires_at=NULL WHERE id=$1", item.id, status); err != nil {
					t.Fatal(err)
				}
				body = nil
				if code := s.call(t, "GET", url, s.personalToken(t, a), ws, nil, &body); code != 200 || body["status"] != status {
					t.Fatalf("terminal %s: %d %v", status, code, body)
				}
				if code := s.call(t, "GET", strings.Replace(url, item.token.String(), uuid.NewString(), 1), s.personalToken(t, a), ws, nil, nil); code != 409 {
					t.Fatalf("terminal wrong token: %d", code)
				}
			}
		})
	}
}

func TestCoresearchRunningStatusUsesDatabaseClockAfterLock(t *testing.T) {
	coresearchLeaseAfterLock(t, "GET", "status")
}

func TestCoresearchHeartbeatUsesDatabaseClockAfterLock(t *testing.T) {
	coresearchLeaseAfterLock(t, "POST", "heartbeat")
}

func coresearchLeaseAfterLock(t *testing.T, method, endpoint string) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	if code := s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "python3", "source": "1"}, nil); code != 201 {
		t.Fatal(code)
	}
	ref := csRegister(t, s, a, ws, "work", "claude")
	work := csWork(t, s, a, "work", ref)
	if len(work.Cells) != 1 {
		t.Fatal(work)
	}
	cell := work.Cells[0]
	token := s.personalToken(t, a)
	blocker, err := s.Env.Owner.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err := blocker.Exec(t.Context(), "SELECT id FROM workspace WHERE id=$1 FOR UPDATE", ws); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE coresearch_cell SET lease_expires_at=clock_timestamp()+interval '2 seconds' WHERE id=$1", cell.ID); err != nil {
		t.Fatal(err)
	}
	url := s.URL + "/api/daemon/coresearch/cells/" + cell.ID.String() + "/" + endpoint + "?daemon_id=work&claim_token=" + cell.ClaimToken.String()
	done := make(chan int, 1)
	go func() {
		req, err := http.NewRequestWithContext(t.Context(), method, url, nil)
		if err != nil {
			done <- 0
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Workspace-ID", ws)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- 0
			return
		}
		defer resp.Body.Close()
		done <- resp.StatusCode
	}()
	deadline := time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		if err := s.Env.Owner.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE $1)", "%FROM workspace WHERE id =%FOR UPDATE%").Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("status request never reached workspace lock")
	}
	if _, err := s.Env.Owner.Exec(t.Context(), "SELECT pg_sleep(2.1)"); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != http.StatusConflict {
			t.Fatalf("expired while waiting for lock: %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("status request did not finish")
	}
}

func TestCoresearchHeartbeatSQLRejectsLeaseExpiredDuringTransaction(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	if code := s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "python3", "source": "1"}, nil); code != 201 {
		t.Fatal(code)
	}
	ref := csRegister(t, s, a, ws, "work", "claude")
	work := csWork(t, s, a, "work", ref)
	if len(work.Cells) != 1 {
		t.Fatal(work)
	}
	cell := work.Cells[0]
	if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE coresearch_cell SET lease_expires_at=clock_timestamp()+interval '1 second' WHERE id=$1", cell.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := s.Env.Owner.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(t.Context(), "SELECT pg_sleep(1.1)"); err != nil {
		t.Fatal(err)
	}
	n, err := db.New(tx).HeartbeatCoresearchCell(t.Context(), db.HeartbeatCoresearchCellParams{WorkspaceID: cell.WorkspaceID, ID: cell.ID, ClaimToken: &cell.ClaimToken})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expired lease was renewed: %d", n)
	}
}

func TestCoresearchCellFinishSQLRejectsExpiredClaim(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	if code := s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "python3", "source": "1"}, nil); code != 201 {
		t.Fatal(code)
	}
	ref := csRegister(t, s, a, ws, "work", "claude")
	work := csWork(t, s, a, "work", ref)
	if len(work.Cells) != 1 {
		t.Fatal(work)
	}
	cell := work.Cells[0]
	if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE coresearch_cell SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", cell.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := s.Env.Owner.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	digest := "test"
	_, err = db.New(tx).FinishCoresearchCell(t.Context(), db.FinishCoresearchCellParams{WorkspaceID: cell.WorkspaceID, ID: cell.ID, Status: "ok", Outputs: json.RawMessage(`[]`), ReceiptSha: &digest, ClaimToken: &cell.ClaimToken})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expired finish error = %v, want no rows", err)
	}
	if _, err = tx.Exec(t.Context(), "UPDATE coresearch_cell SET lease_expires_at=clock_timestamp()+interval '2 minutes' WHERE id=$1", cell.ID); err != nil {
		t.Fatal(err)
	}
	wrong := uuid.New()
	params := db.FinishCoresearchCellParams{WorkspaceID: cell.WorkspaceID, ID: cell.ID, Status: "ok", Outputs: json.RawMessage(`[]`), ReceiptSha: &digest, ClaimToken: &wrong}
	if _, err = db.New(tx).FinishCoresearchCell(t.Context(), params); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("wrong token error = %v, want no rows", err)
	}
	params.ClaimToken = &cell.ClaimToken
	if _, err = db.New(tx).FinishCoresearchCell(t.Context(), params); err != nil {
		t.Fatalf("live matching claim: %v", err)
	}
}

func TestCoresearchSetupLeaseExpiryRollsBackReservation(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	_, c := csChat(t, s, a, ws, p.ID)
	ref := csRegister(t, s, a, ws, "work", "claude")
	var setup db.CoresearchSetup
	if code := s.call(t, "POST", csProjects+"/"+p.ID+"/environment", a, ws, map[string]any{"chat_id": c.ID}, &setup); code != 201 {
		t.Fatal(code)
	}
	work := csWork(t, s, a, "work", ref)
	if len(work.Setups) != 1 {
		t.Fatal(work)
	}
	_, err := s.Env.Owner.Exec(t.Context(), `CREATE FUNCTION test_delay_setup_pin() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(1.3); RETURN NEW; END $$;
 CREATE TRIGGER test_delay_setup_pin BEFORE UPDATE OF python_version ON coresearch_project FOR EACH ROW EXECUTE FUNCTION test_delay_setup_pin()`)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"running", "ok"} {
		if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE coresearch_setup SET lease_expires_at=clock_timestamp()+interval '1 second' WHERE id=$1", setup.ID); err != nil {
			t.Fatal(err)
		}
		url := "/api/daemon/coresearch/setups/" + setup.ID.String() + "/finish?daemon_id=work&claim_token=" + work.Setups[0].ClaimToken.String()
		if code := s.call(t, "POST", url, s.personalToken(t, a), ws, map[string]string{"status": status, "python_version": "3.12"}, nil); code != 409 {
			t.Fatalf("%s: status %d", status, code)
		}
		var version *string
		var state string
		if err := s.Env.Owner.QueryRow(t.Context(), "SELECT p.python_version,s.status FROM coresearch_project p JOIN coresearch_setup s ON s.project_id=p.id WHERE s.id=$1", setup.ID).Scan(&version, &state); err != nil {
			t.Fatal(err)
		}
		if version != nil || state != "running" {
			t.Fatalf("%s: pin=%v state=%s", status, version, state)
		}
	}
	if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE coresearch_setup SET lease_expires_at=clock_timestamp()+interval '30 seconds' WHERE id=$1", setup.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	url := s.URL + "/api/daemon/coresearch/setups/" + setup.ID.String() + "/finish?daemon_id=work&claim_token=" + work.Setups[0].ClaimToken.String()
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(`{"status":"ok","python_version":"3.12"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.personalToken(t, a))
	req.Header.Set("X-Workspace-ID", ws)
	req.Header.Set("Content-Type", "application/json")
	done := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	sleeping := false
	for time.Now().Before(deadline) {
		if err := s.Env.Owner.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event='PgSleep' AND query LIKE '%UPDATE coresearch_project SET python_version%')").Scan(&sleeping); err != nil {
			t.Fatal(err)
		}
		if sleeping {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !sleeping {
		t.Fatal("request never entered reservation trigger")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request error: %v", err)
	}

	// Wait for the cancelled transaction to release the project lock.
	checkTx, err := s.Env.Owner.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkTx.Exec(t.Context(), "SELECT id FROM coresearch_project WHERE id=$1 FOR UPDATE", p.ID); err != nil {
		t.Fatal(err)
	}
	if err := checkTx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	var version *string
	var state string
	if err := s.Env.Owner.QueryRow(t.Context(), "SELECT p.python_version,s.status FROM coresearch_project p JOIN coresearch_setup s ON s.project_id=p.id WHERE s.id=$1", setup.ID).Scan(&version, &state); err != nil {
		t.Fatal(err)
	}
	if version != nil || state != "running" {
		t.Fatalf("cancelled: pin=%v state=%s", version, state)
	}

}

func TestCoresearchRunLeaseExpiryRollsBackOutput(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	_, c := csChat(t, s, a, ws, p.ID)
	csUpload(t, s, a, ws, p.ID, "main.py", 0, false, []byte("1"))
	ref := csRegister(t, s, a, ws, "work", "claude")
	var run db.CoresearchRun
	if code := s.call(t, "POST", csProjects+"/"+p.ID+"/runs", a, ws, map[string]any{"chat_id": c.ID, "script": "main.py"}, &run); code != 201 {
		t.Fatal(code)
	}
	work := csWork(t, s, a, "work", ref)
	if len(work.Runs) != 1 {
		t.Fatal(work)
	}
	_, err := s.Env.Owner.Exec(t.Context(), `CREATE FUNCTION test_delay_run_output() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.run_id IS NOT NULL THEN PERFORM pg_sleep(1.3); END IF; RETURN NEW; END $$;
 CREATE TRIGGER test_delay_run_output BEFORE INSERT ON coresearch_file_version FOR EACH ROW EXECUTE FUNCTION test_delay_run_output()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Env.Owner.Exec(t.Context(), "UPDATE coresearch_run SET lease_expires_at=clock_timestamp()+interval '1 second' WHERE id=$1", run.ID); err != nil {
		t.Fatal(err)
	}
	output := []byte("unique expired output")
	code := csFinishRun(t, s, s.personalToken(t, a), ws, run.ID.String(), map[string]any{"status": "ok", "env_sha": strings.Repeat("a", 64)}, map[string][]byte{"late.txt": output})
	if code != 409 {
		t.Fatalf("finish status %d", code)
	}
	var n int
	var state string
	if err = s.Env.Owner.QueryRow(t.Context(), "SELECT status,(SELECT count(*) FROM coresearch_file_version WHERE run_id=$1) FROM coresearch_run WHERE id=$1", run.ID).Scan(&state, &n); err != nil {
		t.Fatal(err)
	}
	if state != "running" || n != 0 {
		t.Fatalf("state=%s versions=%d", state, n)
	}
	sum := sha256.Sum256(output)
	reader, err := s.H.Blobs.Get(t.Context(), coresearch.BlobKey(uuid.MustParse(ws), hex.EncodeToString(sum[:])))
	if reader != nil {
		reader.Close()
	}
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("output blob retained: %v", err)
	}
}

type cellOutputProbe struct {
	storage.Storage
	key       string
	afterPut  func()
	putErr    error
	deleteErr error
}

func (s *cellOutputProbe) Put(ctx context.Context, key string, r io.Reader, n int64) error {
	s.key = key
	if err := s.Storage.Put(ctx, key, r, n); err != nil {
		return err
	}
	if s.afterPut != nil {
		s.afterPut()
	}
	return s.putErr
}
func (s *cellOutputProbe) Delete(ctx context.Context, key string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.Storage.Delete(ctx, key)
}
func TestCellFailedFinishCleansAcknowledgedGeneration(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	ref := csRegister(t, s, a, ws, "work", "claude")
	var cell db.CoresearchCell
	if code := s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "python3", "source": "1"}, &cell); code != 201 {
		t.Fatal(code)
	}
	work := csWork(t, s, a, "work", ref)
	if len(work.Cells) != 1 {
		t.Fatal(work)
	}
	probe := &cellOutputProbe{Storage: s.H.Blobs}
	probe.afterPut = func() {
		if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE coresearch_cell SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", cell.ID); err != nil {
			t.Error(err)
		}
	}
	s.H.Blobs = probe
	url := "/api/daemon/coresearch/cells/" + cell.ID.String() + "/finish?daemon_id=work&claim_token=" + work.Cells[0].ClaimToken.String()
	output := json.RawMessage(`"` + strings.Repeat("x", (1<<20)+1) + `"`)
	if code := s.call(t, "POST", url, s.personalToken(t, a), ws, map[string]any{"status": "ok", "outputs": output}, nil); code != 409 {
		t.Fatalf("status %d", code)
	}
	if !strings.HasPrefix(probe.key, ws+"/coresearch/cells/"+cell.ID.String()+"/") {
		t.Fatalf("non-generation key %q", probe.key)
	}
	rc, err := probe.Storage.Get(t.Context(), probe.key)
	if rc != nil {
		rc.Close()
	}
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("orphan retained: %v", err)
	}
}

func TestCellAmbiguousPutRetainsUniqueCandidate(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	ref := csRegister(t, s, a, ws, "work", "claude")
	var cell db.CoresearchCell
	if code := s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "python3", "source": "1"}, &cell); code != 201 {
		t.Fatal(code)
	}
	work := csWork(t, s, a, "work", ref)
	if len(work.Cells) != 1 {
		t.Fatal(work)
	}
	probe := &cellOutputProbe{Storage: s.H.Blobs, putErr: errors.New("ambiguous put")}
	s.H.Blobs = probe
	url := "/api/daemon/coresearch/cells/" + cell.ID.String() + "/finish?daemon_id=work&claim_token=" + work.Cells[0].ClaimToken.String()
	output := json.RawMessage(`"` + strings.Repeat("x", (1<<20)+1) + `"`)
	if code := s.call(t, "POST", url, s.personalToken(t, a), ws, map[string]any{"status": "ok", "outputs": output}, nil); code != 500 {
		t.Fatalf("status %d", code)
	}
	first := probe.key
	rc, err := probe.Storage.Get(t.Context(), first)
	if err != nil {
		t.Fatalf("ambiguous candidate lost: %v", err)
	}
	rc.Close()
	probe.putErr = nil
	if code := s.call(t, "POST", url, s.personalToken(t, a), ws, map[string]any{"status": "ok", "outputs": output}, nil); code != 204 {
		t.Fatalf("retry status %d", code)
	}
	if probe.key == first {
		t.Fatal("retry reused ambiguous key")
	}
	rc, err = probe.Storage.Get(t.Context(), probe.key)
	if err != nil {
		t.Fatalf("successful candidate lost: %v", err)
	}
	rc.Close()
}

func TestCellCandidateReferenceRequiresOwner(t *testing.T) {
	s, a, b, _, ws, p := csFixture(t)
	csAdd(t, s, a, b, ws, p.ID, "member")
	path, _ := csChat(t, s, a, ws, p.ID)
	var cell db.CoresearchCell
	if code := s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "x", "source": "x"}, &cell); code != 201 {
		t.Fatal(code)
	}
	workspace := uuid.MustParse(ws)
	attempt := uuid.New()
	for _, tc := range []struct {
		user    string
		allowed bool
	}{{a, true}, {b, false}} {
		err := s.H.Store.InWorkspaceReadCommitted(t.Context(), workspace, uuid.MustParse(s.userID(t, tc.user)), func(tx *store.Tx) error {
			if _, err := tx.Q.LockWorkspaceForMutation(t.Context(), workspace); err != nil {
				return err
			}
			_, err := tx.CoresearchCellCandidateReferenced(t.Context(), cell.ID, attempt)
			return err
		})
		if (err == nil) != tc.allowed {
			t.Fatalf("allowed=%v error=%v", tc.allowed, err)
		}
	}
}

func TestCellCleanupReadCommittedSeesBlockedCommit(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	var cell db.CoresearchCell
	if code := s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "x", "source": "1"}, &cell); code != 201 {
		t.Fatal(code)
	}
	workspace := uuid.MustParse(ws)
	attempt := uuid.New()
	key := ws + "/coresearch/cells/" + cell.ID.String() + "/" + attempt.String() + ".json"
	blocker, err := s.Env.Owner.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err = blocker.Exec(t.Context(), "SELECT id FROM workspace WHERE id=$1 FOR UPDATE", ws); err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(t.Context(), "UPDATE coresearch_cell SET outputs_blob=$1 WHERE id=$2", key, cell.ID); err != nil {
		t.Fatal(err)
	}
	config := s.H.Store.Pool.Config()
	config.ConnConfig.RuntimeParams["default_transaction_isolation"] = "repeatable read"
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	st := &store.Store{Pool: pool}
	ownerID := uuid.MustParse(s.userID(t, a))
	done := make(chan error, 1)
	go func() {
		done <- st.InWorkspaceReadCommitted(t.Context(), workspace, ownerID, func(tx *store.Tx) error {
			if _, err := tx.Q.LockWorkspaceForMutation(t.Context(), workspace); err != nil {
				return err
			}
			referenced, err := tx.CoresearchCellCandidateReferenced(t.Context(), cell.ID, attempt)
			if err != nil {
				return err
			}
			if !referenced {
				return errors.New("committed reference invisible")
			}
			return nil
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		if err = s.Env.Owner.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE $1)", "%FROM workspace WHERE id =%FOR UPDATE%").Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("cleanup did not wait on workspace lock")
	}
	if err = blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCellGenerationDowngradeFencesConcurrentPublisher(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	var cell db.CoresearchCell
	if code := s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "x", "source": "1"}, &cell); code != 201 {
		t.Fatal(code)
	}
	sql, err := os.ReadFile("../../migrations/0071_cell_output_generation.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	writer, err := s.Env.Owner.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(context.Background())
	if _, err = writer.Exec(t.Context(), "SELECT enabled FROM public.coresearch_cell_generation_capability"); err != nil {
		t.Fatal(err)
	}
	key := ws + "/coresearch/cells/" + cell.ID.String() + "/" + uuid.NewString() + ".json"
	if _, err = writer.Exec(t.Context(), "UPDATE coresearch_cell SET outputs_blob=$1 WHERE id=$2", key, cell.ID); err != nil {
		t.Fatal(err)
	}
	config := s.Env.Owner.Config()
	config.ConnConfig.RuntimeParams["default_transaction_isolation"] = "read committed"
	downPool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer downPool.Close()
	done := make(chan error, 1)
	go func() {
		conn, e := downPool.Acquire(t.Context())
		if e != nil {
			done <- e
			return
		}
		defer conn.Release()
		_, e = conn.Exec(t.Context(), string(sql))
		if e != nil {
			_, _ = conn.Exec(context.Background(), "ROLLBACK")
		}
		done <- e
	}()
	deadline := time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		if err = s.Env.Owner.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%DROP TABLE public.coresearch_cell_generation_capability%')").Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("downgrade did not wait for publisher")
	}
	if err = writer.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err == nil {
		t.Fatal("downgrade accepted published generation")
	}
	var enabled bool
	if err = s.Env.Owner.QueryRow(t.Context(), "SELECT enabled FROM public.coresearch_cell_generation_capability").Scan(&enabled); err != nil || !enabled {
		t.Fatalf("capability lost: %v", err)
	}
}

func TestCellGenerationUnavailableDoesNotUpload(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	ref := csRegister(t, s, a, ws, "work", "claude")
	var cell db.CoresearchCell
	if code := s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "x", "source": "1"}, &cell); code != 201 {
		t.Fatal(code)
	}
	work := csWork(t, s, a, "work", ref)
	if len(work.Cells) != 1 {
		t.Fatal(work)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), "DROP TABLE public.coresearch_cell_generation_capability"); err != nil {
		t.Fatal(err)
	}
	probe := &cellOutputProbe{Storage: s.H.Blobs}
	s.H.Blobs = probe
	url := "/api/daemon/coresearch/cells/" + cell.ID.String() + "/finish?daemon_id=work&claim_token=" + work.Cells[0].ClaimToken.String()
	output := json.RawMessage(`"` + strings.Repeat("x", (1<<20)+1) + `"`)
	if code := s.call(t, "POST", url, s.personalToken(t, a), ws, map[string]any{"status": "ok", "outputs": output}, nil); code != 500 {
		t.Fatalf("status %d", code)
	}
	if probe.key != "" {
		t.Fatalf("uploaded without capability: %s", probe.key)
	}
}

func TestCellGenerationDowngradeIsAtomic(t *testing.T) {
	s, _, _, _, _, _ := csFixture(t)
	sql, err := os.ReadFile("../../migrations/0071_cell_output_generation.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	rr, err := s.Env.Owner.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rr.Exec(t.Context(), string(sql)); err == nil {
		t.Fatal("repeatable-read downgrade accepted")
	}
	rr.Rollback(context.Background())
	var enabled bool
	if err = s.Env.Owner.QueryRow(t.Context(), "SELECT enabled FROM public.coresearch_cell_generation_capability").Scan(&enabled); err != nil || !enabled {
		t.Fatalf("repeatable-read damaged capability: %v", err)
	}
	tx, err := s.Env.Owner.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), string(sql)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(t.Context()); err != nil {
		t.Fatalf("downgrade committed outer transaction: %v", err)
	}
	if err = s.Env.Owner.QueryRow(t.Context(), "SELECT enabled FROM public.coresearch_cell_generation_capability").Scan(&enabled); err != nil || !enabled {
		t.Fatalf("outer rollback lost capability: %v", err)
	}
}

func TestCellCleanupErrorDoesNotLeakOrDelete(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	ref := csRegister(t, s, a, ws, "work", "claude")
	var cell db.CoresearchCell
	if code := s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "x", "source": "1"}, &cell); code != 201 {
		t.Fatal(code)
	}
	work := csWork(t, s, a, "work", ref)
	probe := &cellOutputProbe{Storage: s.H.Blobs, deleteErr: errors.New("SECRET_BACKEND_DETAIL")}
	probe.afterPut = func() {
		_, _ = s.Env.Owner.Exec(t.Context(), "UPDATE coresearch_cell SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", cell.ID)
	}
	s.H.Blobs = probe
	url := s.URL + "/api/daemon/coresearch/cells/" + cell.ID.String() + "/finish?daemon_id=work&claim_token=" + work.Cells[0].ClaimToken.String()
	body, _ := json.Marshal(map[string]any{"status": "ok", "outputs": strings.Repeat("x", (1<<20)+1)})
	req, _ := http.NewRequestWithContext(t.Context(), "POST", url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.personalToken(t, a))
	req.Header.Set("X-Workspace-ID", ws)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 409 || strings.Contains(string(raw), "SECRET_BACKEND_DETAIL") {
		t.Fatalf("response %d %s", resp.StatusCode, raw)
	}
	rc, err := probe.Storage.Get(t.Context(), probe.key)
	if err != nil {
		t.Fatalf("blob lost: %v", err)
	}
	rc.Close()
}

func TestCellReceiptRaceAfterCapabilityRemoved(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	ref := csRegister(t, s, a, ws, "work", "claude")
	var cell db.CoresearchCell
	if code := s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "x", "source": "1"}, &cell); code != 201 {
		t.Fatal(code)
	}
	work := csWork(t, s, a, "work", ref)
	conn, err := s.Env.Owner.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	sum := sha256.Sum256([]byte(ws + "/coresearch/cells/" + cell.ID.String() + ".json"))
	lock := int64(binary.BigEndian.Uint64(sum[:8]))
	if _, err = conn.Exec(t.Context(), "SELECT pg_advisory_lock($1)", lock); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", lock)
	output := json.RawMessage(`"` + strings.Repeat("x", (1<<20)+1) + `"`)
	in := struct {
		Status         string          `json:"status"`
		ExecutionCount *int32          `json:"execution_count"`
		Outputs        json.RawMessage `json:"outputs"`
	}{Status: "ok", Outputs: output}
	digest := coresearchReceipt(in)
	url := s.URL + "/api/daemon/coresearch/cells/" + cell.ID.String() + "/finish?daemon_id=work&claim_token=" + work.Cells[0].ClaimToken.String()
	body, _ := json.Marshal(map[string]any{"status": "ok", "outputs": output})
	token := s.personalToken(t, a)
	done := make(chan int, 1)
	go func() {
		req, _ := http.NewRequestWithContext(t.Context(), "POST", url, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Workspace-ID", ws)
		req.Header.Set("Content-Type", "application/json")
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			done <- 0
			return
		}
		resp.Body.Close()
		done <- resp.StatusCode
	}()
	deadline := time.Now().Add(5 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		if err = s.Env.Owner.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%pg_advisory_lock%')").Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("request did not wait for cell lock")
	}
	if _, err = s.Env.Owner.Exec(t.Context(), "UPDATE coresearch_cell SET status='ok', receipt_sha=$1 WHERE id=$2", digest, cell.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Env.Owner.Exec(t.Context(), "DROP TABLE public.coresearch_cell_generation_capability"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(t.Context(), "SELECT pg_advisory_unlock($1)", lock); err != nil {
		t.Fatal(err)
	}
	if code := <-done; code != 204 {
		t.Fatalf("receipt replay status %d", code)
	}
}

type delayedCellDeleteStorage struct {
	storage.Storage
	once          sync.Once
	mu            sync.Mutex
	keys          []string
	deleted       string
	afterFirstPut func()
	entered       chan struct{}
	release       chan struct{}
}

func (s *delayedCellDeleteStorage) Put(ctx context.Context, key string, r io.Reader, n int64) error {
	if err := s.Storage.Put(ctx, key, r, n); err != nil {
		return err
	}
	s.mu.Lock()
	s.keys = append(s.keys, key)
	s.mu.Unlock()
	s.once.Do(s.afterFirstPut)
	return nil
}
func (s *delayedCellDeleteStorage) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	s.deleted = key
	s.mu.Unlock()
	close(s.entered)
	<-s.release
	// Simulate a remote deletion that was already accepted despite local cancellation.
	return s.Storage.Delete(context.Background(), key)
}
func TestCellDelayedDeleteAfterLockLossCannotRemoveRetry(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	path, _ := csChat(t, s, a, ws, p.ID)
	ref := csRegister(t, s, a, ws, "work", "claude")
	var cell db.CoresearchCell
	if code := s.call(t, "POST", path+"/cells", a, ws, map[string]string{"kernel": "x", "source": "1"}, &cell); code != 201 {
		t.Fatal(code)
	}
	work := csWork(t, s, a, "work", ref)
	probe := &delayedCellDeleteStorage{Storage: s.H.Blobs, entered: make(chan struct{}), release: make(chan struct{})}
	probe.afterFirstPut = func() {
		_, _ = s.Env.Owner.Exec(context.Background(), "UPDATE coresearch_cell SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", cell.ID)
	}
	s.H.Blobs = probe
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(probe.release) }) }
	defer release()
	url := s.URL + "/api/daemon/coresearch/cells/" + cell.ID.String() + "/finish?daemon_id=work&claim_token=" + work.Cells[0].ClaimToken.String()
	body, _ := json.Marshal(map[string]any{"status": "ok", "outputs": strings.Repeat("x", (1<<20)+1)})
	token := s.personalToken(t, a)
	send := func() int {
		ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Workspace-ID", ws)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	first := make(chan int, 1)
	go func() { first <- send() }()
	select {
	case <-probe.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not start delete")
	}
	// Terminate the advisory lock session and the cleanup transaction while the
	// storage service still has an in-flight delete for the old generation.
	rows, err := s.Env.Owner.Query(t.Context(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE pid<>pg_backend_pid() AND datname=current_database() AND (query LIKE 'SELECT pg_advisory_lock(%' OR query LIKE 'SELECT public.coresearch_cell_candidate_referenced(%')`)
	if err != nil {
		t.Fatal(err)
	}
	killed := 0
	for rows.Next() {
		var ok bool
		if err = rows.Scan(&ok); err != nil {
			t.Fatal(err)
		}
		if ok {
			killed++
		}
	}
	rows.Close()
	if killed < 2 {
		release()
		t.Fatalf("terminated %d lock sessions", killed)
	}
	if _, err = s.Env.Owner.Exec(t.Context(), "UPDATE coresearch_cell SET lease_expires_at=clock_timestamp()+interval '2 minutes' WHERE id=$1", cell.ID); err != nil {
		release()
		t.Fatal(err)
	}
	if code := send(); code != 204 {
		release()
		t.Fatalf("retry status %d", code)
	}
	probe.mu.Lock()
	keys := append([]string(nil), probe.keys...)
	probe.mu.Unlock()
	if len(keys) != 2 || keys[0] == keys[1] {
		release()
		t.Fatalf("generation keys %v", keys)
	}
	release()
	select {
	case code := <-first:
		if code != 409 {
			t.Fatalf("first status %d", code)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("first request did not finish")
	}
	probe.mu.Lock()
	deleted := probe.deleted
	probe.mu.Unlock()
	if deleted != keys[0] {
		t.Fatalf("deleted %q, expected %q", deleted, keys[0])
	}
	old, err := probe.Storage.Get(t.Context(), keys[0])
	if old != nil {
		old.Close()
	}
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("old key retained: %v", err)
	}
	var stored *string
	if err = s.Env.Owner.QueryRow(t.Context(), "SELECT outputs_blob FROM coresearch_cell WHERE id=$1", cell.ID).Scan(&stored); err != nil || stored == nil || *stored != keys[1] {
		t.Fatalf("stored output %v: %v", stored, err)
	}
	rc, err := probe.Storage.Get(t.Context(), keys[1])
	if err != nil {
		t.Fatalf("delayed delete removed retry: %v", err)
	}
	rc.Close()
}
