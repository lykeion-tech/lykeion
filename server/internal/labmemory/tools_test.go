package labmemory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
)

type toolBearer string

func (b toolBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

type searchStub struct {
	ws   uuid.UUID
	refs []Ref
	err  error
}

func (s searchStub) Search(_ context.Context, ws uuid.UUID, _ string, k int) ([]Ref, error) {
	if s.ws != ws || k < 1 || k > 20 {
		panic("wrong search scope")
	}
	return s.refs, s.err
}

type toolFixture struct {
	*fixture
	run     uuid.UUID
	session *mcp.ClientSession
	url     string
}

func newTools(t *testing.T, f *fixture, index Searcher, stores ...storage.Storage) *toolFixture {
	return newToolsWithPolicy(t, f, index, true, stores...)
}
func newToolsWithPolicy(t *testing.T, f *fixture, index Searcher, enabled bool, stores ...storage.Storage) *toolFixture {
	t.Helper()
	ctx := context.Background()
	st := &store.Store{LessonsEnabled: enabled, Pool: f.env.App}
	run, chat := uuid.New(), uuid.New()
	token := "ltk_" + run.String()
	f.exec(`INSERT INTO lab_chat(id,workspace_id,owner_id) VALUES($1,$2,$3)`, chat, f.ws, f.user)
	f.exec(`INSERT INTO agent_run(id,workspace_id,responsible_user_id,requester_id,lab_chat_id,kind,board_column,round,role,harness,prompt,status,attempt) VALUES($1,$2,$5,$3,$4,'aristotle','',0,'aristotle','claude','question','running',1)`, run, f.ws, f.user, chat, f.member())
	f.exec(`INSERT INTO agent_run_token(token_hash,workspace_id,agent_run_id,role,attempt,scopes,expires_at) VALUES($1,$2,$3,'aristotle',1,$4,now()+interval '1 hour')`, auth.Hash(token), f.ws, run, service.Scopes("aristotle"))
	var blobs storage.Storage
	if len(stores) > 0 {
		blobs = stores[0]
	}
	srv := httptest.NewServer(middleware.AgentRunAuth(st)(middleware.RequireScope(service.ScopeAristotle)((Tools{Store: st, Index: index, Blobs: blobs}).Handler())))
	t.Cleanup(srv.Close)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: &http.Client{Transport: toolBearer(token)}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return &toolFixture{f, run, cs, srv.URL}
}
func (f *toolFixture) call(name string, args map[string]any, wantErr bool) string {
	f.t.Helper()
	result, err := f.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil || result.IsError != wantErr {
		f.t.Fatalf("%s: %+v %v", name, result, err)
	}
	b, _ := json.Marshal(result.Content)
	return string(b)
}
func TestLabToolsReadAsTheAsker(t *testing.T) {
	f := setup(t)
	tf := newTools(t, f, nil)
	ref := Ref{"study", f.study}.String()
	out := tf.call("read", map[string]any{"ref": ref}, false)
	if !strings.Contains(out, "Question") {
		t.Fatal(out)
	}
	tf.call("links", map[string]any{"ref": ref}, false)
	var n int
	if err := f.env.Owner.QueryRow(context.Background(), `SELECT count(*) FROM lab_read WHERE agent_run_id=$1 AND ((ref=$2 AND via='read') OR (ref=$3 AND via=$2))`, tf.run, ref, Ref{"record", f.record}.String()).Scan(&n); err != nil || n != 2 {
		t.Fatalf("missing provenance: %d %v", n, err)
	}
}
func TestLabToolsRefuseAfterAskerLeaves(t *testing.T) {
	f := setup(t)
	tf := newTools(t, f, nil)
	f.exec("DELETE FROM member WHERE workspace_id=$1 AND user_id=$2", f.ws, f.user)
	for _, name := range []string{"read", "links", "search"} {
		args := map[string]any{"ref": "workspace:context"}
		if name == "search" {
			args = map[string]any{"query": "X"}
		}
		out := tf.call(name, args, true)
		if !strings.Contains(out, "the asker is no longer a member of this lab") {
			t.Fatal(out)
		}
	}
}
func TestSearchDropsWhatTheAskerCannotOpen(t *testing.T) {
	f := setup(t)
	ws, card, stale := uuid.New(), uuid.New(), uuid.New()
	f.exec("INSERT INTO workspace(id,slug,name) VALUES($1,'foreign','Foreign')", ws)
	f.exec("INSERT INTO card(id,workspace_id,number,title,question,owner_id,board_column) VALUES($1,$2,1,'Private','Hidden',$3,'grounding')", card, ws, f.user)
	f.exec(`INSERT INTO lesson(id,workspace_id,kind,text,cites,model,checked_at) VALUES($1,$2,'lab','Stale lesson',$3,'test',now())`, stale, f.ws, `[{"ref":"record:`+f.record.String()+`","version":"superseded"}]`)
	tf := newTools(t, f, searchStub{f.ws, []Ref{{"study", card}, {"study", f.study}, {"lesson", stale}}, nil})
	out := tf.call("search", map[string]any{"query": "X"}, false)
	if strings.Contains(out, card.String()) || strings.Contains(out, stale.String()) || !strings.Contains(out, f.study.String()) {
		t.Fatal(out)
	}
	tf.call("read", map[string]any{"ref": Ref{"lesson", stale}.String()}, true)
	var n int
	if err := f.env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM lab_read WHERE agent_run_id=$1 AND via='search'", tf.run).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
}
func TestSearchWithoutIndexSaysSo(t *testing.T) {
	for _, broken := range []bool{false, true} {
		f := setup(t)
		var index Searcher
		if broken {
			index = searchStub{ws: f.ws, err: errors.New("secret internal detail")}
		}
		tf := newTools(t, f, index)
		out := tf.call("search", map[string]any{"query": "X"}, true)
		if !strings.Contains(out, "search is unavailable; read and follow links instead") || strings.Contains(out, "secret") {
			t.Fatal(out)
		}
	}
}
func TestRoleRunTokenCannotUseLabTools(t *testing.T) {
	f := setup(t)
	tf := newTools(t, f, nil)
	run := uuid.New()
	token := "ltk_" + run.String()
	f.exec(`INSERT INTO agent_run(id,workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt,status,attempt) VALUES($1,$2,$3,$4,'grounding',1,'scout','claude','p','running',1)`, run, f.ws, f.study, f.user)
	f.exec(`INSERT INTO agent_run_token(token_hash,workspace_id,agent_run_id,card_id,role,attempt,scopes,expires_at) VALUES($1,$2,$3,$4,'scout',1,$5,now()+interval '1 hour')`, auth.Hash(token), f.ws, run, f.study, service.Scopes("role"))
	req, _ := http.NewRequest("POST", tf.url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal(res.StatusCode)
	}
}

func TestLabReadAttachmentPagesAndPrivateBoundaries(t *testing.T) {
	f := setup(t)
	blobs := storage.Local{Dir: t.TempDir()}
	tf := newTools(t, f, nil, blobs)
	f.team()
	ref := Ref{"study", f.study}.String()
	if s := tf.call("read", map[string]any{"ref": ref}, false); strings.Contains(s, "private-method") || !strings.Contains(s, "Private Team") {
		t.Fatal(s)
	}
	aid, rec := uuid.New(), uuid.New()
	key := f.ws.String() + "/attachments/" + aid.String()
	full := strings.Repeat("a", pageSize-1) + "λtail-proof"
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(full)))
	f.exec(`INSERT INTO attachment(id,workspace_id,uploader_id,card_id,name,content_type,size_bytes,sha256,blob_key,text_sha256,record_key) VALUES($1,$2,$3,$4,'evidence.txt','text/plain',20,$5,$6,$5,'c5')`, aid, f.ws, f.user, f.study, hash, key)
	f.exec(`INSERT INTO record(id,workspace_id,card_id,board_column,round,kind,role,key,body,blob_key) VALUES($1,$2,$3,'grounding',0,'call','server','c5','{"server":"attachments","answer":"prefix"}',$4)`, rec, f.ws, f.study, key+".text")
	f.exec(`INSERT INTO event(workspace_id,card_id,type,actor_type,payload) VALUES($1,$2,'blob.created','system',jsonb_build_object('blob_key',$3::text))`, f.ws, f.study, key+".text")
	if err := blobs.Put(context.Background(), key+".text", strings.NewReader(full), int64(len(full))); err != nil {
		t.Fatal(err)
	}
	ref = Ref{"record", rec}.String()
	first := tf.call("read", map[string]any{"ref": ref}, false)
	if !strings.Contains(first, "next_offset") || strings.Contains(first, "tail-proof") {
		t.Fatal("wrong first page")
	}
	last := tf.call("read", map[string]any{"ref": ref, "offset": pageSize - 1}, false)
	if !strings.Contains(last, "λtail-proof") {
		t.Fatal("missing last page", last)
	}
	// Removing the attachment also hides its recorded preview and its link.
	f.exec(`DELETE FROM attachment WHERE id=$1`, aid)
	tf.call("read", map[string]any{"ref": ref}, true)
	if s := tf.call("links", map[string]any{"ref": Ref{"study", f.study}.String()}, false); strings.Contains(s, rec.String()) {
		t.Fatal(s)
	}
}
func TestLabDirectoryWithoutIndexHasPagedLiveLinks(t *testing.T) {
	f := setup(t)
	tf := newTools(t, f, nil)
	before, e := f.read(Ref{Kind: "workspace"})
	if e != nil {
		t.Fatal(e)
	}
	f.exec(`INSERT INTO card(workspace_id,number,title,question,owner_id,board_column) SELECT $1,i,'Study '||i,'Question',$2,'grounding' FROM generate_series(2,21) i`, f.ws, f.user)
	after, e := f.read(Ref{Kind: "workspace"})
	if e != nil || before.Version != after.Version {
		t.Fatalf("directory became a citation dependency: %v", e)
	}
	args := map[string]any{"ref": "workspace:context"}
	seen := map[string]bool{}
	for page, n := range []int{20, 1} {
		res, e := tf.session.CallTool(context.Background(), &mcp.CallToolParams{Name: "links", Arguments: args})
		if e != nil || res.IsError {
			t.Fatalf("%+v %v", res, e)
		}
		b, _ := json.Marshal(res.StructuredContent)
		var hits hitList
		if err := json.Unmarshal(b, &hits); err != nil || len(hits.Results) != n {
			t.Fatalf("page %d: %s %v", page, b, err)
		}
		for _, hit := range hits.Results {
			if seen[hit.Ref] {
				t.Fatal("repeated study", hit.Ref)
			}
			seen[hit.Ref] = true
		}
		if page == 0 && hits.NextAfter == "" || page == 1 && hits.NextAfter != "" {
			t.Fatal("wrong cursor", hits.NextAfter)
		}
		args["after"] = hits.NextAfter
	}
}

