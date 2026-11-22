package handler

import (
	"github.com/google/uuid"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/service"
	db "lykeion/server/pkg/db/generated"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAristotleAcceptanceUnavailableIsAtomic(t *testing.T) {
	s, a, b, _, ws, _ := csFixture(t)
	csRegister(t, s, b, ws, "other-owner", "claude")
	in := map[string]string{"body": "Ground a question", "request_id": uuid.NewString()}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, nil); code != 409 {
		t.Fatalf("unavailable must reject atomically: %d", code)
	}
	var count int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_conversation WHERE workspace_id=$1`, ws).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial conversations: %d %v", count, err)
	}
}

func TestAristotleAcceptedReceiptSnapshotAndPrivacy(t *testing.T) {
	s, a, b, _, ws, _ := csFixture(t)
	ref := csRegister(t, s, a, ws, "aristotle", "claude")
	var boot struct {
		Agent        db.Agent
		Conversation db.CoresearchConversation
	}
	if code := s.call(t, "POST", "/api/coresearch/bootstrap", a, ws, nil, &boot); code != 200 {
		t.Fatal(code)
	}
	if code := s.call(t, "PATCH", "/api/agents/"+boot.Agent.ID.String(), a, ws, map[string]string{"model": "frozen-model", "instructions": "Frozen instructions"}, nil); code != 200 {
		t.Fatal(code)
	}
	in := map[string]string{"body": "Investigate evidence", "request_id": uuid.NewString()}
	var out, again struct {
		Conversation db.CoresearchConversation
		Run          db.AgentRun
	}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, &out); code != 201 {
		t.Fatalf("accept: %d", code)
	}
	if out.Conversation.SnapshotModel == nil || *out.Conversation.SnapshotModel != "frozen-model" || out.Run.RequestedModel != "frozen-model" || !strings.Contains(out.Run.Prompt, "Frozen instructions") {
		t.Fatalf("snapshot not applied: %+v %+v", out.Conversation, out.Run)
	}
	for _, guidance := range []string{"lyk research-directory", "next_offset", "untrusted", "profile_url", "recommend them to lead", "contributing", "reviewing", "interests", "coverage", "assign"} {
		if !strings.Contains(out.Run.Prompt, guidance) {
			t.Errorf("missing directory guidance %q", guidance)
		}
	}
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE runtime SET models='[{"value":"frozen-model"}]'::jsonb WHERE id=$1`, ref.RuntimeID); e != nil {
		t.Fatal(e)
	}
	claims := csClaim(t, s, a, ref)
	if len(claims) != 1 || claims[0].AgentRun.RequestedModel != "frozen-model" || claims[0].Coresearch == nil || !claims[0].Coresearch.Aristotle {
		t.Fatalf("claim lost frozen config: %+v", claims)
	}
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE runtime SET status='offline' WHERE workspace_id=$1`, ws); e != nil {
		t.Fatal(e)
	}
	// The CLI and model stay fixed while the accepted run is live; instructions may still change.
	if code := s.call(t, "PATCH", "/api/agents/"+boot.Agent.ID.String(), a, ws, map[string]string{"model": "later-model", "harness": "codex"}, nil); code != 409 {
		t.Fatalf("CLI change during a live run: %d", code)
	}
	if code := s.call(t, "PATCH", "/api/agents/"+boot.Agent.ID.String(), a, ws, map[string]string{"instructions": "Later instructions"}, nil); code != 200 {
		t.Fatal(code)
	}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, &again); code != 201 || !reflect.DeepEqual(out, again) {
		t.Fatalf("offline replay: %d %+v", code, again)
	}
	changed := map[string]string{"body": "Different", "request_id": in["request_id"]}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, changed, nil); code != 409 {
		t.Fatalf("reuse %d", code)
	}
	send := "/api/coresearch/conversations/" + out.Conversation.ID.String() + "/turns"
	if code := s.call(t, "POST", send, a, ws, in, nil); code != 409 {
		t.Fatalf("cross-target reuse %d", code)
	}
	for _, tok := range []string{b, a} {
		lab := ws
		if tok == a {
			lab = s.workspace(t, a, "other").ID
		}
		if code := s.call(t, "POST", send, tok, lab, map[string]string{"body": "x", "request_id": uuid.NewString()}, nil); code != 404 {
			t.Fatalf("privacy %d", code)
		}
	}
	legacy := csProjects + "/" + out.Conversation.ProjectID.String() + "/chats/" + out.Conversation.ChatID.String()
	if code := s.call(t, "POST", legacy+"/turns", a, ws, map[string]string{"body": "bypass"}, nil); code != 403 {
		t.Fatalf("legacy send %d", code)
	}
	if code := s.call(t, "POST", csProjects+"/"+out.Conversation.ProjectID.String()+"/chats", a, ws, map[string]string{"cli": "codex"}, nil); code != 403 {
		t.Fatalf("legacy chat creation %d", code)
	}
	if code := s.call(t, "GET", legacy+"/messages", a, ws, nil, nil); code != 200 {
		t.Fatalf("read plumbing %d", code)
	}
	if code := s.call(t, "POST", legacy+"/turns/"+out.Run.ID.String()+"/cancel", a, ws, nil, nil); code != 204 {
		t.Fatalf("cancel plumbing %d", code)
	}
	if code := s.call(t, "GET", legacy+"/turns/"+out.Run.ID.String()+"/steps", a, ws, nil, nil); code != 200 {
		t.Fatalf("stream steps plumbing %d", code)
	}
	// Terminalize the accepted run so another turn can verify frozen config.
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='cancelled' WHERE id=$1`, out.Run.ID); e != nil {
		t.Fatal(e)
	}
	if code := s.call(t, "PATCH", "/api/agents/"+boot.Agent.ID.String(), a, ws, map[string]string{"model": "later-model", "harness": "codex"}, nil); code != 200 {
		t.Fatalf("CLI change after the run settled: %d", code)
	}
	csRegister(t, s, a, ws, "aristotle", "claude")
	for _, catalog := range []any{[]any{}, []map[string]string{{"value": "new-cli-model", "label": "New CLI model", "description": "New catalog"}}} {
		if code := s.call(t, "POST", "/api/daemon/heartbeat", s.personalToken(t, a), "", map[string]any{"runtimes": []service.RuntimeRef{ref}, "models": map[string]any{"claude": catalog}}, nil); code != 200 {
			t.Fatalf("model refresh %d", code)
		}
	}
	next := map[string]string{"body": "Continue", "request_id": uuid.NewString()}
	if code := s.call(t, "POST", send, a, ws, next, &again); code != 201 {
		t.Fatalf("frozen followup %d", code)
	}
	if again.Run.Harness != "claude" || again.Run.RequestedModel != "frozen-model" || !strings.Contains(again.Run.Prompt, "Frozen instructions") || strings.Contains(again.Run.Prompt, "Later instructions") {
		t.Fatalf("followup changed snapshot %+v", again.Run)
	}
	if code := s.call(t, "POST", send, a, ws, next, &out); code != 201 || !reflect.DeepEqual(out, again) {
		t.Fatalf("send replay %d", code)
	}
	// Future conversations require current codex runtime; failed first acceptance leaves no receipt.
	fresh := map[string]string{"body": "Future", "request_id": uuid.NewString()}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, fresh, nil); code != 409 {
		t.Fatalf("wrong harness accepted %d", code)
	}
	csRegister(t, s, a, ws, "codex", "codex")
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, fresh, &out); code != 201 || out.Run.Harness != "codex" || out.Run.RequestedModel != "later-model" {
		t.Fatalf("retry current settings %d %+v", code, out)
	}
}

