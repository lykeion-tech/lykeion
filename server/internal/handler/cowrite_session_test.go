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
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"lykeion/server/internal/storage"
	db "lykeion/server/pkg/db/generated"
)

func TestCowriteSessionStarts(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	ref := csRegister(t, s, a, ws, "cowrite", "claude")
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE runtime SET models='[{"value":"test-model","label":"Test"}]' WHERE id=$1`, ref.RuntimeID); err != nil {
		t.Fatal(err)
	}
	in := map[string]any{"body": "Write a paper", "request_id": uuid.NewString(), "mode": "cowrite", "template": "neurips", "model": "test-model"}
	var out, again struct {
		Conversation db.CoresearchConversation
		Run          db.AgentRun
		ProjectID    uuid.UUID `json:"project_id"`
		ChatID       uuid.UUID `json:"chat_id"`
	}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, &out); code != 201 {
		t.Fatalf("cowrite acceptance: %d", code)
	}
	var mode string
	var private *uuid.UUID
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT mode,conversation_owner_id FROM coresearch_project WHERE id=$1`, out.ProjectID).Scan(&mode, &private); err != nil || mode != "cowrite" || private != nil {
		t.Fatalf("shared project: %s %v %v", mode, private, err)
	}
	var count int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_file f JOIN coresearch_file_version v ON v.file_id=f.id WHERE f.project_id=$1 AND f.path IN ('main.tex','refs.bib') AND v.n=1`, out.ProjectID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("scaffold: %d %v", count, err)
	}
	if out.Conversation.SnapshotModel == nil || *out.Conversation.SnapshotModel != "test-model" || out.Run.RequestedModel != "test-model" {
		t.Fatalf("model: %+v", out)
	}
	claims := csClaim(t, s, a, ref)
	if len(claims) != 1 || claims[0].Coresearch == nil || !claims[0].Coresearch.Aristotle || claims[0].AgentRun.RequestedModel != "test-model" {
		t.Fatalf("claim: %+v", claims)
	}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, &again); code != 201 || !reflect.DeepEqual(out, again) {
		t.Fatalf("replay %d", code)
	}
	in["model"] = "changed"
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, nil); code != 409 {
		t.Fatalf("changed receipt %d", code)
	}
	in["request_id"] = uuid.NewString()
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, nil); code != 422 {
		t.Fatalf("unknown model %d", code)
	}
	in["mode"] = "experiment"
	in["model"] = ""
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, nil); code != 422 {
		t.Fatalf("experiment template %d", code)
	}
}

func TestSharedSessionMembers(t *testing.T) {
	s, a, b, _, ws, _ := csFixture(t)
	csRegister(t, s, a, ws, "cowrite", "claude")
	var out struct {
		Conversation db.CoresearchConversation
		ProjectID    uuid.UUID `json:"project_id"`
	}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, map[string]any{"body": "Shared manuscript", "request_id": uuid.NewString(), "mode": "cowrite"}, &out); code != 201 {
		t.Fatal(code)
	}
	bid := s.userID(t, b)
	if code := s.call(t, "POST", csProjects+"/"+out.ProjectID.String()+"/members", a, ws, map[string]string{"user_id": bid, "role": "member"}, nil); code != 201 {
		t.Fatalf("member %d", code)
	}
	var rows []map[string]any
	if code := s.call(t, "GET", "/api/coresearch/conversations", b, ws, nil, &rows); code != 200 || len(rows) != 1 || rows[0]["shared"] != true || rows[0]["mode"] != "cowrite" {
		t.Fatalf("shared list %d %+v", code, rows)
	}
	if code := s.call(t, "GET", csProjects+"/"+out.ProjectID.String(), b, ws, nil, nil); code != 200 {
		t.Fatal(code)
	}
	if code := s.call(t, "POST", csProjects+"/"+out.ProjectID.String()+"/chats/"+out.Conversation.ChatID.String()+"/turns", b, ws, map[string]string{"body": "x", "request_id": uuid.NewString()}, nil); code != 403 {
		t.Fatalf("member turn %d", code)
	}
	var added db.CoresearchChat
	if code := s.call(t, "POST", csProjects+"/"+out.ProjectID.String()+"/chats", b, ws, map[string]string{"cli": "claude"}, &added); code != 201 {
		t.Fatalf("shared chat %d", code)
	}
	if added.Mode != "cowrite" {
		t.Fatalf("chat mode %s", added.Mode)
	}
	ref := csRegister(t, s, b, ws, "member-cowrite", "claude")
	var boot struct{ Agent db.Agent }
	if code := s.call(t, "POST", "/api/coresearch/bootstrap", b, ws, nil, &boot); code != 200 {
		t.Fatal(code)
	}
	if code := s.call(t, "PATCH", "/api/agents/"+boot.Agent.ID.String(), b, ws, map[string]string{"model": "member-model", "instructions": "Member instructions"}, nil); code != 200 {
		t.Fatal(code)
	}
	var accepted struct {
		Conversation db.CoresearchConversation
		Run          db.AgentRun
	}
	path := csProjects + "/" + out.ProjectID.String() + "/chats/" + added.ID.String() + "/turns"
	if code := s.call(t, "POST", path, b, ws, map[string]string{"body": "My manuscript chat", "request_id": uuid.NewString()}, &accepted); code != 201 || accepted.Run.RequestedModel != "member-model" || !strings.Contains(accepted.Run.Prompt, "Member instructions") {
		t.Fatalf("member Aristotle turn %d %+v", code, accepted)
	}
	claims := csClaim(t, s, b, ref)
	if len(claims) != 1 || claims[0].Coresearch == nil || !claims[0].Coresearch.Aristotle || claims[0].AgentRun.RequestedModel != "member-model" {
		t.Fatalf("new chat claim %+v", claims)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='cancelled' WHERE id=$1`, accepted.Run.ID); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "PATCH", "/api/agents/"+boot.Agent.ID.String(), b, ws, map[string]string{"model": "later-model", "instructions": "Later instructions"}, nil); code != 200 {
		t.Fatal(code)
	}
	if code := s.call(t, "POST", path, b, ws, map[string]string{"body": "Continue writing", "request_id": uuid.NewString()}, &accepted); code != 201 || accepted.Run.RequestedModel != "member-model" || !strings.Contains(accepted.Run.Prompt, "Member instructions") || strings.Contains(accepted.Run.Prompt, "Later instructions") {
		t.Fatalf("member frozen snapshot %d %+v", code, accepted)
	}

}

