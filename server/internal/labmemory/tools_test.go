package labmemory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"lykeion/server/internal/storage"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/service"
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
	t.Helper()
	ctx := context.Background()
	st := &store.Store{Pool: f.env.App}
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
	f.exec(`INSERT INTO lesson(id,workspace_id,kind,text,cites,model) VALUES($1,$2,'lab','Stale lesson',$3,'test')`, stale, f.ws, `[{"ref":"record:`+f.record.String()+`","version":"superseded"}]`)
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