func TestAristotleWelcomeAndConcurrentIdempotency(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	var boot struct {
		Agent        db.Agent
		Conversation db.CoresearchConversation
	}
	if code := s.call(t, "POST", "/api/coresearch/bootstrap", a, ws, nil, &boot); code != 200 {
		t.Fatal(code)
	}
	var count int
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_message WHERE chat_id=$1`, boot.Conversation.ChatID).Scan(&count); e != nil || count != 0 {
		t.Fatalf("fake welcome response %d %v", count, e)
	}
	send := "/api/coresearch/conversations/" + boot.Conversation.ID.String() + "/turns"
	in := map[string]string{"body": "Hello", "request_id": uuid.NewString()}
	if code := s.call(t, "POST", send, a, ws, in, nil); code != 409 {
		t.Fatalf("welcome offline %d", code)
	}
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_conversation WHERE id=$1 AND snapshot_harness IS NULL`, boot.Conversation.ID).Scan(&count); e != nil || count != 1 {
		t.Fatalf("snapshot before acceptance %d %v", count, e)
	}
	if code := s.call(t, "PATCH", "/api/agents/"+boot.Agent.ID.String(), a, ws, map[string]string{"harness": "codex", "instructions": "Welcome frozen"}, nil); code != 200 {
		t.Fatal(code)
	}
	csRegister(t, s, a, ws, "codex", "codex")
	var wg sync.WaitGroup
	ids := make(chan uuid.UUID, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out struct {
				Conversation db.CoresearchConversation
				Run          db.AgentRun
			}
			if code := s.call(t, "POST", send, a, ws, in, &out); code != 201 {
				t.Errorf("concurrent %d", code)
				return
			}
			if out.Conversation.SnapshotHarness == nil || *out.Conversation.SnapshotHarness != "codex" || out.Run.RequestedModel != "" || !strings.Contains(out.Run.Prompt, "Welcome frozen") {
				t.Errorf("welcome snapshot %+v", out)
			}
			ids <- out.Run.ID
		}()
	}
	wg.Wait()
	close(ids)
	var first uuid.UUID
	for id := range ids {
		if first == uuid.Nil {
			first = id
		}
		if first != id {
			t.Fatalf("duplicate runs %s %s", first, id)
		}
	}
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM agent_run WHERE coresearch_chat_id=$1`, boot.Conversation.ChatID).Scan(&count); e != nil || count != 1 {
		t.Fatalf("duplicate acceptance %d %v", count, e)
	}
}

func TestAristotleConcurrentCreateAndUntrustedConfig(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	csRegister(t, s, a, ws, "claude", "claude")
	for _, extra := range []string{"agent_id", "snapshot_model"} {
		if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, map[string]string{"body": "x", "request_id": uuid.NewString(), extra: "untrusted"}, nil); code != 400 {
			t.Fatalf("trusted client %s: %d", extra, code)
		}
	}
	in := map[string]string{"body": "Concurrent create", "request_id": uuid.NewString()}
	ids := make(chan uuid.UUID, 4)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out struct {
				Conversation db.CoresearchConversation
				Run          db.AgentRun
			}
			if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, &out); code != 201 {
				t.Errorf("concurrent create %d", code)
				return
			}
			ids <- out.Conversation.ID
		}()
	}
	wg.Wait()
	close(ids)
	var first uuid.UUID
	for id := range ids {
		if first == uuid.Nil {
			first = id
		}
		if first != id {
			t.Fatalf("duplicate conversation %s %s", first, id)
		}
	}
	var count int
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_conversation WHERE workspace_id=$1`, ws).Scan(&count); e != nil || count != 1 {
		t.Fatalf("duplicate creation %d %v", count, e)
	}
}