func TestCowriteTemplatesEndpoint(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	var rows []map[string]string
	if code := s.call(t, "GET", "/api/coresearch/templates", a, ws, nil, &rows); code != 200 || len(rows) != 8 {
		t.Fatalf("templates %d %+v", code, rows)
	}
	if !strings.EqualFold(rows[0]["label"], "Blank") {
		t.Fatal(rows)
	}
}

func TestAttachmentPathRules(t *testing.T) {
	s, a, b, _, ws, _ := csFixture(t)
	csRegister(t, s, a, ws, "cowrite", "claude")
	s.H.Blobs = coresearchUnlockedStorage{Storage: s.H.Blobs, check: func() {
		if _, err := s.Env.Owner.Exec(t.Context(), `SELECT id FROM workspace WHERE id=$1 FOR UPDATE NOWAIT`, ws); err != nil {
			t.Errorf("blob I/O under workspace mutation lock: %v", err)
		}
	}}
	upload := func(name string, data []byte) string {
		t.Helper()
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		f, _ := mw.CreateFormFile("file", name)
		_, _ = f.Write(data)
		_ = mw.Close()
		req, _ := http.NewRequest("POST", s.URL+"/api/coresearch/uploads", &body)
		req.Header.Set("Authorization", "Bearer "+a)
		req.Header.Set("X-Workspace-ID", ws)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			UploadID string `json:"upload_id"`
		}
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 201 {
			t.Fatalf("upload %d %s", resp.StatusCode, raw)
		}
		if err = json.Unmarshal(raw, &out); err != nil || out.UploadID == "" {
			t.Fatalf("upload result %s %v", raw, err)
		}
		return out.UploadID
	}
	png := []byte{137, 80, 78, 71, 13, 10, 26, 10, 0, 1}
	attachment := func(id, name string) []map[string]string { return []map[string]string{{"upload_id": id, "name": name}} }
	firstID := upload("plot.png", png)
	in := map[string]any{"body": "Write paper", "request_id": uuid.NewString(), "mode": "cowrite", "attachments": attachment(firstID, "plot.png")}
	var out struct {
		Conversation db.CoresearchConversation
		Run          db.AgentRun
		ProjectID    uuid.UUID `json:"project_id"`
	}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, &out); code != 201 {
		t.Fatalf("first attachment %d", code)
	}
	if !strings.Contains(out.Run.Prompt, "figures/plot.png") {
		t.Fatalf("attachment missing in prompt %s", out.Run.Prompt)
	}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, nil); code != 201 {
		t.Fatalf("attachment receipt %d", code)
	}
	terminal := func() {
		t.Helper()
		if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='cancelled' WHERE coresearch_chat_id=$1`, out.Conversation.ChatID); err != nil {
			t.Fatal(err)
		}
	}
	terminal()
	send := "/api/coresearch/conversations/" + out.Conversation.ID.String() + "/turns"
	in = map[string]any{"body": "Update plot", "request_id": uuid.NewString(), "attachments": attachment(upload("plot.png", append(png, 2)), "plot.png")}
	if code := s.call(t, "POST", send, a, ws, in, &out); code != 201 {
		t.Fatalf("later attachment %d", code)
	}
	terminal()
	var n int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT max(v.n) FROM coresearch_file f JOIN coresearch_file_version v ON v.file_id=f.id WHERE f.project_id=$1 AND f.path='figures/plot.png'`, out.Conversation.ProjectID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("same name version %d %v", n, err)
	}
	if code, _ := csUpload(t, s, a, ws, out.Conversation.ProjectID.String(), "figures/notes.tex", 0, false, []byte(strings.Repeat("a", 8191)+"λ")); code != 201 {
		t.Fatal(code)
	}
	in = map[string]any{"body": "Replace text", "request_id": uuid.NewString(), "attachments": attachment(upload("notes.tex", png), "notes.tex")}
	var refusal map[string]any
	if code := s.call(t, "POST", send, a, ws, in, &refusal); code != 409 || !strings.Contains(fmt.Sprint(refusal), "figures/notes.tex") {
		t.Fatalf("text collision %d %+v", code, refusal)
	}
	var queued int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM agent_run WHERE coresearch_chat_id=$1 AND status='queued'`, out.Conversation.ChatID).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("collision enqueued work: %d %v", queued, err)
	}
	stolen := upload("private.png", png)
	if code := s.call(t, "POST", "/api/coresearch/conversations", b, ws, map[string]any{"body": "stolen", "mode": "cowrite", "request_id": uuid.NewString(), "attachments": attachment(stolen, "private.png")}, nil); code != 422 {
		t.Fatalf("another uploader %d", code)
	}
	expired := upload("old.png", png)
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE attachment SET created_at=now()-interval '25 hours' WHERE id=$1`, expired); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", send, a, ws, map[string]any{"body": "expired", "request_id": uuid.NewString(), "attachments": attachment(expired, "old.png")}, nil); code != 422 {
		t.Fatalf("expired upload %d", code)
	}

	expiring := upload("expiring.png", png)
	s.H.Blobs = coresearchGetHookStorage{Storage: s.H.Blobs, beforeGet: func(key string) {
		if strings.HasSuffix(key, "/coresearch-uploads/"+expiring) {
			if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE attachment SET created_at=now()-interval '25 hours' WHERE id=$1`, expiring); err != nil {
				t.Error(err)
			}
		}
	}}
	if code := s.call(t, "POST", send, a, ws, map[string]any{"body": "expires while staging", "request_id": uuid.NewString(), "attachments": attachment(expiring, "expiring.png")}, nil); code != 409 {
		t.Fatalf("upload expired during staging %d", code)
	}
	for _, name := range []string{"../x", ".claude/x"} {
		in["attachments"] = attachment(upload("file", png), name)
		in["request_id"] = uuid.NewString()
		if code := s.call(t, "POST", send, a, ws, in, nil); code != 422 {
			t.Fatalf("unsafe %s %d", name, code)
		}
	}
	in = map[string]any{"body": "Experiment file", "request_id": uuid.NewString(), "attachments": attachment(upload("plot.png", png), "plot.png")}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, &out); code != 201 || !strings.Contains(out.Run.Prompt, "attachments/plot.png") {
		t.Fatalf("experiment attachment %d %+v", code, out)
	}
}

func TestCowriteReceiptPayloadIdentity(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	csRegister(t, s, a, ws, "cowrite", "claude")
	id := uuid.NewString()
	in := map[string]any{"body": "x", "mode": "cowrite", "request_id": id}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, nil); code != 201 {
		t.Fatal(code)
	}
	canonical := `{"Body":"x","Mode":"cowrite","Template":"","Model":null,"Attachments":null}`
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, map[string]string{"body": canonical, "request_id": id}, nil); code != 409 {
		t.Fatalf("different structured payload replayed %d", code)
	}
	for key, value := range map[string]any{"mode": "experiment", "template": "neurips", "model": "different", "attachments": []map[string]string{{"upload_id": uuid.NewString(), "name": "changed.png"}}} {
		next := map[string]any{"body": "x", "mode": "cowrite", "request_id": id}
		next[key] = value
		if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, next, nil); code != 409 {
			t.Fatalf("changed %s replay %d", key, code)
		}
	}
}

type coresearchUnlockedStorage struct {
	storage.Storage
	check func()
}

func (s coresearchUnlockedStorage) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	s.check()
	return s.Storage.Put(ctx, key, r, size)
}
func (s coresearchUnlockedStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	s.check()
	return s.Storage.Get(ctx, key)
}
func (s coresearchUnlockedStorage) Delete(ctx context.Context, key string) error {
	s.check()
	return s.Storage.Delete(ctx, key)
}

func TestCowriteUploadBounds(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	for _, tc := range []struct {
		size int64
		text bool
		want int
	}{{1 << 20, true, 201}, {1<<20 + 1, true, 413}, {50 << 20, false, 201}, {50<<20 + 1, false, 413}} {
		pr, pw := io.Pipe()
		mw := multipart.NewWriter(pw)
		go func() {
			part, err := mw.CreateFormFile("file", "upload")
			if err == nil {
				var reader io.Reader = &patterned{left: tc.size}
				if !tc.text {
					reader = io.LimitReader(zeroReader{}, tc.size)
				}
				_, err = io.Copy(part, reader)
			}
			if err == nil {
				err = mw.Close()
			}
			_ = pw.CloseWithError(err)
		}()
		req, _ := http.NewRequest("POST", s.URL+"/api/coresearch/uploads", pr)
		req.Header.Set("Authorization", "Bearer "+a)
		req.Header.Set("X-Workspace-ID", ws)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		pr.Close()
		if resp.StatusCode != tc.want {
			t.Fatalf("size %d text %v: %d %s", tc.size, tc.text, resp.StatusCode, raw)
		}
	}
}

type coresearchGetHookStorage struct {
	storage.Storage
	beforeGet func(string)
}

func (s coresearchGetHookStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	s.beforeGet(key)
	return s.Storage.Get(ctx, key)
}

func TestCowriteOverlappingAttachmentReceipt(t *testing.T) {
	for _, boundary := range []string{"pending_metadata", "deleted_blob"} {
		for _, outcome := range []string{"identical", "different_attachment_name", "no_receipt"} {
			t.Run(boundary+"/"+outcome, func(t *testing.T) {
				s, a, _, _, ws, _ := csFixture(t)
				csRegister(t, s, a, ws, "overlapping-cowrite", "claude")
				png := []byte{137, 80, 78, 71, 13, 10, 26, 10, 0, 1}
				upload := uuid.New()
				key := ws + "/coresearch-uploads/" + upload.String()
				sum := fmt.Sprintf("%x", sha256.Sum256(png))
				if err := s.H.Blobs.Put(t.Context(), key, bytes.NewReader(png), int64(len(png))); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO attachment(id,workspace_id,uploader_id,name,content_type,size_bytes,sha256,blob_key) VALUES($1,$2,$3,'plot.png','image/png',$4,$5,$6)`, upload, ws, s.userID(t, a), len(png), sum, key); err != nil {
					t.Fatal(err)
				}
				gate := &coresearchOverlapGate{entered: make(chan struct{}), release: make(chan struct{})}
				if boundary == "pending_metadata" {
					original := s.H.Store.Pool
					cfg := original.Config()
					cfg.ConnConfig.Tracer = &coresearchReceiptBarrier{gate: gate}
					pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
					if err != nil {
						t.Fatal(err)
					}
					s.H.Store.Pool = pool
					defer func() { s.H.Store.Pool = original; pool.Close() }()
				} else {
					s.H.Blobs = coresearchDeletedUploadBarrier{Storage: s.H.Blobs, key: key, gate: gate}
				}
				resumed := false
				defer func() {
					if !resumed {
						close(gate.release)
					}
				}()
				request := uuid.NewString()
				payload := func(name string) map[string]any {
					return map[string]any{"body": "Write this paper", "mode": "cowrite", "request_id": request, "attachments": []map[string]string{{"upload_id": upload.String(), "name": name}}}
				}
				delayedName := "plot.png"
				if outcome == "different_attachment_name" {
					delayedName = "different.png"
				}
				type reply struct {
					code int
					body map[string]any
				}
				delayed := make(chan reply, 1)
				go func() {
					var body map[string]any
					code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, payload(delayedName), &body)
					delayed <- reply{code, body}
				}()
				select {
				case <-gate.entered:
				case <-time.After(5 * time.Second):
					t.Fatal("overlapping request did not reach staging boundary")
				}
				var accepted map[string]any
				if outcome == "no_receipt" {
					if _, err := s.Env.Owner.Exec(t.Context(), `DELETE FROM attachment WHERE id=$1`, upload); err != nil {
						t.Fatal(err)
					}
					if err := s.H.Blobs.Delete(t.Context(), key); err != nil {
						t.Fatal(err)
					}
				} else if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, payload("plot.png"), &accepted); code != 201 {
					t.Fatalf("winning acceptance %d %+v", code, accepted)
				}
				close(gate.release)
				resumed = true
				var got reply
				select {
				case got = <-delayed:
				case <-time.After(5 * time.Second):
					t.Fatal("delayed request did not finish")
				}
				if boundary == "deleted_blob" && !gate.missingBlob.Load() {
					t.Fatal("overlap did not reach the consumed upload storage error")
				}
				want := 201
				if outcome == "different_attachment_name" {
					want = 409
				} else if outcome == "no_receipt" {
					want = 422
					if boundary == "deleted_blob" {
						want = 500
					}
				}
				if got.code != want {
					t.Errorf("delayed acceptance %d want %d: %+v", got.code, want, got.body)
				}
				if outcome == "identical" && !reflect.DeepEqual(got.body, accepted) {
					t.Errorf("overlap did not replay exact accepted receipt: got %+v want %+v", got.body, accepted)
				}
				var runs, versions int
				if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM agent_run WHERE workspace_id=$1`, ws).Scan(&runs); err != nil {
					t.Fatal(err)
				}
				if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_file_version v JOIN coresearch_file f ON f.id=v.file_id AND f.workspace_id=v.workspace_id WHERE f.workspace_id=$1 AND f.path='figures/plot.png'`, ws).Scan(&versions); err != nil {
					t.Fatal(err)
				}
				count := 1
				if outcome == "no_receipt" {
					count = 0
				}
				if runs != count || versions != count {
					t.Fatalf("overlap published duplicate work: runs %d versions %d want %d", runs, versions, count)
				}
			})
		}
	}
}

