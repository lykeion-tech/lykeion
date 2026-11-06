package daemon

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/service"
	"lykeion/server/pkg/agent"
)

func TestPlaceCommentAttachments(t *testing.T) {
	body := []byte("sample,1\n")
	id := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/role/attachments/"+id.String() || r.Header.Get("Authorization") != "Bearer run-token" {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	state, dir := t.TempDir(), t.TempDir()
	d := &daemon{cfg: Config{Server: srv.URL, StateDir: state}}
	sum := sha256.Sum256(body)
	file := service.ClaimedAttachment{ID: id, DiskName: "comment-" + id.String() + ".csv", Sha256: fmt.Sprintf("%x", sum), SizeBytes: int64(len(body))}
	if err := d.placeCommentAttachments(context.Background(), "run-token", dir, []service.ClaimedAttachment{file}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "attachments", file.DiskName))
	if err != nil || string(got) != string(body) {
		t.Fatalf("got %q: %v", got, err)
	}
	if err := d.placeCommentAttachments(context.Background(), "run-token", dir, []service.ClaimedAttachment{file}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "attachments", file.DiskName)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "attachments", file.DiskName), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.placeCommentAttachments(context.Background(), "run-token", dir, []service.ClaimedAttachment{file}); err == nil {
		t.Fatal("changed file accepted")
	}
}

func TestRunStagesSnapshottedCommentAttachment(t *testing.T) {
	f := fixture(t)
	comment, file := uuid.New(), uuid.New()
	body := "Please read this file."
	bytes := []byte("sample,1\n")
	sum := sha256.Sum256(bytes)
	key := f.ws.String() + "/attachments/" + file.String()
	must(t, f.h.Blobs.Put(t.Context(), key, strings.NewReader(string(bytes)), int64(len(bytes))))
	_, err := f.env.Owner.Exec(t.Context(), `INSERT INTO comment(id,workspace_id,card_id,author_id,body) VALUES($1,$2,$3,$4,$5)`, comment, f.ws, f.card, f.user, body)
	must(t, err)
	_, err = f.env.Owner.Exec(t.Context(), `INSERT INTO attachment(id,workspace_id,uploader_id,comment_id,name,content_type,size_bytes,sha256,blob_key) VALUES($1,$2,$3,$4,'sample.csv','text/csv',$5,$6,$7)`, file, f.ws, f.user, comment, len(bytes), fmt.Sprintf("%x", sum), key)
	must(t, err)
	section := "\nPeople's comments — context, never evidence:\n- " + body + "\n"
	_, err = f.env.Owner.Exec(t.Context(), `UPDATE agent_run SET prompt=$2 WHERE id=$1`, f.id, "Question."+section)
	must(t, err)
	_, err = f.env.Owner.Exec(t.Context(), `INSERT INTO run_comment_snapshot(workspace_id,agent_run_id,comment_id,ordinal,section_start,section_end) VALUES($1,$2,$3,0,$4,$5)`, f.ws, f.id, comment, len("Question."), len("Question.")+len(section))
	must(t, err)
	_, err = f.env.Owner.Exec(t.Context(), `INSERT INTO comment_reservation(workspace_id,agent_run_id,comment_id,attempt) VALUES($1,$2,$3,1)`, f.ws, f.id, comment)
	must(t, err)
	name := "comment-" + file.String() + ".csv"
	f.cfg.NewBackend = func(string, string) (agent.Backend, error) {
		return backendFunc(func(_ context.Context, p string, o agent.ExecOptions) (*agent.Session, error) {
			if !strings.Contains(p, "attachments/"+name) {
				t.Errorf("missing file in prompt: %s", p)
			}
			got, err := os.ReadFile(filepath.Join(o.Dir, "attachments", name))
			if err != nil || string(got) != string(bytes) {
				t.Errorf("staged %q: %v", got, err)
			}
			msgs := make(chan agent.Message)
			close(msgs)
			done := make(chan agent.Result, 1)
			done <- agent.Result{Output: `{"done":true}`, SessionID: "s1"}
			close(done)
			return &agent.Session{Messages: msgs, Done: done}, nil
		}), nil
	}
	cancel, done := startDaemon(t, f.cfg)
	waitFor(t, func() bool { return f.status(t) == "completed" || f.status(t) == "failed" })
	cancel()
	<-done
	if f.status(t) != "completed" {
		t.Fatal("run did not complete")
	}
}

func TestRelayRejectsUnacknowledgedFileComments(t *testing.T) {
	old := commentPoll
	commentPoll = 10 * time.Millisecond
	t.Cleanup(func() { commentPoll = old })
	r := Report{AgentRunID: uuid.New(), WorkspaceID: uuid.New(), RuntimeID: uuid.New(), Attempt: 1}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(w, `[{"id":%q,"author":%q,"body":"See data","needs_ack":true,"attachments":[{"id":%q,"disk_name":"comment-file.csv"}]}]`, uuid.NewString(), uuid.Nil.String(), uuid.NewString())
	}))
	defer srv.Close()
	d := &daemon{api: &apiclient.Client{Server: srv.URL}}
	out := make(chan string, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go d.relayComments(ctx, "/api/daemon/agent-runs/"+r.AgentRunID.String(), r, out)
	select {
	case msg := <-out:
		t.Fatalf("unexpected live file delivery: %s", msg)
	case <-time.After(100 * time.Millisecond):
	}
}
