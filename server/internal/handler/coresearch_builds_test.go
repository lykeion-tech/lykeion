package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/coresearch/doc"
	"lykeion/server/internal/daemon"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func TestBuildQueueRules(t *testing.T) {
	s, a, b, ws, p, f := paperFixture(t)
	root := csProjects + "/" + p.ID
	csRegister(t, s, b, ws, "own", "claude")
	s.call(t, "GET", root+"/docs/"+f, a, ws, nil, nil)
	s.call(t, "POST", root+"/docs/"+f+"/steps", a, ws, map[string]any{"base": 0, "client_id": "a", "steps": []any{[]any{[]any{6, "queued text"}}}}, nil)
	var one, two db.CoresearchBuild
	if c := s.call(t, "POST", root+"/builds", b, ws, nil, &one); c != 201 {
		t.Fatalf("queue %d", c)
	}
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT sources FROM coresearch_build WHERE id=$1`, one.ID).Scan(&one.Sources); e != nil {
		t.Fatal(e)
	}
	if c := s.call(t, "POST", root+"/builds", b, ws, nil, &two); c != 201 {
		t.Fatal(c)
	}
	if one.ID == two.ID || one.Seq != 1 || two.Seq != 2 {
		t.Fatal("queued build not replaced", one, two)
	}
	var superseded string
	var frozen json.RawMessage
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT status,sources FROM coresearch_build WHERE id=$1`, one.ID).Scan(&superseded, &frozen); e != nil {
		t.Fatal(e)
	}
	var initialRefs, retainedRefs []coresearch.FileRef
	json.Unmarshal(one.Sources, &initialRefs)
	json.Unmarshal(frozen, &retainedRefs)
	if superseded != "aborted" || !reflect.DeepEqual(initialRefs, retainedRefs) {
		t.Fatalf("replaced build history changed: %s %s", superseded, frozen)
	}
	other := csRegister(t, s, a, ws, "other", "claude")
	if work := csWork(t, s, a, "other", other); len(work.Builds) != 0 {
		t.Fatal("wrong owner claimed")
	}
	own := csRegister(t, s, b, ws, "own", "claude")
	work := csWork(t, s, b, "own", own)
	if len(work.Builds) != 1 || work.Builds[0].ID != two.ID {
		t.Fatalf("claim %+v", work)
	}
	if len(work.Builds[0].Docs) != 1 || work.Builds[0].Docs[0].Text != "queued text" || work.Builds[0].Docs[0].Version != 1 {
		t.Fatalf("claimed live text %+v", work.Builds[0].Docs)
	}
	s.call(t, "POST", root+"/docs/"+f+"/steps", a, ws, map[string]any{"base": 1, "client_id": "a", "steps": []any{[]any{[]any{11, "later text"}}}}, nil)
	if code := finishBuildTest(t, s, b, ws, work.Builds[0], "ok"); code != 204 {
		t.Fatalf("finish %d", code)
	}
	var versions []db.CoresearchVersion
	s.call(t, "GET", root+"/versions", b, ws, nil, &versions)
	if len(versions) != 1 || versions[0].Title != "Build 2" {
		t.Fatalf("versions %+v", versions)
	}
	var refs []coresearch.FileRef
	json.Unmarshal(versions[0].Files, &refs)
	if len(refs) != 1 || refs[0].DocVersion == nil || *refs[0].DocVersion != 1 {
		t.Fatalf("source refs %+v", refs)
	}
	s.call(t, "POST", root+"/builds", b, ws, nil, &two)
	s.call(t, "DELETE", root+"/members/"+s.userID(t, b), a, ws, nil, nil)
	var status, reason string
	s.Env.Owner.QueryRow(t.Context(), `SELECT status,failure_reason FROM coresearch_build WHERE id=$1`, two.ID).Scan(&status, &reason)
	if status != "aborted" || reason != "coresearch_access_revoked" {
		t.Fatal(status, reason)
	}
	if c := s.call(t, "POST", root+"/builds", b, ws, nil, nil); c != 404 {
		t.Fatal(c)
	}
}
func finishBuildTest(t *testing.T, s *server, who, ws string, b coresearch.ClaimedBuild, status string) int {
	t.Helper()
	var data bytes.Buffer
	mw := multipart.NewWriter(&data)
	mw.WriteField("meta", fmt.Sprintf(`{"status":%q,"pages":1}`, status))
	for _, name := range []string{"pdf", "log", "synctex"} {
		w, _ := mw.CreateFormFile(name, name)
		w.Write([]byte("test " + name))
	}
	mw.Close()
	req, _ := http.NewRequest("POST", s.URL+"/api/daemon/coresearch/builds/"+b.ID.String()+"/finish?daemon_id=own&claim_token="+b.ClaimToken.String(), &data)
	req.Header.Set("Authorization", "Bearer "+s.personalToken(t, who))
	req.Header.Set("X-Workspace-ID", ws)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	return res.StatusCode
}