func TestAristotleRejectsStaleOnlineRuntime(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	ref := csRegister(t, s, a, ws, "stale", "claude")
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE runtime SET last_seen_at=now()-interval '65 seconds' WHERE id=$1`, ref.RuntimeID); e != nil {
		t.Fatal(e)
	}
	in := map[string]string{"body": "Research", "request_id": uuid.NewString()}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, nil); code != 409 {
		t.Fatalf("stale-online runtime accepted: %d", code)
	}
	var count int
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_conversation WHERE workspace_id=$1`, ws).Scan(&count); e != nil || count != 0 {
		t.Fatalf("partial stale acceptance %d %v", count, e)
	}
	csRegister(t, s, a, ws, "stale", "claude")
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, nil); code != 201 {
		t.Fatalf("fresh retry %d", code)
	}
}

func TestAristotleSelectedCLIIsValidatedAndFrozen(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	csRegister(t, s, a, ws, "selected-codex", "codex")
	input := map[string]string{"body": "Choose CLI", "request_id": uuid.NewString(), "harness": "codex", "model": ""}
	var out struct {
		Conversation db.CoresearchConversation
		Run          db.AgentRun
	}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, input, &out); code != 201 {
		t.Fatalf("selected CLI: %d", code)
	}
	if out.Conversation.SnapshotHarness == nil || *out.Conversation.SnapshotHarness != "codex" || out.Run.Harness != "codex" {
		t.Fatalf("wrong CLI snapshot: %+v %+v", out.Conversation, out.Run)
	}
	replay := map[string]string{"body": "Choose CLI", "request_id": input["request_id"], "harness": "claude", "model": ""}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, replay, nil); code != 409 {
		t.Fatalf("changed replay: %d", code)
	}
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='cancelled' WHERE id=$1`, out.Run.ID); e != nil {
		t.Fatal(e)
	}
	next := map[string]string{"body": "Continue", "request_id": uuid.NewString(), "harness": "claude"}
	if code := s.call(t, "POST", "/api/coresearch/conversations/"+out.Conversation.ID.String()+"/turns", a, ws, next, nil); code != 422 {
		t.Fatalf("changed frozen CLI: %d", code)
	}
	for _, cli := range []string{"invalid", "claude"} {
		bad := map[string]string{"body": "No", "request_id": uuid.NewString(), "harness": cli}
		code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, bad, nil)
		if (cli == "invalid" && code != 422) || (cli == "claude" && code != 409) {
			t.Fatalf("unavailable or invalid CLI %s: %d", cli, code)
		}
	}
}

func TestAristotleStarterBecomesFirstSession(t *testing.T) {
	for _, mode := range []string{"experiment", "cowrite"} {
		t.Run(mode, func(t *testing.T) {
			s, a, _, _, ws, _ := csFixture(t)
			csRegister(t, s, a, ws, "starter", "claude")
			var boot, after struct {
				Agent        db.Agent
				Conversation db.CoresearchConversation
			}
			if code := s.call(t, "POST", "/api/coresearch/bootstrap", a, ws, nil, &boot); code != 200 {
				t.Fatal(code)
			}
			in := map[string]string{"body": "First research question", "request_id": uuid.NewString(), "mode": mode}
			if mode == "cowrite" {
				in["template"] = "blank"
			}
			var out struct {
				Conversation db.CoresearchConversation
				Run          db.AgentRun
			}
			path := "/api/coresearch/conversations/" + boot.Conversation.ID.String() + "/turns"
			if code := s.call(t, "POST", path, a, ws, in, &out); code != 201 {
				t.Fatalf("accept starter: %d", code)
			}
			if out.Conversation.ID != boot.Conversation.ID || out.Conversation.ProjectID != boot.Conversation.ProjectID || out.Conversation.ChatID != boot.Conversation.ChatID || out.Conversation.IsWelcome || out.Conversation.Title != "First research question" {
				t.Fatalf("starter not converted: %+v", out.Conversation)
			}
			var replay struct {
				Conversation db.CoresearchConversation
				Run          db.AgentRun
			}
			if code := s.call(t, "POST", path, a, ws, in, &replay); code != 201 || replay.Run.ID != out.Run.ID || replay.Conversation.ID != out.Conversation.ID {
				t.Fatalf("starter replay: %d %+v", code, replay)
			}
			if code := s.call(t, "POST", "/api/coresearch/bootstrap", a, ws, nil, &after); code != 200 || after.Conversation.ID != out.Conversation.ID {
				t.Fatalf("bootstrap recreated starter: %d %+v", code, after.Conversation)
			}
			var projectMode, chatMode string
			if e := s.Env.Owner.QueryRow(t.Context(), `SELECT p.mode, c.mode FROM coresearch_project p JOIN coresearch_chat c ON c.project_id=p.id WHERE p.id=$1 AND c.id=$2`, out.Conversation.ProjectID, out.Conversation.ChatID).Scan(&projectMode, &chatMode); e != nil || projectMode != mode || chatMode != mode {
				t.Fatalf("mode mismatch: %s %s %v", projectMode, chatMode, e)
			}
			if mode == "cowrite" {
				var n int
				if e := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_file WHERE project_id=$1`, out.Conversation.ProjectID).Scan(&n); e != nil || n == 0 {
					t.Fatalf("missing paper files: %d %v", n, e)
				}
			}
		})
	}
}