type searchFunc func(context.Context, uuid.UUID, string, int) ([]Ref, error)

func (s searchFunc) Search(ctx context.Context, ws uuid.UUID, q string, k int) ([]Ref, error) {
	return s(ctx, ws, q, k)
}
func TestSearchRechecksMembershipAfterRetrieval(t *testing.T) {
	f := setup(t)
	tf := newTools(t, f, searchFunc(func(ctx context.Context, ws uuid.UUID, q string, k int) ([]Ref, error) {
		_, err := f.env.Owner.Exec(ctx, "DELETE FROM member WHERE workspace_id=$1 AND user_id=$2", ws, f.user)
		return []Ref{{"study", f.study}}, err
	}))
	out := tf.call("search", map[string]any{"query": "study"}, true)
	if !strings.Contains(out, errMember.Error()) {
		t.Fatal(out)
	}
	var n int
	if e := f.env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM lab_read WHERE agent_run_id=$1", tf.run).Scan(&n); n != 0 || e != nil {
		t.Fatalf("%d %v", n, e)
	}
}

func TestLabRejectsBorrowedChatOwner(t *testing.T) {
	f := setup(t)
	tf := newTools(t, f, nil)
	f.exec("UPDATE agent_run SET requester_id=$1 WHERE id=$2", f.member(), tf.run)
	tf.call("read", map[string]any{"ref": Ref{"study", f.study}.String()}, true)
}
func TestLabHidesHiddenServiceAndCoresearchAttachmentMetadata(t *testing.T) {
	f := setup(t)
	tf := newTools(t, f, nil)
	f.exec(`INSERT INTO attachment(workspace_id,uploader_id,card_id,name,content_type,size_bytes,sha256,blob_key,kind,service_name,service_start) VALUES($1,$2,$3,'private-service.zip','application/zip',2,'abc','hidden','service','evaluator','python service.py'),($1,$2,$3,'project-only.txt','text/plain',2,'abc',$4,'file',NULL,NULL)`, f.ws, f.user, f.study, f.ws.String()+"/coresearch-uploads/"+uuid.NewString())
	out := tf.call("read", map[string]any{"ref": Ref{"study", f.study}.String()}, false)
	if strings.Contains(out, "project-only.txt") || strings.Contains(out, "private-service.zip") {
		t.Fatal(out)
	}
}