func TestBuildRunningMemberRevocation(t *testing.T) {
	s, a, b, ws, p, _ := paperFixture(t)
	root := csProjects + "/" + p.ID
	ref := csRegister(t, s, b, ws, "own", "claude")
	s.call(t, "POST", root+"/builds", b, ws, nil, nil)
	work := csWork(t, s, b, "own", ref)
	if len(work.Builds) != 1 {
		t.Fatal(work)
	}
	s.call(t, "DELETE", root+"/members/"+s.userID(t, b), a, ws, nil, nil)
	if c := finishBuildTest(t, s, b, ws, work.Builds[0], "ok"); c != 404 {
		t.Fatalf("revoked late finish %d", c)
	}
	var n int
	s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_version WHERE project_id=$1`, p.ID).Scan(&n)
	if n != 0 {
		t.Fatal("revoked build published a version")
	}
}

func TestBuildSettlementOnlyAppliedHunks(t *testing.T) {
	for _, applied := range []bool{false, true} {
		t.Run(fmt.Sprint(applied), func(t *testing.T) {
			s, a, _, ws, p, f := paperFixture(t)
			_, chat := csChat(t, s, a, ws, p.ID)
			var run db.AgentRun
			if e := s.H.Store.InWorkspace(t.Context(), uuid.MustParse(ws), uuid.MustParse(s.userID(t, a)), func(tx *store.Tx) error {
				var e error
				run, e = coresearch.EnqueueTurn(t.Context(), tx, s.H.AgentRuns, chat, chat.OwnerID, "edit")
				return e
			}); e != nil {
				t.Fatal(e)
			}
			if applied {
				s.call(t, "GET", csProjects+"/"+p.ID+"/docs/"+f, a, ws, nil, nil)
				if e := s.H.Store.InWorkspace(t.Context(), uuid.MustParse(ws), uuid.MustParse(s.userID(t, a)), func(tx *store.Tx) error {
					file, e := tx.Q.GetCoresearchFile(t.Context(), db.GetCoresearchFileParams{WorkspaceID: tx.WS, ProjectID: uuid.MustParse(p.ID), ID: uuid.MustParse(f)})
					if e != nil {
						return e
					}
					return func() error {
						_, e := doc.Push(t.Context(), tx, doc.File{Record: file}, 0, []doc.Step{{Changes: json.RawMessage(`[6,[0," applied"]]`), ClientID: "agent"}}, doc.Author{AgentRunID: &run.ID})
						return e
					}()
				}); e != nil {
					t.Fatal(e)
				}
			}

			run.Status = "completed"
			csTerminal(t, s, run)
			svc := &coresearch.Service{Store: s.H.Store, Blobs: s.H.Blobs}
			hook := svc.OnSettlement(nil)
			if applied {
				svc.Blobs = buildFailStorage{Storage: s.H.Blobs}
				if e := hook(t.Context(), service.Settlement{ID: run.ID, AgentRun: run}); e == nil {
					t.Fatal("expected staged storage failure")
				}
				svc.Blobs = s.H.Blobs
			}
			for i := 0; i < 2; i++ {
				if e := hook(t.Context(), service.Settlement{ID: run.ID, AgentRun: run}); e != nil {
					t.Fatal(e)
				}
			}
			var n int
			s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_build WHERE project_id=$1 AND requested_by=$2`, p.ID, s.userID(t, a)).Scan(&n)
			want := 0
			if applied {
				want = 1
			}
			if n != want {
				t.Fatalf("build count %d want %d", n, want)
			}
			if applied {
				var original uuid.UUID
				s.Env.Owner.QueryRow(t.Context(), `SELECT coresearch_build_id FROM agent_run WHERE id=$1`, run.ID).Scan(&original)
				var manual db.CoresearchBuild
				csRegister(t, s, a, ws, "own", "claude")
				if code := s.call(t, "POST", csProjects+"/"+p.ID+"/builds", a, ws, nil, &manual); code != 201 {
					t.Fatal(code)
				}
				if manual.ID == original {
					t.Fatal("manual request mutated automatic build")
				}
				if e := hook(t.Context(), service.Settlement{ID: run.ID, AgentRun: run}); e != nil {
					t.Fatal(e)
				}
				var linked uuid.UUID
				s.Env.Owner.QueryRow(t.Context(), `SELECT coresearch_build_id FROM agent_run WHERE id=$1`, run.ID).Scan(&linked)
				s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_build WHERE project_id=$1`, p.ID).Scan(&n)
				if linked != original || n != 2 {
					t.Fatalf("superseded settlement replay linked=%s original=%s count=%d", linked, original, n)
				}
			}

		})
	}
}

func TestBuildLeaseAndReceipt(t *testing.T) {
	s, owner, a, ws, p, _ := paperFixture(t)
	root := csProjects + "/" + p.ID
	ref := csRegister(t, s, a, ws, "own", "claude")
	var first, next db.CoresearchBuild
	s.call(t, "POST", root+"/builds", a, ws, nil, &first)
	work := csWork(t, s, a, "own", ref)
	if len(work.Builds) != 1 {
		t.Fatal(work)
	}
	s.call(t, "POST", root+"/builds", a, ws, nil, &next)
	if work2 := csWork(t, s, a, "own", ref); len(work2.Builds) != 0 {
		t.Fatal("parallel requester build", work2)
	}
	if c := finishBuildTest(t, s, a, ws, work.Builds[0], "ok"); c != 204 {
		t.Fatal(c)
	}
	if c := finishBuildTest(t, s, a, ws, work.Builds[0], "ok"); c != 204 {
		t.Fatal("retry", c)
	}
	var n int
	s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_version WHERE project_id=$1`, p.ID).Scan(&n)
	if n != 1 {
		t.Fatal("duplicate version", n)
	}
	var title string
	s.Env.Owner.QueryRow(t.Context(), `SELECT title FROM coresearch_version WHERE project_id=$1`, p.ID).Scan(&title)
	if title != "Build 1" {
		t.Fatal(title)
	}
	work = csWork(t, s, a, "own", ref)
	if len(work.Builds) != 1 {
		t.Fatal(work)
	}
	s.Env.Owner.Exec(t.Context(), `UPDATE coresearch_build SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, next.ID)
	if c := finishBuildTest(t, s, a, ws, work.Builds[0], "ok"); c != 409 {
		t.Fatal("expired finish", c)
	}
	if e := coresearch.Sweep(t.Context(), s.H.Store); e != nil {
		t.Fatal(e)
	}
	var status string
	s.Env.Owner.QueryRow(t.Context(), `SELECT status FROM coresearch_build WHERE id=$1`, next.ID).Scan(&status)
	if status != "failed" {
		t.Fatal("lease not swept", status)
	}
	if c := s.call(t, "GET", root+"/builds/1.pdf", owner, ws, nil, nil); c != 200 {
		t.Fatal("artifact", c)
	}
}

type buildStageStorage struct {
	storage.Storage
	once   sync.Once
	before func()
}

func (s *buildStageStorage) Put(ctx context.Context, key string, r io.Reader, n int64) error {
	s.once.Do(s.before)
	return s.Storage.Put(ctx, key, r, n)
}
func TestBuildSnapshotsJournalOpenedDuringStaging(t *testing.T) {
	s, a, _, ws, p, f := paperFixture(t)
	root := csProjects + "/" + p.ID
	csRegister(t, s, a, ws, "own", "claude")
	_, second := csUpload(t, s, a, ws, p.ID, "second.tex", 0, false, []byte("old"))
	s.call(t, "GET", root+"/docs/"+f, a, ws, nil, nil)
	s.call(t, "POST", root+"/docs/"+f+"/steps", a, ws, map[string]any{"base": 0, "client_id": "a", "steps": []any{[]any{[]any{6, "changed"}}}}, nil)
	s.H.Blobs = &buildStageStorage{Storage: s.H.Blobs, before: func() {
		url := root + "/docs/" + second.FileID.String()
		if c := s.call(t, "GET", url, a, ws, nil, nil); c != 200 {
			t.Fatal(c)
		}
		if c := s.call(t, "POST", url+"/steps", a, ws, map[string]any{"base": 0, "client_id": "a", "steps": []any{[]any{[]any{3, "new"}}}}, nil); c != 200 {
			t.Fatal(c)
		}
	}}
	var b db.CoresearchBuild
	if c := s.call(t, "POST", root+"/builds", a, ws, nil, &b); c != 201 {
		t.Fatal(c)
	}
	var refs []coresearch.FileRef
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT sources FROM coresearch_build WHERE id=$1`, b.ID).Scan(&b.Sources); e != nil {
		t.Fatal(e)
	}
	json.Unmarshal(b.Sources, &refs)
	for _, ref := range refs {
		if ref.FileID == second.FileID {
			if ref.DocVersion == nil || *ref.DocVersion != 1 {
				t.Fatalf("new journal omitted %+v", ref)
			}
			return
		}
	}
	t.Fatal("missing second source")
}