func TestDeleteCoresearchConversationRemovesOwnedProject(t *testing.T) {
	s, a, b, _, ws, _ := csFixture(t)
	csRegister(t, s, a, ws, "delete-machine", "claude")
	var boot struct{ Conversation db.CoresearchConversation }
	if code := s.call(t, "POST", "/api/coresearch/bootstrap", a, ws, nil, &boot); code != 200 {
		t.Fatal(code)
	}
	var out struct {
		Conversation db.CoresearchConversation
		Run          db.AgentRun
	}
	path := "/api/coresearch/conversations/" + boot.Conversation.ID.String()
	if code := s.call(t, "POST", path+"/turns", a, ws, map[string]string{"body": "Question", "request_id": uuid.NewString()}, &out); code != 201 {
		t.Fatal(code)
	}
	if code := s.call(t, "DELETE", path, b, ws, nil, nil); code != 404 {
		t.Fatalf("nonowner delete: %d", code)
	}
	if code := s.call(t, "DELETE", path, a, ws, nil, nil); code != 409 {
		t.Fatalf("active delete: %d", code)
	}
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='cancelled' WHERE id=$1`, out.Run.ID); e != nil {
		t.Fatal(e)
	}
	if code := s.call(t, "DELETE", path, a, ws, nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	var n int
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_project WHERE id=$1`, out.Conversation.ProjectID).Scan(&n); e != nil || n != 0 {
		t.Fatalf("project retained: %d %v", n, e)
	}
	if code := s.call(t, "GET", "/api/coresearch/projects/"+out.Conversation.ProjectID.String(), a, ws, nil, nil); code != 404 {
		t.Fatalf("project accessible: %d", code)
	}
}

