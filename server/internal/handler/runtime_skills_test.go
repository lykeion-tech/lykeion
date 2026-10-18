package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/skill"
	db "lykeion/server/pkg/db/generated"
)

type runtimeSkillFixture struct {
	s                  *server
	jwt, token, ws, rt string
}

func runtimeSkillSetup(t *testing.T) runtimeSkillFixture {
	t.Helper()
	s := newServer(t)
	jwt := s.signIn(t, "runtime-owner@lab.org")
	ws := s.workspace(t, jwt, "lab")
	token := s.personalToken(t, jwt)
	var runtimes []db.Runtime
	if c := s.call(t, "POST", "/api/daemon/register", token, "", map[string]any{"workspace_id": ws.ID, "daemon_id": "skills-daemon", "device_name": "laptop", "harnesses": []string{"claude"}}, &runtimes); c != 200 {
		t.Fatal(c)
	}
	return runtimeSkillFixture{s, jwt, token, ws.ID, runtimes[0].ID.String()}
}
func (f runtimeSkillFixture) start(t *testing.T, keys []string) db.RuntimeSkillRequest {
	t.Helper()
	suffix := ""
	var in any
	if keys != nil {
		suffix = "/import"
		in = map[string]any{"keys": keys}
	}
	var job db.RuntimeSkillRequest
	if c := f.s.call(t, "POST", "/api/runtimes/"+f.rt+"/local-skills"+suffix, f.jwt, f.ws, in, &job); c != 200 {
		t.Fatalf("start: %d %+v", c, job)
	}
	return job
}
func (f runtimeSkillFixture) claim(t *testing.T, operation string) db.RuntimeSkillRequest {
	t.Helper()
	suffix := ""
	if operation == "import" {
		suffix = "/import"
	}
	var jobs []struct {
		db.RuntimeSkillRequest
		Harness string `json:"harness"`
	}
	if c := f.s.call(t, "POST", "/api/daemon/runtimes/"+f.rt+"/local-skills"+suffix, f.token, f.ws, map[string]any{"daemon_id": "skills-daemon"}, &jobs); c != 200 || len(jobs) != 1 || jobs[0].AcquisitionID == nil || jobs[0].Harness != "claude" {
		t.Fatalf("claim: %d %+v", c, jobs)
	}
	return jobs[0].RuntimeSkillRequest
}
func (f runtimeSkillFixture) resultPath(j db.RuntimeSkillRequest, key string) string {
	suffix := ""
	if j.Operation == "import" {
		suffix = "/import"
	}
	q := url.Values{"daemon_id": {"skills-daemon"}, "acquisition_id": {j.AcquisitionID.String()}, "key": {key}}
	return "/api/daemon/runtimes/" + f.rt + "/local-skills" + suffix + "/" + j.ID.String() + "/result?" + q.Encode()
}
func runtimeSkillZip(t *testing.T, name string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for p, data := range map[string][]byte{"SKILL.md": []byte("---\nname: " + name + "\ndescription: Copied research\n---\n"), ".hidden": {0, 255, 128, 42}} {
		w, err := z.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func (f runtimeSkillFixture) upload(t *testing.T, j db.RuntimeSkillRequest, key string, data []byte) int {
	status, _ := f.uploadResult(t, j, key, data)
	return status
}
func (f runtimeSkillFixture) uploadResult(t *testing.T, j db.RuntimeSkillRequest, key string, data []byte) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest("POST", f.s.URL+f.resultPath(j, key), bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("X-Workspace-ID", f.ws)
	req.Header.Set("Content-Type", "application/zip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}
func (f runtimeSkillFixture) poll(t *testing.T, j db.RuntimeSkillRequest) db.RuntimeSkillRequest {
	t.Helper()
	suffix := ""
	if j.Operation == "import" {
		suffix = "/import"
	}
	var out db.RuntimeSkillRequest
	if c := f.s.call(t, "GET", "/api/runtimes/"+f.rt+"/local-skills"+suffix+"/"+j.ID.String(), f.jwt, f.ws, nil, &out); c != 200 {
		t.Fatal(c)
	}
	return out
}
func TestRuntimeSkillCopyRequiresRuntimeOwner(t *testing.T) {
	f := runtimeSkillSetup(t)
	admin := f.s.member(t, f.jwt, f.ws, "other-admin@lab.org", "admin")
	for _, suffix := range []string{"", "/import"} {
		if c := f.s.call(t, "POST", "/api/runtimes/"+f.rt+"/local-skills"+suffix, admin, f.ws, map[string]any{"keys": []string{"selected"}}, nil); c != 403 {
			t.Fatalf("copied another person's setup: %d", c)
		}
	}
	other := f.s.workspace(t, f.jwt, "other")
	if c := f.s.call(t, "POST", "/api/runtimes/"+f.rt+"/local-skills", f.jwt, other.ID, nil, nil); c != 404 {
		t.Fatalf("foreign workspace: %d", c)
	}
	if _, err := f.s.Env.Owner.Exec(context.Background(), `UPDATE runtime SET status='offline' WHERE id=$1`, f.rt); err != nil {
		t.Fatal(err)
	}
	if c := f.s.call(t, "POST", "/api/runtimes/"+f.rt+"/local-skills", f.jwt, f.ws, nil, nil); c != 503 {
		t.Fatalf("offline: %d", c)
	}
}
func TestRuntimeSkillJobsFenceLateResults(t *testing.T) {
	f := runtimeSkillSetup(t)
	f.start(t, []string{"selected"})
	j := f.claim(t, "import")
	forged := j
	id := uuid.New()
	forged.AcquisitionID = &id
	if c := f.upload(t, forged, "selected", runtimeSkillZip(t, "bad-claim")); c != 409 {
		t.Fatalf("forged claim: %d", c)
	}
	if c := f.s.call(t, "POST", f.resultPath(j, "selected")+"&daemon_id=other", f.token, f.ws, map[string]string{"error": "failed"}, nil); c != 400 && c != 403 {
		t.Fatalf("forged daemon identity: %d", c)
	}
	if _, err := f.s.Env.Owner.Exec(context.Background(), `UPDATE runtime_skill_request SET acquisition_id=$2 WHERE id=$1`, j.ID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if c := f.upload(t, j, "selected", runtimeSkillZip(t, "reclaimed")); c != 409 {
		t.Fatalf("reclaimed: %d", c)
	}
	if _, err := f.s.Env.Owner.Exec(context.Background(), `UPDATE runtime_skill_request SET expires_at=now()-interval '1 second' WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.poll(t, j); got.Status != "timeout" {
		t.Fatalf("expired job: %+v", got)
	}
	if c := f.upload(t, j, "selected", runtimeSkillZip(t, "late")); c != 409 {
		t.Fatalf("expired result: %d", c)
	}
	if f.s.count(t, `SELECT count(*) FROM workspace_skill`) != 0 {
		t.Fatal("late result accepted")
	}
	// Pending and completed results are durable across a replacement server.
	list := f.start(t, nil)
	replacement := newServerOn(t, f.s.Env)
	replacement.H.Blobs = f.s.H.Blobs
	f.s = replacement
	claimed := f.claim(t, "list")
	if claimed.ID != list.ID {
		t.Fatal("server replacement lost request")
	}
	if c := f.s.call(t, "POST", f.resultPath(claimed, ""), f.token, f.ws, map[string]any{"status": "completed", "skills": []any{}}, nil); c != 200 {
		t.Fatal(c)
	}
	if got := f.poll(t, claimed); got.Status != "completed" {
		t.Fatal(got.Status)
	}
	if _, err := f.s.Env.Owner.Exec(context.Background(), `UPDATE runtime_skill_request SET completed_at=now()-interval '6 minutes' WHERE id=$1`, claimed.ID); err != nil {
		t.Fatal(err)
	}
	if c := f.s.call(t, "GET", "/api/runtimes/"+f.rt+"/local-skills/"+claimed.ID.String(), f.jwt, f.ws, nil, nil); c != 404 {
		t.Fatalf("terminal retention: %d", c)
	}
}
func TestRuntimeSkillBatchKeepsSuccesses(t *testing.T) {
	f := runtimeSkillSetup(t)
	keys := []string{}
	for i := 0; i < 10; i++ {
		keys = append(keys, fmt.Sprintf("key-%d", i))
	}
	f.start(t, keys)
	j := f.claim(t, "import")
	if c := f.upload(t, j, keys[0], runtimeSkillZip(t, "copied-method")); c != 200 {
		t.Fatal(c)
	}
	// A new handler uses the same store; no process-local request state may be required.
	replacement := newServerOn(t, f.s.Env)
	replacement.H.Blobs = f.s.H.Blobs
	f.s = replacement
	if c := f.upload(t, j, keys[0], runtimeSkillZip(t, "must-not-import")); c != 200 {
		t.Fatalf("duplicate receipt: %d", c)
	}
	if c := f.upload(t, j, keys[1], runtimeSkillZip(t, "copied-method")); c != 200 {
		t.Fatal(c)
	}
	for i := 2; i < 10; i++ {
		if c := f.s.call(t, "POST", f.resultPath(j, keys[i]), f.token, f.ws, map[string]string{"error": "unreadable skill"}, nil); c != 200 {
			t.Fatal(c)
		}
	}
	got := f.poll(t, j)
	var payload struct {
		Items []struct {
			Key     string `json:"key"`
			Status  string `json:"status"`
			SkillID string `json:"skill_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(got.Result, &payload); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, item := range payload.Items {
		counts[item.Status]++
	}
	if got.Status != "completed" || counts["imported"] != 1 || counts["conflict"] != 1 || counts["failed"] != 8 || len(payload.Items) != 10 {
		t.Fatalf("batch outcomes lost: %s %s", got.Status, got.Result)
	}
	if f.s.count(t, `SELECT count(*) FROM workspace_skill`) != 1 || f.s.count(t, `SELECT count(*) FROM event WHERE type='skill.created'`) != 1 {
		t.Fatal("duplicate imports")
	}
	if f.s.count(t, `SELECT count(*) FROM event WHERE type='runtime_skill_request.updated' AND (payload ? 'files' OR payload ? 'content' OR payload ? 'items')`) != 0 {
		t.Fatal("event leaked result data")
	}
	var detail skill.Detail
	if c := f.s.call(t, "GET", "/api/skills/"+payload.Items[0].SkillID, f.jwt, f.ws, nil, &detail); c != 200 || detail.Source.Kind != "runtime" || detail.Source.RuntimeID != f.rt {
		t.Fatalf("source: %d %+v", c, detail.Source)
	}
	partial := f.start(t, []string{"one", "missing"})
	partial = f.claim(t, "import")
	if c := f.upload(t, partial, "one", runtimeSkillZip(t, "partial-method")); c != 200 {
		t.Fatal(c)
	}
	if _, err := f.s.Env.Owner.Exec(context.Background(), `UPDATE runtime_skill_request SET expires_at=now()-interval '1 second' WHERE id=$1`, partial.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.poll(t, partial); got.Status != "timeout" || !bytes.Contains(got.Result, []byte("imported")) {
		t.Fatalf("timeout lost success: %+v", got)
	}
}
func TestRuntimeSkillPublicationRechecksMembershipAndRuntime(t *testing.T) {
	for _, change := range []string{"membership", "runtime", "acquisition", "expiry", "workspace"} {
		t.Run(change, func(t *testing.T) {
			f := runtimeSkillSetup(t)
			f.start(t, []string{"selected"})
			j := f.claim(t, "import")
			f.s.H.Blobs = skillBlobHook{Storage: f.s.H.Blobs, hook: func() error {
				ctx := context.Background()
				tx, err := f.s.Env.Owner.Begin(ctx)
				if err != nil {
					return err
				}
				defer tx.Rollback(ctx)
				if _, err = tx.Exec(ctx, `SELECT id FROM workspace WHERE id=$1 FOR UPDATE NOWAIT`, f.ws); err != nil {
					return err
				}
				switch change {
				case "membership":
					_, err = tx.Exec(ctx, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, f.ws, j.OwnerID)
				case "runtime":
					_, err = tx.Exec(ctx, `DELETE FROM runtime WHERE id=$1`, f.rt)
				case "workspace":
					_, err = tx.Exec(ctx, `DELETE FROM workspace WHERE id=$1`, f.ws)
				case "expiry":
					_, err = tx.Exec(ctx, `UPDATE runtime_skill_request SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, j.ID)
				case "acquisition":
					_, err = tx.Exec(ctx, `UPDATE runtime_skill_request SET acquisition_id=$2 WHERE id=$1`, j.ID, uuid.New())
				}
				if err != nil {
					return err
				}
				return tx.Commit(ctx)
			}}
			c := f.upload(t, j, "selected", runtimeSkillZip(t, "revoked-method"))
			if c != 404 && c != 409 {
				t.Fatalf("publication after %s change: %d", change, c)
			}
			if f.s.count(t, `SELECT count(*) FROM workspace_skill`) != 0 || f.s.count(t, `SELECT count(*) FROM event WHERE type='skill.created'`) != 0 {
				t.Fatal("late publication")
			}
		})
	}
}

func TestRuntimeSkillConcurrentDuplicateAndRevokedReceipt(t *testing.T) {
	f := runtimeSkillSetup(t)
	f.start(t, []string{"selected"})
	j := f.claim(t, "import")
	data := runtimeSkillZip(t, "once-method")
	var first, second []byte
	results := raceCalls(t, func() int { var status int; status, first = f.uploadResult(t, j, "selected", data); return status }, func() int { var status int; status, second = f.uploadResult(t, j, "selected", data); return status })
	if !bytes.Equal(first, second) {
		t.Fatalf("simultaneous receipts differ: %s vs %s", first, second)
	}
	if results[0] != 200 || results[1] != 200 {
		t.Fatalf("simultaneous delivery: %v", results)
	}
	got := f.poll(t, j)
	if got.Status != "completed" || f.s.count(t, `SELECT count(*) FROM workspace_skill`) != 1 || f.s.count(t, `SELECT count(*) FROM workspace_skill_version`) != 1 || f.s.count(t, `SELECT count(*) FROM event WHERE type='skill.created'`) != 1 {
		t.Fatal("simultaneous results duplicated publication")
	}
	if c := f.upload(t, j, "selected", data); c != 200 {
		t.Fatal(c)
	}
	after := f.poll(t, j)
	if !bytes.Equal(got.Result, after.Result) {
		t.Fatal("replay changed receipt")
	}
	if _, err := f.s.Env.Owner.Exec(context.Background(), `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, f.ws, j.OwnerID); err != nil {
		t.Fatal(err)
	}
	if c := f.upload(t, j, "selected", data); c != 404 {
		t.Fatalf("revoked owner read receipt: %d", c)
	}
}

func TestRuntimeSkillReceiptFailureRollsBackPublication(t *testing.T) {
	f := runtimeSkillSetup(t)
	f.start(t, []string{"selected"})
	j := f.claim(t, "import")
	if _, err := f.s.Env.Owner.Exec(context.Background(), `ALTER TABLE runtime_skill_request ADD CONSTRAINT reject_completed_test CHECK (status<>'completed')`); err != nil {
		t.Fatal(err)
	}
	if c := f.upload(t, j, "selected", runtimeSkillZip(t, "atomic-method")); c != 500 {
		t.Fatalf("receipt failure: %d", c)
	}
	if f.s.count(t, `SELECT count(*) FROM workspace_skill`) != 0 || f.s.count(t, `SELECT count(*) FROM workspace_skill_version`) != 0 || f.s.count(t, `SELECT count(*) FROM event WHERE type='skill.created'`) != 0 {
		t.Fatal("failed receipt left publication")
	}
	if _, err := f.s.Env.Owner.Exec(context.Background(), `ALTER TABLE runtime_skill_request DROP CONSTRAINT reject_completed_test`); err != nil {
		t.Fatal(err)
	}
	if c := f.upload(t, j, "selected", runtimeSkillZip(t, "atomic-method")); c != 200 {
		t.Fatal(c)
	}
}

type runtimeSkillCountingReader struct{ reads int }

func (r *runtimeSkillCountingReader) Read([]byte) (int, error) {
	r.reads++
	return 0, fmt.Errorf("body should not be read")
}
func TestRuntimeSkillArchiveAuthenticatesBeforeBodyAndKeepsCap(t *testing.T) {
	f := runtimeSkillSetup(t)
	f.start(t, []string{"selected"})
	j := f.claim(t, "import")
	body := &runtimeSkillCountingReader{}
	req := httptest.NewRequest("POST", f.resultPath(j, "selected"), body)
	req.Header.Set("Content-Type", "application/zip")
	req.Header.Set("X-Workspace-ID", f.ws)
	rec := httptest.NewRecorder()
	NewRouter(f.s.H).ServeHTTP(rec, req)
	if rec.Code != 401 || body.reads != 0 {
		t.Fatalf("read unauthorized upload: %d reads=%d", rec.Code, body.reads)
	}
	wrong := strings.Replace(f.resultPath(j, "selected"), "daemon_id=skills-daemon", "daemon_id=other", 1)
	if c := f.s.call(t, "POST", wrong, f.token, f.ws, map[string]string{"error": "failed"}, nil); c != 403 {
		t.Fatalf("forged daemon: %d", c)
	}
	if c := f.upload(t, j, "selected", make([]byte, skill.ArchiveBytes+1)); c != 413 {
		t.Fatalf("archive cap: %d", c)
	}
	if got := f.poll(t, j); got.Status != "running" || len(got.Result) > 2 {
		t.Fatal("oversize published outcome")
	}
	if c := f.upload(t, j, "selected", []byte("invalid zip")); c != 200 {
		t.Fatal(c)
	}
	if got := f.poll(t, j); got.Status != "completed" || !bytes.Contains(got.Result, []byte(`"failed"`)) {
		t.Fatal("invalid archive did not retain per-item failure")
	}
}

func TestRuntimeSkillOwnerReplacementCannotPublishOrReadReceipt(t *testing.T) {
	f := runtimeSkillSetup(t)
	other := f.s.member(t, f.jwt, f.ws, "new-owner@lab.org", "admin")
	otherID := f.s.userID(t, other)
	f.start(t, []string{"selected"})
	j := f.claim(t, "import")
	f.s.H.Blobs = skillBlobHook{Storage: f.s.H.Blobs, hook: func() error {
		ctx := context.Background()
		tx, err := f.s.Env.Owner.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, `SELECT id FROM workspace WHERE id=$1 FOR UPDATE NOWAIT`, f.ws); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM runtime WHERE id=$1`, f.rt); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO runtime (id,workspace_id,owner_id,daemon_id,harness,device_name,status) VALUES ($1,$2,$3,'skills-daemon','claude','replacement','online')`, f.rt, f.ws, otherID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}}
	if c := f.upload(t, j, "selected", runtimeSkillZip(t, "changed-owner")); c != 403 {
		t.Fatalf("old owner published after replacement: %d", c)
	}
	if f.s.count(t, `SELECT count(*) FROM workspace_skill`) != 0 {
		t.Fatal("new owner runtime accepted old job")
	}
	if c := f.s.call(t, "GET", "/api/runtimes/"+f.rt+"/local-skills/import/"+j.ID.String(), f.jwt, f.ws, nil, nil); c != 403 {
		t.Fatalf("old owner read: %d", c)
	}
}