type buildFailStorage struct{ storage.Storage }

func (s buildFailStorage) Put(context.Context, string, io.Reader, int64) error {
	return errors.New("injected staged storage failure")
}

func TestBuildArtifactQuota(t *testing.T) {
	s, a, _, ws, p, f := paperFixture(t)
	root := csProjects + "/" + p.ID
	ref := csRegister(t, s, a, ws, "own", "claude")
	var b db.CoresearchBuild
	s.call(t, "POST", root+"/builds", a, ws, nil, &b)
	work := csWork(t, s, a, "own", ref)
	if len(work.Builds) != 1 {
		t.Fatal(work)
	}
	old := coresearch.MaxProjectBytes
	defer func() { coresearch.MaxProjectBytes = old }()
	coresearch.MaxProjectBytes = 10
	if c := finishBuildTest(t, s, a, ws, work.Builds[0], "ok"); c != 204 {
		t.Fatalf("source plus artifacts quota %d", c)
	}
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte("test pdf")))
	if rc, e := s.H.Blobs.Get(t.Context(), coresearch.BlobKey(uuid.MustParse(ws), sum)); e == nil {
		rc.Close()
		t.Fatal("unreferenced failed artifact retained")
	} else if !errors.Is(e, storage.ErrNotFound) {
		t.Fatal(e)
	}
	var status string
	s.Env.Owner.QueryRow(t.Context(), `SELECT status FROM coresearch_build WHERE id=$1`, b.ID).Scan(&status)
	if status != "failed" {
		t.Fatal("quota did not settle", status)
	}
	coresearch.MaxProjectBytes = 100
	if c := finishBuildTest(t, s, a, ws, work.Builds[0], "ok"); c != 204 {
		t.Fatal("repeat quota receipt", c)
	}
	s.call(t, "POST", root+"/builds", a, ws, nil, &b)
	work = csWork(t, s, a, "own", ref)
	if len(work.Builds) != 1 {
		t.Fatal("quota blocks successor", work)
	}
	if c := finishBuildTest(t, s, a, ws, work.Builds[0], "ok"); c != 204 {
		t.Fatal(c)
	}
	var usage int64
	if e := s.H.Store.InWorkspace(t.Context(), uuid.MustParse(ws), uuid.MustParse(s.userID(t, a)), func(tx *store.Tx) error {
		var e error
		usage, e = tx.Q.CoresearchProjectBytes(t.Context(), db.CoresearchProjectBytesParams{WorkspaceID: tx.WS, ProjectID: uuid.MustParse(p.ID), Sha256: ""})
		return e
	}); e != nil {
		t.Fatal(e)
	}
	// Source is eight UTF-8 bytes; artifacts are 8,8,12 bytes.
	if usage != 36 {
		t.Fatalf("exact artifact bytes=%d", usage)
	}
	coresearch.MaxProjectBytes = 36
	s.call(t, "POST", root+"/builds", a, ws, nil, &b)
	work = csWork(t, s, a, "own", ref)
	if len(work.Builds) != 1 {
		t.Fatal(work)
	}
	if c := finishBuildTest(t, s, a, ws, work.Builds[0], "ok"); c != 204 {
		t.Fatalf("shared output double charge %d", c)
	}
	if c, _ := csUpload(t, s, a, ws, p.ID, "extra.bin", 0, false, []byte{0, 1}); c != 413 {
		t.Fatalf("later source ignores artifact bytes %d", c)
	}
	if c, _ := csUpload(t, s, a, ws, p.ID, "same.bin", 0, false, []byte("test pdf")); c != 201 {
		t.Fatalf("shared artifact/source key charged twice %d", c)
	}
	url := root + "/docs/" + f
	s.call(t, "GET", url, a, ws, nil, nil)
	s.call(t, "POST", url+"/steps", a, ws, map[string]any{"base": 0, "client_id": "quota", "steps": []any{[]any{[]any{6, "new source"}}}}, nil)
	if c := s.call(t, "POST", root+"/builds", a, ws, nil, nil); c != 413 {
		t.Fatalf("live snapshot ignores artifact quota or wrong error %d", c)
	}

}