func TestSearchRejectsRevokedTokenDuringIndexIO(t *testing.T) {
	f := setup(t)
	var run uuid.UUID
	tf := newTools(t, f, searchFunc(func(ctx context.Context, ws uuid.UUID, q string, k int) ([]Ref, error) {
		_, err := f.env.Owner.Exec(ctx, "UPDATE agent_run_token SET scopes=ARRAY['call:record'] WHERE agent_run_id=$1", run)
		return []Ref{{"study", f.study}}, err
	}))
	run = tf.run
	if s := tf.call("search", map[string]any{"query": "test"}, true); !strings.Contains(s, middleware.ErrRoleUnauthorized.Error()) {
		t.Fatal(s)
	}
	var n int
	if e := f.env.Owner.QueryRow(context.Background(), "SELECT count(*) FROM lab_read WHERE agent_run_id=$1", run).Scan(&n); e != nil || n != 0 {
		t.Fatalf("%d %v", n, e)
	}
}

func TestLabOutputBytesRequireMemberDownloadVisibility(t *testing.T) {
	f := setup(t)
	blobs := storage.Local{Dir: t.TempDir()}
	tf := newTools(t, f, nil, blobs)
	for i, kind := range []string{"output", "output", "rerun_output"} {
		id := uuid.New()
		key := f.ws.String() + "/outputs/" + uuid.NewString()
		payload := fmt.Sprintf("file-bytes-%d", i)
		if err := blobs.Put(t.Context(), key, strings.NewReader(payload), int64(len(payload))); err != nil {
			t.Fatal(err)
		}
		f.exec(`INSERT INTO record(id,workspace_id,card_id,board_column,round,kind,role,key,body,blob_key) VALUES($1,$2,$3,'experimentation',1,$4,'runner',$5,'{"name":"output-metadata.txt"}',$6)`, id, f.ws, f.study, kind, fmt.Sprintf("o%d", i+1), key)
		f.exec(`INSERT INTO event(workspace_id,card_id,type,actor_type,payload) VALUES($1,$2,'blob.created','system',jsonb_build_object('blob_key',$3::text))`, f.ws, f.study, key)
		if i == 1 {
			f.exec("UPDATE record SET superseded_at=now() WHERE id=$1", id)
		}
		out := tf.call("read", map[string]any{"ref": Ref{"record", id}.String()}, false)
		if !strings.Contains(out, "output-metadata.txt") || strings.Contains(out, payload) != (i == 0) {
			t.Fatalf("output case %d returned %s", i, out)
		}
	}
}
func TestLabLessonDirectoryAndSearchRequireSuccessfulCheck(t *testing.T) {
	f := setup(t)
	unchecked := f.lessonFor(Ref{"record", f.record})
	f.exec("UPDATE lesson SET checked_at=NULL WHERE id=$1", unchecked.ID)
	stale := f.lessonFor(Ref{"comment", f.comment})
	f.exec("INSERT INTO comment_cancellation(workspace_id,comment_id,author_id) VALUES($1,$2,$3)", f.ws, f.comment, f.user)
	checked := f.lessonFor(Ref{"record", f.record})
	// Pin fixture navigation order without using either lesson reference.
	f.exec("UPDATE lesson SET directory_key=$1 WHERE id=$2", "10000000-0000-4000-8000-000000000000", stale.ID)
	f.exec("UPDATE lesson SET directory_key=$1 WHERE id=$2", "f0000000-0000-4000-8000-000000000000", checked.ID)
	tf := newTools(t, f, searchStub{f.ws, []Ref{unchecked, stale, checked}, nil})
	tf.call("read", map[string]any{"ref": unchecked.String()}, true)
	tf.call("links", map[string]any{"ref": unchecked.String()}, true)
	out := tf.call("search", map[string]any{"query": "lesson"}, false)
	if !strings.Contains(out, checked.String()) || strings.Contains(out, unchecked.String()) || strings.Contains(out, stale.String()) {
		t.Fatal(out)
	}
	// First candidate is stale. Its id must not escape through either the
	// results or cursor, and its absence must not end directory traversal.
	args := map[string]any{"ref": "workspace:context", "directory": "lessons", "limit": 1}
	first := tf.page(args)
	if len(first.Results) != 0 || first.NextAfter == "" || strings.Contains(first.NextAfter, stale.ID.String()) {
		t.Fatal(first)
	}
	f.exec("DELETE FROM lesson WHERE id=$1", stale.ID)
	args["after"] = first.NextAfter
	second := tf.page(args)
	if len(second.Results) != 1 || second.Results[0].Ref != checked.String() || second.NextAfter != "" {
		t.Fatal(second)
	}
	var count int
	if err := f.env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM lab_read WHERE agent_run_id=$1 AND ref=ANY($2)", tf.run, []string{stale.String(), unchecked.String()}).Scan(&count); err != nil || count != 0 {
		t.Fatalf("hidden lesson logged: %d %v", count, err)
	}
}
func (f *toolFixture) page(args map[string]any) hitList {
	f.t.Helper()
	res, e := f.session.CallTool(f.t.Context(), &mcp.CallToolParams{Name: "links", Arguments: args})
	if e != nil || res.IsError {
		f.t.Fatalf("%+v %v", res, e)
	}
	data, e := json.Marshal(res.StructuredContent)
	if e != nil {
		f.t.Fatal(e)
	}
	var page hitList
	if e = json.Unmarshal(data, &page); e != nil {
		f.t.Fatal(e)
	}
	return page
}
func TestLabLargeStudyLogsOnlyCurrentPage(t *testing.T) {
	f := setup(t)
	tf := newTools(t, f, nil)
	f.exec(`INSERT INTO comment(workspace_id,card_id,author_id,body) SELECT $1,$2,$3,'Comment '||i FROM generate_series(1,1500) i`, f.ws, f.study, f.user)
	src := Ref{"study", f.study}.String()
	args := map[string]any{"ref": src}
	first := tf.page(args)
	if len(first.Results) != 20 || first.NextAfter == "" {
		t.Fatal(first)
	}
	tf.call("links", map[string]any{"ref": src, "limit": 21}, true)
	var count int
	if err := f.env.Owner.QueryRow(t.Context(), "SELECT count(*) FROM lab_read WHERE agent_run_id=$1 AND via=$2", tf.run, src).Scan(&count); err != nil || count != 20 {
		t.Fatalf("logged %d links: %v", count, err)
	}
	// An execution worker can take its workspace lock between pages. Hold it,
	// start the next tool call, and release it to let traversal continue.
	tx, err := f.env.Owner.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	var pid int
	if err = tx.QueryRow(t.Context(), "SELECT pg_backend_pid() FROM workspace WHERE id=$1 FOR NO KEY UPDATE NOWAIT", f.ws).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	args["after"] = first.NextAfter
	args["limit"] = 3
	done := make(chan error, 1)
	go func() {
		r, e := tf.session.CallTool(t.Context(), &mcp.CallToolParams{Name: "links", Arguments: args})
		if e == nil && r.IsError {
			e = fmt.Errorf("links: %v", r.Content)
		}
		done <- e
	}()
	waitCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		var blocked bool
		err = f.env.Owner.QueryRow(waitCtx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid)))", pid).Scan(&blocked)
		if err != nil {
			t.Fatalf("second page did not reach the execution lock: %v", err)
		}
		if blocked {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = f.env.Owner.QueryRow(t.Context(), "SELECT count(DISTINCT ref) FROM lab_read WHERE agent_run_id=$1 AND via=$2", tf.run, src).Scan(&count); err != nil || count != 23 {
		t.Fatalf("second page overlaps: %d %v", count, err)
	}
}