func TestDeleteCowriteProjectRemovesPaperRecords(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	csRegister(t, s, a, ws, "delete-paper", "claude")
	var out struct {
		Conversation db.CoresearchConversation
		Run          db.AgentRun
	}
	in := map[string]string{"body": "Paper", "mode": "cowrite", "template": "blank", "request_id": uuid.NewString()}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, &out); code != 201 {
		t.Fatalf("create: %d", code)
	}
	var files int
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_file WHERE project_id=$1`, out.Conversation.ProjectID).Scan(&files); e != nil || files == 0 {
		t.Fatalf("paper files: %d %v", files, e)
	}
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='cancelled' WHERE id=$1`, out.Run.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Env.Owner.Exec(t.Context(), `INSERT INTO coresearch_build(workspace_id,project_id,seq,requested_by,status,sources) VALUES($1,$2,1,$3,'queued','[]')`, ws, out.Conversation.ProjectID, out.Conversation.OwnerID); e != nil {
		t.Fatal(e)
	}
	if code := s.call(t, "DELETE", "/api/coresearch/conversations/"+out.Conversation.ID.String(), a, ws, nil, nil); code != 409 {
		t.Fatalf("active build deletion: %d", code)
	}
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE coresearch_build SET status='aborted' WHERE project_id=$1`, out.Conversation.ProjectID); e != nil {
		t.Fatal(e)
	}
	if code := s.call(t, "DELETE", "/api/coresearch/conversations/"+out.Conversation.ID.String(), a, ws, nil, nil); code != 204 {
		t.Fatalf("delete paper: %d", code)
	}
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_file WHERE project_id=$1`, out.Conversation.ProjectID).Scan(&files); e != nil || files != 0 {
		t.Fatalf("paper files retained: %d %v", files, e)
	}
}