func TestBuildRunCLIRoutes(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	ref := csRegister(t, s, a, ws, "paper-build", "claude")
	var accepted struct {
		Conversation db.CoresearchConversation
		Run          db.AgentRun
	}
	if c := s.call(t, "POST", "/api/coresearch/conversations", a, ws, map[string]any{"body": "write", "mode": "cowrite", "request_id": uuid.NewString()}, &accepted); c != 201 {
		t.Fatal(c)
	}
	claimed := csClaim(t, s, a, ref)
	if len(claimed) != 1 {
		t.Fatal(claimed)
	}
	ref.Attempt = claimed[0].AgentRun.Attempt
	if e := s.H.AgentRuns.Start(t.Context(), uuid.MustParse(s.userID(t, a)), ref, claimed[0].AgentRun.ID); e != nil {
		t.Fatal(e)
	}
	var reply struct {
		ID  string `json:"build_id"`
		Seq int
	}
	if c := s.call(t, "POST", "/api/run/coresearch/paper/build", claimed[0].Token, "", nil, &reply); c != 201 || reply.ID == "" {
		t.Fatalf("queue %d %+v", c, reply)
	}
	buildWork := csWork(t, s, a, "paper-build", ref)
	if len(buildWork.Builds) != 1 {
		t.Fatal(buildWork)
	}
	var public map[string]json.RawMessage
	if c := s.call(t, "GET", "/api/run/coresearch/paper/build/"+reply.ID, claimed[0].Token, "", nil, &public); c != 200 {
		t.Fatal(c)
	}
	assertPublicBuild(t, public)
	var build db.CoresearchBuild
	if c := s.call(t, "GET", "/api/run/coresearch/paper/build/"+reply.ID, claimed[0].Token, "", nil, &build); c != 200 || build.Status != "running" {
		t.Fatalf("wait %d %+v", c, build)
	}
	if build.RequestedBy.String() != s.userID(t, a) {
		t.Fatal("build requester is not chat owner")
	}
	s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='cancelled' WHERE id=$1`, accepted.Run.ID)
	if c := s.call(t, "GET", "/api/run/coresearch/paper/build/"+reply.ID, claimed[0].Token, "", nil, nil); c != 401 && c != 404 {
		t.Fatal("inactive run token still reads", c)
	}
}

func assertPublicBuild(t *testing.T, value map[string]json.RawMessage) {
	t.Helper()
	for _, key := range []string{"claim_token", "claimed_daemon_id", "lease_expires_at", "receipt_sha", "daemon_id"} {
		if _, found := value[key]; found {
			t.Errorf("public response exposes %s", key)
		}
	}
}
func TestBuildPublicProjection(t *testing.T) {
	s, a, b, ws, p, _ := paperFixture(t)
	root := csProjects + "/" + p.ID
	ref := csRegister(t, s, a, ws, "own", "claude")
	var created map[string]json.RawMessage
	if c := s.call(t, "POST", root+"/builds", a, ws, nil, &created); c != 201 {
		t.Fatal(c)
	}
	if len(created) != 2 || created["id"] == nil || created["seq"] == nil {
		t.Errorf("POST must return only id/seq: %v", created)
	}
	work := csWork(t, s, a, "own", ref)
	if len(work.Builds) != 1 || work.Builds[0].ClaimToken == uuid.Nil {
		t.Fatal(work)
	}
	var rows []map[string]json.RawMessage
	if c := s.call(t, "GET", root+"/builds", b, ws, nil, &rows); c != 200 || len(rows) != 1 {
		t.Fatal(c, rows)
	}
	assertPublicBuild(t, rows[0])
}

// Run the real daemon receipt recovery/delivery loop against the real API. A
// quota rejection must be ACKed, leaving neither a running row nor a rejected
// durable receipt, and a queued successor must become eligible immediately.
func TestBuildQuotaDaemonReceipt(t *testing.T) {
	s, a, _, ws, p, _ := paperFixture(t)
	root := csProjects + "/" + p.ID
	ref := csRegister(t, s, a, ws, "own", "claude")
	s.call(t, "POST", root+"/builds", a, ws, nil, nil)
	work := csWork(t, s, a, "own", ref)
	if len(work.Builds) != 1 {
		t.Fatal(work)
	}
	b := work.Builds[0]
	var successor db.CoresearchBuild
	s.call(t, "POST", root+"/builds", a, ws, nil, &successor)
	state := t.TempDir()
	receiptDir := filepath.Join(state, "coresearch-receipts", b.ID.String()+"-"+b.ClaimToken.String())
	if e := os.MkdirAll(receiptDir, 0700); e != nil {
		t.Fatal(e)
	}
	outputs := []map[string]any{}
	for i, name := range []string{"pdf", "log", "synctex"} {
		data := []byte("test " + name)
		file := fmt.Sprintf("output-%08d", i)
		if e := os.WriteFile(filepath.Join(receiptDir, file), data, 0600); e != nil {
			t.Fatal(e)
		}
		outputs = append(outputs, map[string]any{"path": name, "file": file, "sha": fmt.Sprintf("%x", sha256.Sum256(data)), "size": len(data)})
	}
	receipt, _ := json.Marshal(map[string]any{"work": b.WorkContext, "id": b.ID, "kind": "builds", "body": map[string]any{"status": "ok", "pages": 1}, "outputs": outputs})
	if e := os.WriteFile(filepath.Join(receiptDir, "finish.json"), receipt, 0600); e != nil {
		t.Fatal(e)
	}
	identity, _ := json.Marshal(map[string]any{"daemon_id": "own", "server": s.URL})
	if e := os.WriteFile(filepath.Join(state, "daemon.json"), identity, 0600); e != nil {
		t.Fatal(e)
	}
	old := coresearch.MaxProjectBytes
	coresearch.MaxProjectBytes = 10
	defer func() { coresearch.MaxProjectBytes = old }()
	done := errors.New("receipt check complete")
	cfg := daemon.Config{Server: s.URL, Token: s.personalToken(t, a), StateDir: state, Home: t.TempDir(), Harnesses: map[string]string{"claude": "/usr/bin/true"}, Flush: 10 * time.Millisecond, Ready: func() error {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, e := os.Stat(receiptDir); errors.Is(e, os.ErrNotExist) {
				return done
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Error("quota receipt was retained/rejected instead of acknowledged")
		return done
	}}
	if e := daemon.Run(t.Context(), cfg); !errors.Is(e, done) {
		t.Fatal(e)
	}
	var status, reason string
	var refs, versions int
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT status,failure_reason,(pdf_blob IS NOT NULL)::int+(log_blob IS NOT NULL)::int+(synctex_blob IS NOT NULL)::int FROM coresearch_build WHERE id=$1`, b.ID).Scan(&status, &reason, &refs); e != nil {
		t.Fatal(e)
	}
	if status != "failed" || reason != "project_quota_exceeded" || refs != 0 {
		t.Fatalf("quota settlement %s %s refs=%d", status, reason, refs)
	}
	s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_version WHERE build_id=$1`, b.ID).Scan(&versions)
	if versions != 0 {
		t.Fatal("quota published version")
	}
	if c := finishBuildTest(t, s, a, ws, b, "ok"); c != 204 {
		t.Fatal("same receipt retry", c)
	}
	if c := finishBuildTest(t, s, a, ws, b, "failed"); c != 409 {
		t.Fatal("conflicting receipt", c)
	}
	next := csWork(t, s, a, "own", ref)
	if len(next.Builds) != 1 || next.Builds[0].ID != successor.ID {
		t.Fatal("quota blocks successor", next)
	}
}

type buildDeleteOnceStorage struct {
	storage.Storage
	key    string
	failed bool
}

func (s *buildDeleteOnceStorage) Delete(ctx context.Context, key string) error {
	if key == s.key && !s.failed {
		s.failed = true
		return errors.New("injected transient artifact delete failure")
	}
	return s.Storage.Delete(ctx, key)
}
func TestBuildQuotaRetriesArtifactCleanup(t *testing.T) {
	s, a, _, ws, p, _ := paperFixture(t)
	root := csProjects + "/" + p.ID
	ref := csRegister(t, s, a, ws, "own", "claude")
	// This candidate is shared with a retained source and must survive cleanup.
	if c, _ := csUpload(t, s, a, ws, p.ID, "retained.txt", 0, false, []byte("test log")); c != 201 {
		t.Fatal(c)
	}
	keyFor := func(data string) string {
		return coresearch.BlobKey(uuid.MustParse(ws), fmt.Sprintf("%x", sha256.Sum256([]byte(data))))
	}
	unrelated := keyFor("unrelated unreferenced bytes")
	if e := s.H.Blobs.Put(t.Context(), unrelated, bytes.NewBufferString("unrelated unreferenced bytes"), int64(len("unrelated unreferenced bytes"))); e != nil {
		t.Fatal(e)
	}
	s.call(t, "POST", root+"/builds", a, ws, nil, nil)
	work := csWork(t, s, a, "own", ref)
	if len(work.Builds) != 1 {
		t.Fatal(work)
	}
	old := coresearch.MaxProjectBytes
	defer func() { coresearch.MaxProjectBytes = old }()
	coresearch.MaxProjectBytes = 16
	broken := &buildDeleteOnceStorage{Storage: s.H.Blobs, key: keyFor("test pdf")}
	s.H.Blobs = broken
	if c := finishBuildTest(t, s, a, ws, work.Builds[0], "ok"); c != 500 {
		t.Fatalf("cleanup failure must be retryable: %d", c)
	}
	if !broken.failed {
		t.Fatal("delete failure not reached")
	}
	rc, e := s.H.Blobs.Get(t.Context(), broken.key)
	if e != nil {
		t.Fatal("failed deletion did not retain candidate", e)
	}
	rc.Close()
	if c := finishBuildTest(t, s, a, ws, work.Builds[0], "ok"); c != 204 {
		t.Fatalf("retry failed: %d", c)
	}
	for _, value := range []string{"test pdf", "test synctex"} {
		if rc, e := s.H.Blobs.Get(t.Context(), keyFor(value)); e == nil {
			rc.Close()
			t.Errorf("ACK abandoned orphan %q", value)
		} else if !errors.Is(e, storage.ErrNotFound) {
			t.Fatal(e)
		}
	}
	for _, key := range []string{keyFor("test log"), unrelated} {
		rc, e := s.H.Blobs.Get(t.Context(), key)
		if e != nil {
			t.Fatal("retained or unrelated blob removed", e)
		}
		rc.Close()
	}
}

func TestBuildRefusedWithoutOwnMachine(t *testing.T) {
	s, a, b, ws, p, _ := paperFixture(t)
	root := csProjects + "/" + p.ID
	csRegister(t, s, a, ws, "colleague", "claude")
	var refused map[string]any
	if c := s.call(t, "POST", root+"/builds", b, ws, nil, &refused); c != 409 || refused["code"] != "no_machine" {
		t.Fatalf("no own machine: %d %v", c, refused)
	}
	var n int
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_build WHERE project_id=$1`, p.ID).Scan(&n); e != nil || n != 0 {
		t.Fatal("refused build was queued", n, e)
	}
	csRegister(t, s, b, ws, "own", "claude")
	if c := s.call(t, "POST", root+"/builds", b, ws, nil, nil); c != 201 {
		t.Fatalf("own machine online: %d", c)
	}
}
