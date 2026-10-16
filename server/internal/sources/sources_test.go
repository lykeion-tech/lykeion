package sources

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/records"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

func fakeAPIs(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	work := `{"id":"https://openalex.org/W2","title":"No effect","doi":"https://doi.org/10.1/b","publication_year":2020,"is_retracted":true,"abstract_inverted_index":{"no":[0],"effect":[1]}}`
	mux.HandleFunc("/oa/works", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"results":[` + work + `]}`)) })
	mux.HandleFunc("/oa/works/W2", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(work)) })
	mux.HandleFunc("/epmc/search", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"resultList":{"result":[{"id":"1","source":"MED","pmcid":"PMC1","doi":"10.1/a","title":"X raises Y","abstractText":"abs","pubYear":"2021","pubTypeList":{"pubType":["research-article"]}}]}}`))
	})
	mux.HandleFunc("/epmc/PMC1/fullTextXML", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<article><body><p>X raised Y by 40% in mice.</p></body></article>`))
	})
	mux.HandleFunc("/geo/esearch.fcgi", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"esearchresult":{"idlist":["200001"]}}`))
	})
	mux.HandleFunc("/geo/esummary.fcgi", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":{"uids":["200001"],"200001":{"accession":"GSE1","title":"Mouse liver","summary":"s","gpl":"1234","n_samples":2,"samples":[{"accession":"GSM1","title":"mouse 1"},{"accession":"GSM2","title":"mouse 2"}]}}}`))
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

type bearer string

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

// TestHostedToolsRecordOnTheCard drives /mcp/sources the way an agent does:
// over streamable HTTP with a run token.
func TestHostedToolsRecordOnTheCard(t *testing.T) {
	env := testutil.Setup(t)
	api := fakeAPIs(t)
	st := &store.Store{Pool: env.App}
	blobs := storage.Local{Dir: t.TempDir()}
	h := &Hosted{OA: OpenAlex{Base: api.URL + "/oa"}, EPMC: EuropePMC{Base: api.URL + "/epmc"}, GEO: GEO{Base: api.URL + "/geo"}, Store: st, Blobs: blobs}

	// a running agent run for a card, with its token
	ctx := context.Background()
	u, err := st.Global().UpsertUserByEmail(ctx, "a@lab.org")
	if err != nil {
		t.Fatal(err)
	}
	ws, card, run := uuid.New(), uuid.New(), uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace (id, slug, name) VALUES ($1, 'w', 'w')`, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO card (id, workspace_id, number, title, question, owner_id, board_column, round) VALUES ($1, $2, 1, 't', 'q', $3, 'grounding', 1)`, card, ws, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO agent_run (id, workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, attempt) VALUES ($1, $2, $3, $4, 'grounding', 1, 'literature-scout', 'claude', 'p', 'running', 1)`, run, ws, card, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO agent_run_token (token_hash, workspace_id, agent_run_id, card_id, role, attempt, expires_at) VALUES ($1, $2, $3, $4, 'literature-scout', 1, now() + interval '1 hour')`,
		hashToken("ltk_test"), ws, run, card); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/mcp/sources", middleware.AgentRunAuth(st)(h.Handler()))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx,
		&mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp/sources", HTTPClient: &http.Client{Transport: bearer("ltk_test")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	for _, c := range []struct {
		name string
		args map[string]any
	}{
		{"search_datasets", map[string]any{"query": "mouse"}},
		{"search_literature", map[string]any{"database": "openalex", "query": "no effect"}},
		{"search_literature", map[string]any{"database": "europepmc", "query": "x y"}},
		{"fetch_text", map[string]any{"source_id": "europepmc:PMC1"}},
		{"fetch_text", map[string]any{"source_id": "openalex:W2"}},
		{"open_dataset", map[string]any{"accession": "GSE1"}},
	} {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: c.name, Arguments: c.args})
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Fatalf("%s: %v %+v", c.name, err, res)
		}
	}

	var v *records.View
	if err := st.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		var err error
		v, err = records.Load(ctx, tx, blobs, card, "grounding")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	{
		f, ok := v.ByKey("fetch", "europepmc:PMC1")
		txt, err := v.Text(f)
		if err != nil {
			t.Fatal(err)
		}
		if !ok || f.Role != "literature-scout" || !strings.Contains(txt, "X raised Y by 40%") || !records.Body[struct {
			FullText bool `json:"full_text"`
		}](f).FullText {
			t.Errorf("fetch not recorded: %+v %q", f, txt)
		}
		if w2, _ := v.ByKey("fetch", "openalex:W2"); !records.Body[struct{ Retracted bool }](w2).Retracted {
			t.Error("retraction not recorded")
		}
		if s, _ := v.Latest("search"); records.Body[struct{ Database, Query string }](s).Database != "europepmc" {
			t.Errorf("search not recorded: %+v", s)
		}
		d, ok := v.ByKey("open_dataset", "GSE1")
		if !ok || d.BlobKey == "" {
			t.Fatal("open_dataset not recorded")
		}
		structure, err := v.Text(d)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(structure, "platform: GPL1234") || !strings.Contains(structure, "- GSM2: mouse 2") {
			t.Errorf("dataset structure: %q", structure)
		}
	}
	if err := st.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		f, _ := v.ByKey("fetch", "europepmc:PMC1")
		// The role blob route (lyk blob) serves only blobs with a blob.created event.
		for _, key := range []string{f.BlobKey, func() string { d, _ := v.ByKey("open_dataset", "GSE1"); return d.BlobKey }()} {
			if ok, err := tx.Q.RoleBlobExists(ctx, db.RoleBlobExistsParams{WorkspaceID: ws, CardID: &card, BlobKey: key}); err != nil || !ok {
				t.Errorf("blob %s is not readable through the role blob route: %v", key, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := mcp.NewClient(&mcp.Implementation{Name: "x", Version: "0"}, nil).Connect(ctx,
		&mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp/sources", HTTPClient: &http.Client{Transport: bearer("lyk_person")}}, nil); err == nil {
		t.Fatal("a person's token reached the sources server")
	}

	// The server keeps no MCP session: it never hands out a session id, and it
	// offers no standalone stream, so any server node can answer any call.
	raw := func(method, body string) *http.Response {
		req, _ := http.NewRequest(method, srv.URL+"/mcp/sources", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer ltk_test")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	if res := raw("POST", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`); res.StatusCode != 200 || res.Header.Get("Mcp-Session-Id") != "" {
		t.Errorf("initialize: %d, session %q", res.StatusCode, res.Header.Get("Mcp-Session-Id"))
	}
	if res := raw("GET", ""); res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp/sources: %d, want 405", res.StatusCode)
	}

	// A run that settled after its request was authenticated records nothing.
	if _, err := env.Owner.Exec(ctx, `UPDATE agent_run SET status = 'completed' WHERE id = $1`, run); err != nil {
		t.Fatal(err)
	}
	role := middleware.Role{AgentRunID: run, WorkspaceID: ws, CardID: card, Role: "literature-scout", Column: "grounding", Round: 1}
	if err := h.Record(ctx, role, "ltk_test", "search", "", map[string]any{"database": "openalex", "query": "late"}, ""); !errors.Is(err, middleware.ErrRoleUnauthorized) {
		t.Fatalf("a settled run recorded a search: %v", err)
	}
}

func hashToken(s string) string { return auth.Hash(s) }

func TestInvalidGEOSummaryDoesNotRecordSuccessfulSearch(t *testing.T) {
	for _, tc := range []struct{ name, response string }{
		{"null summary", `{"result":{"200001":null}}`},
		{"empty summary", `{"result":{"200001":{}}}`},
		{"missing summary", `{"result":{"uids":["200001"]}}`},
		{"malformed summary", `{"result":{"uids":["200001"],"200001":{"accession":"GSE1","n_samples":"bad"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.Setup(t)
			st := &store.Store{Pool: env.App}
			ctx := context.Background()
			u, err := st.Global().UpsertUserByEmail(ctx, "invalid-geo@lab.org")
			if err != nil {
				t.Fatal(err)
			}
			ws, card, run := uuid.New(), uuid.New(), uuid.New()
			for _, q := range []struct {
				sql  string
				args []any
			}{
				{`INSERT INTO workspace (id,slug,name) VALUES ($1,'w','w')`, []any{ws}},
				{`INSERT INTO card (id,workspace_id,number,title,question,owner_id,board_column,round) VALUES ($1,$2,1,'t','q',$3,'grounding',1)`, []any{card, ws, u.ID}},
				{`INSERT INTO agent_run (id,workspace_id,card_id,responsible_user_id,board_column,round,role,harness,prompt,status,attempt) VALUES ($1,$2,$3,$4,'grounding',1,'literature-scout','claude','p','running',1)`, []any{run, ws, card, u.ID}},
				{`INSERT INTO agent_run_token (token_hash,workspace_id,agent_run_id,card_id,role,attempt,expires_at) VALUES ($1,$2,$3,$4,'literature-scout',1,now()+interval '1 hour')`, []any{hashToken("ltk_invalid_geo"), ws, run, card}},
			} {
				if _, err := env.Owner.Exec(ctx, q.sql, q.args...); err != nil {
					t.Fatal(err)
				}
			}
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/esearch.fcgi":
					w.Write([]byte(`{"esearchresult":{"idlist":["200001"]}}`))
				case "/esummary.fcgi":
					w.Write([]byte(tc.response))
				default:
					http.NotFound(w, r)
				}
			}))
			defer api.Close()
			h := &Hosted{GEO: GEO{Base: api.URL}, Store: st, Blobs: storage.Local{Dir: t.TempDir()}}
			srv := httptest.NewServer(middleware.AgentRunAuth(st)(h.Handler()))
			defer srv.Close()
			cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: &http.Client{Transport: bearer("ltk_invalid_geo")}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "search_datasets", Arguments: map[string]any{"query": "mouse"}})
			if err != nil {
				t.Fatal(err)
			}
			if !res.IsError {
				t.Errorf("invalid GEO summary returned successful tool result: %+v", res)
			}
			if err := st.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
				view, err := records.Load(ctx, tx, h.Blobs, card, "grounding")
				if err != nil {
					return err
				}
				if searches := view.Kind("search"); len(searches) != 0 {
					t.Errorf("invalid GEO response wrote %d successful search records", len(searches))
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestACardlessAgentRunIsLoggedButNeverRecorded(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	st := &store.Store{Pool: env.App}
	blobs := storage.Local{Dir: t.TempDir()}
	h := &Hosted{Store: st, Blobs: blobs}
	ws, u := uuid.New(), uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace (id, slug, name) VALUES ($1, 'w', 'w')`, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO "user" (id, email) VALUES ($1, 'u@x')`, u); err != nil {
		t.Fatal(err)
	}
	var ar uuid.UUID
	if err := env.Owner.QueryRow(ctx, `INSERT INTO agent_run (workspace_id, kind, requester_id, responsible_user_id, board_column, round, role, harness, prompt, status, attempt)
		VALUES ($1, 'autoresearch', $2, $2, '', 0, 'autoresearch', 'claude', 'p', 'running', 1) RETURNING id`, ws, u).Scan(&ar); err != nil {
		t.Fatal(err)
	}
	token := "ltk_" + ar.String()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO agent_run_token (token_hash, workspace_id, agent_run_id, card_id, role, attempt, scopes, expires_at)
		VALUES ($1, $2, $3, NULL, 'autoresearch', 1, '{report:write,card:create}', now() + interval '1 hour')`, auth.Hash(token), ws, ar); err != nil {
		t.Fatal(err)
	}
	role := middleware.Role{AgentRunID: ar, WorkspaceID: ws, Role: "autoresearch"}
	if err := h.Record(ctx, role, token, "fetch", "europepmc:PMC1", map[string]any{"source_id": "europepmc:PMC1"}, "the full text"); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct{ invalidate, restore string }{
		{`UPDATE agent_run SET attempt=2 WHERE id=$1`, `UPDATE agent_run SET attempt=1 WHERE id=$1`},
		{`UPDATE agent_run_token SET expires_at=now()-interval '1 minute' WHERE agent_run_id=$1`, `UPDATE agent_run_token SET expires_at=now()+interval '1 hour' WHERE agent_run_id=$1`},
	} {
		if _, err := env.Owner.Exec(ctx, change.invalidate, ar); err != nil {
			t.Fatal(err)
		}
		if err := h.Record(ctx, role, token, "fetch", "europepmc:PMC1", map[string]any{"source_id": "europepmc:PMC1"}, "the full text"); !errors.Is(err, middleware.ErrRoleUnauthorized) {
			t.Fatalf("stale sources: %v", err)
		}
		if _, err := env.Owner.Exec(ctx, change.restore, ar); err != nil {
			t.Fatal(err)
		}
	}
	wrong := role
	wrong.Role = "scoper"
	if err := h.Record(ctx, wrong, token, "fetch", "europepmc:PMC1", map[string]any{}, "text"); !errors.Is(err, middleware.ErrRoleUnauthorized) {
		t.Fatalf("wrong role: %v", err)
	}
	if err := h.Record(ctx, role, "ltk_wrong", "fetch", "europepmc:PMC1", map[string]any{}, "text"); !errors.Is(err, middleware.ErrRoleUnauthorized) {
		t.Fatalf("wrong token: %v", err)
	}
	// a run that settled while its call was in flight logs nothing
	if _, err := env.Owner.Exec(ctx, `UPDATE agent_run SET status = 'completed' WHERE id = $1`, ar); err != nil {
		t.Fatal(err)
	}
	if err := h.Record(ctx, role, token, "fetch", "europepmc:PMC1", map[string]any{"source_id": "europepmc:PMC1"}, "the full text"); !errors.Is(err, middleware.ErrRoleUnauthorized) {
		t.Fatal("a finished agent run's call was logged")
	}
	var records, events int
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM record`).Scan(&records); err != nil {
		t.Fatal(err)
	}
	if err := env.Owner.QueryRow(ctx, `SELECT count(*) FROM event WHERE type = 'sources.called' AND payload->>'key' = 'europepmc:PMC1'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(blobs.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if records != 0 || events != 1 || len(entries) != 0 {
		t.Fatalf("records %d, events %d, blobs %d", records, events, len(entries))
	}
}

// hostedMetadataFixture exercises the authenticated, stateless MCP route.
func hostedMetadataFixture(t *testing.T, metadata http.HandlerFunc, cardless bool) (*Hosted, *mcp.ClientSession, *testutil.Env, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	env := testutil.Setup(t)
	ctx := context.Background()
	st := &store.Store{Pool: env.App}
	ws, card, run, user := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := env.Owner.Exec(ctx, `INSERT INTO workspace (id,slug,name) VALUES ($1,'w','w');`, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO "user" (id,email) VALUES ($1,'metadata@lab.org')`, user); err != nil {
		t.Fatal(err)
	}
	var cardArg any = card
	role := "literature-scout"
	if cardless {
		card = uuid.Nil
		cardArg = nil
		role = "autoresearch"
	} else {
		if _, err := env.Owner.Exec(ctx, `INSERT INTO card (id,workspace_id,number,title,question,owner_id,board_column,round) VALUES ($1,$2,1,'t','q',$3,'grounding',1)`, card, ws, user); err != nil {
			t.Fatal(err)
		}
	}
	kind := "role"
	if cardless {
		kind = "autoresearch"
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO agent_run (id,workspace_id,card_id,responsible_user_id,requester_id,kind,board_column,round,role,harness,prompt,status,attempt) VALUES ($1,$2,$3,$4,$4,$5,'grounding',1,$6,'claude','p','running',1)`, run, ws, cardArg, user, kind, role); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Owner.Exec(ctx, `INSERT INTO agent_run_token (token_hash,workspace_id,agent_run_id,card_id,role,attempt,expires_at) VALUES ($1,$2,$3,$4,$5,1,now()+interval '1 hour')`, hashToken("ltk_metadata"), ws, run, cardArg, role); err != nil {
		t.Fatal(err)
	}
	api := fakeAPIs(t)
	meta := httptest.NewServer(metadata)
	t.Cleanup(meta.Close)
	h := &Hosted{GEO: GEO{Base: api.URL + "/geo", MetadataBase: meta.URL}, OA: OpenAlex{Base: api.URL + "/oa"}, Store: st, Blobs: storage.Local{Dir: t.TempDir()}}
	srv := httptest.NewServer(middleware.AgentRunAuth(st)(h.Handler()))
	t.Cleanup(srv.Close)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: &http.Client{Transport: bearer("ltk_metadata")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return h, cs, env, ws, card, run
}
func metadataCall(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}
func metadataJSON(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestHostedGEOMetadataIsRecorded(t *testing.T) {
	for _, truncated := range []bool{false, true} {
		t.Run(fmt.Sprint(truncated), func(t *testing.T) {
			h, cs, _, ws, card, _ := hostedMetadataFixture(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, "^SAMPLE = GSM1\n!Sample_title = station observations\n!Sample_characteristics_ch1 = group: A\n")
				if truncated {
					fmt.Fprint(w, strings.Repeat("!Sample_description = observed\n", 80000))
				}
			}, false)
			opened := metadataCall(t, cs, "open_dataset", map[string]any{"accession": "GSE1"})
			if opened.IsError {
				t.Fatal(opened)
			}
			out := metadataJSON(t, opened)
			if !strings.Contains(out["structure"].(string), "- GSM2: mouse 2") {
				t.Fatal(out)
			}
			refs, ok := out["metadata_sources"].([]any)
			if !ok {
				t.Fatalf("missing metadata refs: %v", out)
			}
			var source string
			for _, ref := range refs {
				r := ref.(map[string]any)
				if r["relation"] == "sample" && r["accession"] == "GSM1" {
					source = r["source_id"].(string)
				}
			}
			if source != "geo:GSM1" {
				t.Fatal(refs)
			}
			fetched := metadataCall(t, cs, "fetch_text", map[string]any{"source_id": source})
			if fetched.IsError {
				t.Fatal(fetched)
			}
			got := metadataJSON(t, fetched)
			if got["source_kind"] != "dataset_metadata" || got["full_text"] != false {
				t.Fatal(got)
			}
			if (got["truncated"] == true) != truncated {
				t.Fatalf("truncation: %v", got["truncated"])
			}
			text := got["text"].(string)
			if err := h.Store.InWorkspace(context.Background(), ws, uuid.Nil, func(tx *store.Tx) error {
				v, err := records.Load(context.Background(), tx, h.Blobs, card, "grounding")
				if err != nil {
					return err
				}
				openedRecord, ok := v.ByKey("open_dataset", "GSE1")
				if !ok {
					t.Fatal("missing open record")
				}
				savedRefs := records.Body[struct {
					MetadataSources []MetadataRef `json:"metadata_sources"`
				}](openedRecord).MetadataSources
				saved, err := json.Marshal(savedRefs)
				if err != nil {
					return err
				}
				returned, err := json.Marshal(refs)
				if err != nil {
					return err
				}
				var savedValue any
				if err := json.Unmarshal(saved, &savedValue); err != nil {
					return err
				}
				if fmt.Sprint(savedValue) != fmt.Sprint(refs) {
					t.Errorf("persisted metadata references %s, returned %s", saved, returned)
				}
				f, ok := v.ByKey("fetch", source)
				if !ok {
					t.Fatal("no fetch")
				}
				stored, err := v.Text(f)
				if err != nil {
					return err
				}
				body := records.Body[map[string]any](f)
				if stored != text || body["source_kind"] != got["source_kind"] || body["source_url"] != got["source_url"] || body["sha256"] != fmt.Sprintf("%x", sha256.Sum256([]byte(text))) || (body["truncated"] == true) != truncated {
					t.Fatal("record differs", body)
				}
				var readable bool
				readable, err = tx.Q.RoleBlobExists(context.Background(), db.RoleBlobExistsParams{WorkspaceID: ws, CardID: &card, BlobKey: f.BlobKey})
				if err == nil && !readable {
					t.Fatal("blob unreadable")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := h.Store.InWorkspace(context.Background(), uuid.New(), uuid.Nil, func(tx *store.Tx) error {
				v, err := records.Load(context.Background(), tx, h.Blobs, card, "grounding")
				if err != nil {
					return err
				}
				if len(v.Kind("fetch")) != 0 {
					t.Fatal("another workspace read metadata evidence")
				}
				sum := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
				readable, err := tx.Q.RoleBlobExists(context.Background(), db.RoleBlobExistsParams{WorkspaceID: ws, CardID: &card, BlobKey: ws.String() + "/" + card.String() + "/" + sum})
				if readable {
					t.Fatal("another workspace read metadata blob")
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			paper := metadataJSON(t, metadataCall(t, cs, "fetch_text", map[string]any{"source_id": "openalex:W2"}))
			for _, key := range []string{"source_kind", "source_url", "truncated"} {
				if _, ok := paper[key]; ok {
					t.Fatal("legacy field", paper)
				}
			}
		})
	}
}

func TestHostedGEOMetadataCannotOutliveRun(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(fmt.Sprint(revoke), func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			_, cs, env, ws, card, run := hostedMetadataFixture(t, func(w http.ResponseWriter, r *http.Request) {
				close(started)
				<-release
				fmt.Fprint(w, "^SAMPLE = GSM1\n!Sample_title = observations\n")
			}, false)
			done := make(chan *mcp.CallToolResult, 1)
			go func() {
				res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "fetch_text", Arguments: map[string]any{"source_id": "geo:GSM1"}})
				if err != nil {
					done <- nil
					return
				}
				done <- res
			}()
			select {
			case <-started:
			case res := <-done:
				t.Fatalf("metadata dispatch failed before upstream: %+v", res)
			}
			sql := `UPDATE agent_run SET status='completed' WHERE id=$1`
			if revoke {
				sql = `DELETE FROM agent_run_token WHERE agent_run_id=$1`
			}
			if _, err := env.Owner.Exec(context.Background(), sql, run); err != nil {
				close(release)
				t.Fatal(err)
			}
			close(release)
			res := <-done
			if res == nil || !res.IsError {
				t.Fatalf("settled fetch: %+v", res)
			}
			b, _ := json.Marshal(res)
			if strings.Contains(string(b), "observations") {
				t.Fatal("unrecorded text returned", string(b))
			}
			var n int
			if err := env.Owner.QueryRow(context.Background(), `SELECT count(*) FROM record WHERE card_id=$1`, card).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatal("late record", n)
			}
			if err := env.Owner.QueryRow(context.Background(), `SELECT count(*) FROM event WHERE workspace_id=$1 AND type='blob.created'`, ws).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatal("late readable blob", n)
			}
		})
	}
}
func TestFailedMetadataIsNotFetchedEvidence(t *testing.T) {
	for _, mode := range []string{"malformed", "challenge", "rate"} {
		t.Run(mode, func(t *testing.T) {
			h, cs, env, _, card, _ := hostedMetadataFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "rate":
					w.Header().Set("Retry-After", "30")
					w.WriteHeader(429)
					fmt.Fprint(w, "SECRET")
				case "challenge":
					fmt.Fprint(w, "<html>browser check SECRET</html>")
				default:
					fmt.Fprint(w, "invalid SECRET")
				}
			}, false)
			res := metadataCall(t, cs, "fetch_text", map[string]any{"source_id": "geo:GSM1"})
			if !res.IsError {
				t.Fatal(res)
			}
			b, _ := json.Marshal(res)
			if strings.Contains(string(b), "SECRET") {
				t.Fatal("response leaked", string(b))
			}
			if mode == "rate" && (!strings.Contains(string(b), "429") || !strings.Contains(string(b), "30")) {
				t.Fatal(string(b))
			}
			var n int
			if err := env.Owner.QueryRow(context.Background(), `SELECT count(*) FROM record WHERE card_id=$1 AND kind='fetch'`, card).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatal("failed fetch recorded")
			}
			entries, err := os.ReadDir(h.Blobs.(storage.Local).Dir)
			if err != nil || len(entries) != 0 {
				t.Fatal("failed blobs", entries, err)
			}
		})
	}
}
func TestHostedGEOMetadataCardlessLogging(t *testing.T) {
	h, cs, env, ws, _, _ := hostedMetadataFixture(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "^SAMPLE = GSM1\n!Sample_title = observations\n")
	}, true)
	res := metadataCall(t, cs, "fetch_text", map[string]any{"source_id": "geo:GSM1"})
	if res.IsError {
		t.Fatal(res)
	}
	var n int
	if err := env.Owner.QueryRow(context.Background(), `SELECT count(*) FROM event WHERE workspace_id=$1 AND type='sources.called' AND payload->>'key'='geo:GSM1'`, ws).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("missing log", n)
	}
	if err := env.Owner.QueryRow(context.Background(), `SELECT count(*) FROM record WHERE workspace_id=$1`, ws).Scan(&n); err != nil || n != 0 {
		t.Fatal("cardless evidence", n, err)
	}
	entries, err := os.ReadDir(h.Blobs.(storage.Local).Dir)
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
}