func TestLabLessonCursorIndependentOfOtherWorkspace(t *testing.T) {
	f := setup(t)
	f.lessonFor(Ref{Kind: "workspace"})
	f.lessonFor(Ref{Kind: "workspace"})
	tf := newTools(t, f, nil)
	args := map[string]any{"ref": "workspace:context", "directory": "lessons", "limit": 1}
	first := tf.page(args)
	key, err := uuid.Parse(first.NextAfter)
	if err != nil || key.Version() != 4 || len(first.Results) != 1 || first.Results[0].Ref == "lesson:"+key.String() {
		t.Fatalf("expected an independent random cursor: %+v", first)
	}
	other := &fixture{t: t, env: f.env, ws: uuid.New(), user: f.member()}
	other.exec("INSERT INTO workspace(id,slug,name) VALUES($1,'unrelated','Unrelated')", other.ws)
	other.exec("INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner')", other.ws, other.user)
	otherLesson := other.lessonFor(Ref{Kind: "workspace"})
	other.exec(`INSERT INTO lesson(workspace_id,kind,text,cites,model,checked_at) SELECT l.workspace_id,l.kind,l.text,l.cites,l.model,now() FROM lesson l CROSS JOIN generate_series(1,80) WHERE l.id=$1`, otherLesson.ID)
	unchanged := tf.page(args)
	if len(unchanged.Results) != 1 || unchanged.Results[0] != first.Results[0] || unchanged.NextAfter != first.NextAfter {
		t.Fatalf("foreign insertions changed page: %+v", unchanged)
	}
	other.exec("UPDATE lesson SET directory_key=$1 WHERE id=$2", "ffffffff-ffff-ffff-ffff-ffffffffffff", otherLesson.ID)
	outside := newTools(t, other, nil)
	foreignPage := outside.page(args)
	foreignKey, err := uuid.Parse(foreignPage.NextAfter)
	if err != nil || foreignKey.Version() != 4 || foreignKey == key {
		t.Fatalf("unexpected foreign cursor %+v", foreignPage)
	}
	args["after"] = first.NextAfter
	page := outside.page(args)
	if len(page.Results) != 1 {
		t.Fatal("expected a visible local hit after the foreign cursor", page)
	}
	for _, hit := range page.Results {
		var local bool
		err := f.env.Owner.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM lesson WHERE workspace_id=$1 AND id=$2)", other.ws, strings.TrimPrefix(hit.Ref, "lesson:")).Scan(&local)
		if err != nil || !local {
			t.Fatalf("replayed foreign cursor returned a nonlocal lesson: %+v %v", hit, err)
		}
	}
}

func TestRestartFactsOnlyTools(t *testing.T) {
	f := setup(t)
	lesson := f.lessonFor(Ref{"record", f.record})
	fact := Ref{"record", f.record}
	for _, enabled := range []bool{true, false, true} {
		tf := newToolsWithPolicy(t, f, searchStub{f.ws, []Ref{lesson, fact}, nil}, enabled)
		tf.call("read", map[string]any{"ref": lesson.String()}, !enabled)
		tf.call("links", map[string]any{"ref": lesson.String()}, !enabled)
		out := tf.call("search", map[string]any{"query": "Lesson"}, false)
		if strings.Contains(out, lesson.String()) != enabled || !strings.Contains(out, fact.String()) {
			t.Fatal(out)
		}
		page := tf.page(map[string]any{"ref": "workspace:context", "directory": "lessons", "limit": 1})
		if (len(page.Results) > 0) != enabled || page.NextAfter != "" {
			t.Fatal(page)
		}
		tf.call("read", map[string]any{"ref": fact.String()}, false)
	}
}