func TestDeleteConversationCleansUnsharedBlobsAndPreservesSharedBlobs(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	csRegister(t, s, a, ws, "blob-delete", "claude")
	type accepted struct {
		Conversation db.CoresearchConversation
		Run          db.AgentRun
	}
	start := func(body string) accepted {
		var out accepted
		if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, map[string]string{"body": body, "request_id": uuid.NewString()}, &out); code != 201 {
			t.Fatalf("start: %d", code)
		}
		if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='cancelled' WHERE id=$1`, out.Run.ID); e != nil {
			t.Fatal(e)
		}
		return out
	}
	first, second := start("First"), start("Second")
	code, unique := csUpload(t, s, a, ws, first.Conversation.ProjectID.String(), "unique.txt", 0, false, []byte("unique blob"))
	if code != 201 {
		t.Fatalf("unique upload %d", code)
	}
	code, shared := csUpload(t, s, a, ws, first.Conversation.ProjectID.String(), "shared.txt", 0, false, []byte("shared blob"))
	if code != 201 {
		t.Fatalf("shared upload %d", code)
	}
	code, retained := csUpload(t, s, a, ws, second.Conversation.ProjectID.String(), "shared.txt", 0, false, []byte("shared blob"))
	if code != 201 || shared.BlobKey != retained.BlobKey {
		t.Fatalf("same blob: %d", code)
	}

	cellKey := ws + "/coresearch/cells/" + uuid.NewString() + ".json"
	generationCellID := uuid.New()
	generationKey := ws + "/coresearch/cells/" + generationCellID.String() + "/" + uuid.NewString() + ".json"
	artifactKey := coresearch.BlobKey(uuid.MustParse(ws), strings.Repeat("a", 64))
	for _, key := range []string{cellKey, generationKey, artifactKey} {
		if err := s.H.Blobs.Put(t.Context(), key, strings.NewReader("artifact"), int64(len("artifact"))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO coresearch_cell(workspace_id,chat_id,seq,kernel,source,status,outputs_blob) VALUES ($1,$2,1,'python3','','ok',$3)`, ws, first.Conversation.ChatID, cellKey); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO coresearch_cell(id,workspace_id,chat_id,seq,kernel,source,status,outputs_blob) VALUES ($1,$2,$3,2,'python3','','ok',$4)`, generationCellID, ws, first.Conversation.ChatID, generationKey); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO coresearch_build(workspace_id,project_id,seq,requested_by,status,sources,pdf_blob) VALUES($1,$2,1,$3,'aborted','[]',$4)`, ws, first.Conversation.ProjectID, first.Conversation.OwnerID, artifactKey); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "DELETE", "/api/coresearch/conversations/"+first.Conversation.ID.String(), a, ws, nil, nil); code != 204 {
		t.Fatalf("first delete: %d", code)
	}
	for _, key := range []string{cellKey, generationKey, artifactKey} {
		if rc, err := s.H.Blobs.Get(t.Context(), key); err == nil {
			rc.Close()
			t.Fatalf("artifact %s retained", key)
		}
	}
	if rc, e := s.H.Blobs.Get(t.Context(), unique.BlobKey); e == nil {
		rc.Close()
		t.Fatal("unreferenced blob retained")
	}
	if rc, e := s.H.Blobs.Get(t.Context(), shared.BlobKey); e != nil {
		t.Fatalf("shared blob removed: %v", e)
	} else {
		rc.Close()
	}
	if code := s.call(t, "DELETE", "/api/coresearch/conversations/"+second.Conversation.ID.String(), a, ws, nil, nil); code != 204 {
		t.Fatalf("second delete: %d", code)
	}
	if rc, e := s.H.Blobs.Get(t.Context(), shared.BlobKey); e == nil {
		rc.Close()
		t.Fatal("last shared blob retained")
	}
}