type coresearchOverlapGate struct {
	entered     chan struct{}
	release     chan struct{}
	paused      atomic.Bool
	missingBlob atomic.Bool
}

func (g *coresearchOverlapGate) wait(ctx context.Context) bool {
	if !g.paused.CompareAndSwap(false, true) {
		return false
	}
	close(g.entered)
	select {
	case <-g.release:
	case <-ctx.Done():
	}
	return true
}

type coresearchReceiptBarrier struct{ gate *coresearchOverlapGate }
type coresearchCommitTraceKey struct{}

func (t *coresearchReceiptBarrier) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, coresearchCommitTraceKey{}, strings.EqualFold(strings.TrimSpace(data.SQL), "commit"))
}
func (t *coresearchReceiptBarrier) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if commit, _ := ctx.Value(coresearchCommitTraceKey{}).(bool); commit && data.Err == nil {
		t.gate.wait(ctx)
	}
}

type coresearchDeletedUploadBarrier struct {
	storage.Storage
	key  string
	gate *coresearchOverlapGate
}

func (s coresearchDeletedUploadBarrier) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	paused := false
	if key == s.key {
		paused = s.gate.wait(ctx)
	}
	rc, err := s.Storage.Get(ctx, key)
	if paused && errors.Is(err, storage.ErrNotFound) {
		s.gate.missingBlob.Store(true)
	}
	return rc, err
}
