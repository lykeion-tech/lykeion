package sources

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