func TestDeleteConversationRetriesFailedBlobCleanup(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	csRegister(t, s, a, ws, "blob-retry", "claude")
	var out struct {
		Conversation db.CoresearchConversation
		Run          db.AgentRun
	}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, map[string]string{"body": "Retry cleanup", "request_id": uuid.NewString()}, &out); code != 201 {
		t.Fatal(code)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='cancelled' WHERE id=$1`, out.Run.ID); err != nil {
		t.Fatal(err)
	}
	code, version := csUpload(t, s, a, ws, out.Conversation.ProjectID.String(), "retry.txt", 0, false, []byte("retry bytes"))
	if code != 201 {
		t.Fatal(code)
	}
	original := s.H.Blobs
	s.H.Blobs = &buildDeleteOnceStorage{Storage: original, key: version.BlobKey}
	if code := s.call(t, "DELETE", "/api/coresearch/conversations/"+out.Conversation.ID.String(), a, ws, nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	var pending int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_blob_cleanup WHERE workspace_id=$1 AND blob_key=$2`, ws, version.BlobKey).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("queue: %d %v", pending, err)
	}
	if err := coresearch.SweepBlobCleanup(t.Context(), s.H.Store, original); err != nil {
		t.Fatal(err)
	}
	if rc, err := original.Get(t.Context(), version.BlobKey); err == nil {
		rc.Close()
		t.Fatal("blob retained after retry")
	}
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_blob_cleanup WHERE workspace_id=$1 AND blob_key=$2`, ws, version.BlobKey).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("queue after retry: %d %v", pending, err)
	}
}

func TestDeleteConversationWaitsForBlobWriter(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	csRegister(t, s, a, ws, "blob-lock", "claude")
	var out struct {
		Conversation db.CoresearchConversation
		Run          db.AgentRun
	}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, map[string]string{"body": "Lock cleanup", "request_id": uuid.NewString()}, &out); code != 201 {
		t.Fatal(code)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='cancelled' WHERE id=$1`, out.Run.ID); err != nil {
		t.Fatal(err)
	}
	code, v := csUpload(t, s, a, ws, out.Conversation.ProjectID.String(), "lock.txt", 0, false, []byte("locked blob"))
	if code != 201 {
		t.Fatal(code)
	}
	held, release, writerDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		writerDone <- coresearch.WithBlob(t.Context(), s.H.Store, s.H.Blobs, uuid.MustParse(ws), out.Conversation.OwnerID, v.Sha256, strings.NewReader("locked blob"), int64(len("locked blob")), func() error { close(held); <-release; return nil })
	}()
	<-held
	deleted := make(chan int, 1)
	go func() {
		deleted <- s.call(t, "DELETE", "/api/coresearch/conversations/"+out.Conversation.ID.String(), a, ws, nil, nil)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM coresearch_blob_cleanup WHERE workspace_id=$1 AND blob_key=$2`, ws, v.BlobKey).Scan(&count); err != nil {
			close(release)
			t.Fatal(err)
		}
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("deletion did not enqueue cleanup")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case code := <-deleted:
		close(release)
		t.Fatalf("cleanup did not wait for writer: %d", code)
	default:
	}
	if rc, err := s.H.Blobs.Get(t.Context(), v.BlobKey); err != nil {
		close(release)
		t.Fatal("blob removed while writer held lock")
	} else {
		rc.Close()
	}
	close(release)
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-deleted:
		if code != 204 {
			t.Fatal(code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deletion did not finish")
	}
	if rc, err := s.H.Blobs.Get(t.Context(), v.BlobKey); err == nil {
		rc.Close()
		t.Fatal("blob retained after writer released lock")
	}
}

func TestAristotleTurnCarriesSelectedCapabilities(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	ref := csRegister(t, s, a, ws, "selected", "claude")
	in := map[string]any{"body": "Analyze", "request_id": uuid.NewString(), "capabilities": map[string]any{"skills": []string{"aeon"}, "connectors": []string{"ensembl"}}}
	var out struct{ Run db.AgentRun }
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, &out); code != 201 {
		t.Fatalf("accept: %d", code)
	}
	if !strings.Contains(out.Run.Prompt, "aeon") || !strings.Contains(out.Run.Prompt, "ensembl") {
		t.Fatal("selected capabilities absent from run")
	}
	claims := csClaim(t, s, a, ref)
	if len(claims) != 1 || !strings.Contains(claims[0].AgentRun.Prompt, "aeon") || !strings.Contains(claims[0].AgentRun.Prompt, "ensembl") {
		t.Fatal("selected capabilities absent from claim")
	}
	in["capabilities"] = map[string]any{"skills": []string{"different"}}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, nil); code != 409 {
		t.Fatalf("changed retry: %d", code)
	}
}

func TestCoresearchClaimsOnlySelectedWorkspaceConnectors(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	var selected, other mcpView
	if code := s.call(t, "POST", "/api/mcp-servers", a, ws, mcpInput("selected", 0, map[string]any{"url": "https://selected.example/mcp"}), &selected); code != 201 {
		t.Fatal(code)
	}
	if code := s.call(t, "POST", "/api/mcp-servers", a, ws, mcpInput("other", 0, map[string]any{"url": "https://other.example/mcp"}), &other); code != 201 {
		t.Fatal(code)
	}
	ref := csRegister(t, s, a, ws, "custom-selected", "claude")
	in := map[string]any{"body": "Analyze", "request_id": uuid.NewString(), "capabilities": map[string]any{"workspace_connectors": []string{selected.ID}}}
	var accepted struct{ Conversation struct{ ID uuid.UUID } }
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, &accepted); code != 201 {
		t.Fatalf("accept: %d", code)
	}
	claims := csClaim(t, s, a, ref)
	if len(claims) != 1 || len(claims[0].WorkspaceConnectors) != 1 || claims[0].WorkspaceConnectors[0].ID.String() != selected.ID {
		t.Fatalf("unexpected selected connector claim in %d claims", len(claims))
	}
	if err := s.H.AgentRuns.Cancel(t.Context(), uuid.MustParse(ws), uuid.MustParse(s.userID(t, a)), claims[0].AgentRun.ID); err != nil {
		t.Fatal(err)
	}
	if code := s.call(t, "POST", "/api/coresearch/conversations/"+accepted.Conversation.ID.String()+"/turns", a, ws, map[string]any{"body": "Next message", "request_id": uuid.NewString()}, nil); code != 201 {
		t.Fatalf("next turn: %d", code)
	}
	claims = csClaim(t, s, a, ref)
	if len(claims) != 1 || len(claims[0].WorkspaceConnectors) != 0 || claims[0].CallToken != "" {
		t.Fatal("connector carried into next message")
	}
}

func TestCoresearchRequiresKeylessDeclarationForCommand(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	var command mcpView
	if code := s.call(t, "POST", "/api/mcp-servers", a, ws, mcpInput("local", 0, map[string]any{"command": "node", "args": []string{"server.js"}}), &command); code != 201 {
		t.Fatal(code)
	}
	ref := csRegister(t, s, a, ws, "keyless-command", "claude")
	in := map[string]any{"body": "Analyze", "request_id": uuid.NewString(), "capabilities": map[string]any{"workspace_connectors": []string{command.ID}}}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, nil); code != 422 {
		t.Fatalf("undeclared command: %d", code)
	}
	if code := s.call(t, "PUT", "/api/mcp-servers/"+command.ID, a, ws, mcpInput("local", 1, map[string]any{"command": "node", "args": []string{"server.js"}, "keyless": true}), nil); code != 200 {
		t.Fatalf("declare: %d", code)
	}
	if code := s.call(t, "POST", "/api/coresearch/conversations", a, ws, in, nil); code != 201 {
		t.Fatalf("declared command: %d", code)
	}
	claims := csClaim(t, s, a, ref)
	if len(claims) != 1 || len(claims[0].WorkspaceConnectors) != 1 || claims[0].CallToken == "" || len(claims[0].WorkspaceConnectors[0].Command) != 2 {
		t.Fatal("declared command not included in claim")
	}
}
