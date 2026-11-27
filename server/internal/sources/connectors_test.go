package sources

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/connectors"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
)

var testSecret = bytes.Repeat([]byte{7}, 32)

func TestStandaloneAgentConnectorSelection(t *testing.T) {
	f := newConnFix(t, ok(`{}`))
	ctx := t.Context()
	var agent uuid.UUID
	if err := f.env.Owner.QueryRow(ctx, `INSERT INTO agent(workspace_id,name,harness,owner_id) VALUES($1,'agent','claude',$2) RETURNING id`, f.ws, f.user).Scan(&agent); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.Owner.Exec(ctx, `INSERT INTO agent_capabilities(workspace_id,agent_id,skills,connectors) VALUES($1,$2,'[]','[{"name":"ensembl","catalogue":"ensembl"}]')`, f.ws, agent); err != nil {
		t.Fatal(err)
	}
	role := f.role(f.card, "agent")
	if _, err := f.env.Owner.Exec(ctx, `UPDATE agent_run SET agent_id=$1 WHERE id=$2`, agent, role.AgentRunID); err != nil {
		t.Fatal(err)
	}
	got, err := f.h.Connectors(ctx, role)
	if err != nil || len(got) != 1 || got[0].Name != "ensembl" {
		t.Fatalf("selected connectors: %+v %v", got, err)
	}
	_, err = f.h.Fetch(ctx, role, runTok(role), ConnectorIn{Connector: "ena", Path: "/"})
	if err == nil || !strings.Contains(err.Error(), "does not have") {
		t.Fatalf("unselected connector was allowed: %v", err)
	}
}

type connFix struct {
	t               *testing.T
	env             *testutil.Env
	st              *store.Store
	blobs           storage.Local
	h               *Hosted
	ws, card, card2 uuid.UUID
	user            uuid.UUID
	hits            atomic.Int32
}

func newConnFix(t *testing.T, upstream http.HandlerFunc) *connFix {
	t.Helper()
	env := testutil.Setup(t)
	f := &connFix{t: t, env: env, st: &store.Store{Pool: env.App}, blobs: storage.Local{Dir: t.TempDir()}, ws: uuid.New(), card: uuid.New(), card2: uuid.New()}
	client := testutil.Upstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.hits.Add(1); upstream(w, r) }))
	f.h = &Hosted{Store: f.st, Blobs: f.blobs, Conn: &connectors.Client{HTTP: client, Contact: "lab@example.org", Secret: testSecret}}
	ctx := context.Background()
	u, _ := f.st.Global().UpsertUserByEmail(ctx, "a@lab.org")
	f.user = u.ID
	env.Owner.Exec(ctx, `INSERT INTO workspace (id, slug, name) VALUES ($1, 'w', 'w')`, f.ws)
	for i, c := range []uuid.UUID{f.card, f.card2} {
		env.Owner.Exec(ctx, `INSERT INTO card (id, workspace_id, number, title, question, owner_id, board_column, round) VALUES ($1, $2, $3, $4, $4, $5, 'grounding', 1)`,
			c, f.ws, i+1, "card "+string(rune('a'+i)), u.ID)
	}
	return f
}

// team gives card a Team for its grounding column, whose members declare the
// catalogue connectors in declared (member name to connector names).
func (f *connFix) team(card uuid.UUID, declared map[string][]string) {
	f.t.Helper()
	ctx := context.Background()
	zipBytes := testutil.Pack(f.t, testutil.PackOpts{Catalogue: declared, Name: "team-" + card.String()})
	p, err := pack.Parse(zipBytes)
	if err != nil {
		f.t.Fatal(err)
	}
	key, err := pack.PutBlob(ctx, f.blobs, f.ws, zipBytes, p.SHA256)
	if err != nil {
		f.t.Fatal(err)
	}
	var packID uuid.UUID
	if err := f.st.InWorkspace(ctx, f.ws, uuid.Nil, func(tx *store.Tx) error {
		row, err := pack.Insert(ctx, tx, key, p, nil)
		packID = row.ID
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.env.Owner.Exec(ctx, `INSERT INTO card_team (workspace_id, card_id, board_column, pack_id) VALUES ($1, $2, 'grounding', $3)`, f.ws, card, packID); err != nil {
		f.t.Fatal(err)
	}
}

// role is a running run, attempt 1, with its token, so the records its
// fetches write point at a real run and pass the sources server's re-check.
// On a card it works the grounding column as the named Team member; card-less
// (uuid.Nil) it is an Autoresearch run, whose fetch writes only an event.
func (f *connFix) role(card uuid.UUID, member string) middleware.Role {
	f.t.Helper()
	ctx := context.Background()
	run := uuid.New()
	role := middleware.Role{AgentRunID: run, WorkspaceID: f.ws, CardID: card, Role: member, Column: "grounding", Round: 1, Scopes: []string{service.ScopeRole}}
	var cardID *uuid.UUID
	q := `INSERT INTO agent_run (id, workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, attempt)
		VALUES ($1, $2, $3, $4, 'grounding', 1, $5, 'claude', 'p', 'running', 1)`
	args := []any{run, f.ws, nil, f.user, member}
	if card == uuid.Nil {
		role = middleware.Role{AgentRunID: run, WorkspaceID: f.ws, Role: "autoresearch", Scopes: []string{service.ScopeReport}}
		q = `INSERT INTO agent_run (id, workspace_id, card_id, responsible_user_id, kind, requester_id, board_column, round, role, harness, prompt, status, attempt)
			VALUES ($1, $2, $3, $4, 'autoresearch', $4, '', 0, 'autoresearch', 'claude', 'p', 'running', 1)`
		args = args[:4]
	} else {
		cardID = &card
		args[2] = card
	}
	if _, err := f.env.Owner.Exec(ctx, q, args...); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.env.Owner.Exec(ctx, `INSERT INTO agent_run_token (token_hash, workspace_id, agent_run_id, card_id, role, attempt, scopes, expires_at)
		VALUES ($1, $2, $3, $4, $5, 1, $6, now() + interval '1 hour')`, auth.Hash(runTok(role)), f.ws, run, cardID, role.Role, role.Scopes); err != nil {
		f.t.Fatal(err)
	}
	return role
}

// runTok is the token role seeds for a run.
func runTok(r middleware.Role) string { return "ltk_" + r.AgentRunID.String() }

// fetchAs calls Fetch as a fresh running role on the card, with its token.
func (f *connFix) fetchAs(card uuid.UUID, member string, in ConnectorIn) (ConnectorOut, error) {
	r := f.role(card, member)
	return f.h.Fetch(context.Background(), r, runTok(r), in)
}

func (f *connFix) fetch(card uuid.UUID, in ConnectorIn) (ConnectorOut, error) {
	return f.fetchAs(card, "agent", in)
}

func (f *connFix) count(q string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.env.Owner.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *connFix) names(r middleware.Role) []string {
	f.t.Helper()
	cs, err := f.h.Connectors(context.Background(), r)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []string
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func ok(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}
}

var tp53 = ConnectorIn{Connector: "ensembl", Path: "/lookup/id/ENSG00000141510", Query: map[string]string{"content-type": "application/json"}}
var uniprot = ConnectorIn{Connector: "uniprot", Path: "/uniprotkb/P01308.txt"}

func TestATeamMemberSeesOnlyItsDeclaredConnectors(t *testing.T) {
	f := newConnFix(t, ok(`{}`))
	f.team(f.card, map[string][]string{"member-1": {"ensembl"}, "pi": {"uniprot"}})

	member := f.role(f.card, "member-1")
	if got := f.names(member); !slices.Equal(got, []string{"ensembl"}) {
		t.Fatalf("member-1 sees %v", got)
	}
	_, err := f.h.Fetch(context.Background(), member, runTok(member), uniprot)
	if err == nil || !strings.Contains(err.Error(), "this study's Team does not declare `uniprot`") || f.hits.Load() != 0 {
		t.Fatalf("%v, %d upstream calls", err, f.hits.Load())
	}
	if got := f.names(f.role(f.card, "leader")); !slices.Equal(got, []string{"uniprot"}) {
		t.Fatalf("the leader sees %v", got)
	}
	if got := f.names(f.role(f.card, "auditor")); len(got) != 0 {
		t.Fatalf("the auditor declares none and sees %v", got)
	}
	if _, err := f.fetchAs(f.card, "member-1", tp53); err != nil {
		t.Fatalf("a declared connector: %v", err)
	}
}

func TestASingleAgentSeesEveryEnabledConnector(t *testing.T) {
	f := newConnFix(t, ok(`{}`))
	f.team(f.card, map[string][]string{"member-1": {"ensembl"}})
	all := f.names(f.role(f.card2, "agent")) // card2 has no Team
	if !slices.Contains(all, "uniprot") || !slices.Contains(all, "ensembl") || len(all) < 10 {
		t.Fatalf("a card with no Team: %v", all)
	}
	if got := f.names(f.role(f.card, "agent")); !slices.Equal(got, all) {
		t.Fatalf("role agent on a Team's column: %v", got)
	}
	if got := f.names(f.role(uuid.Nil, "")); !slices.Equal(got, all) {
		t.Fatalf("an Autoresearch run: %v", got)
	}
	f.env.Owner.Exec(context.Background(), `INSERT INTO workspace_connector (workspace_id, name, enabled, updated_by) VALUES ($1, 'uniprot', false, $2)`, f.ws, f.user)
	if got := f.names(f.role(f.card2, "agent")); !slices.Contains(got, "uniprot") || !slices.Contains(got, "ensembl") {
		t.Fatalf("connector missing despite old setting: %v", got)
	}
	if _, err := f.fetch(f.card2, uniprot); err != nil {
		t.Fatalf("connector unavailable despite old setting: %v", err)
	}
}

func TestAFetchWritesACallRecord(t *testing.T) {
	f := newConnFix(t, ok(`{"id":"ENSG00000141510","display_name":"TP53"}`))
	ctx := context.Background()
	out, err := f.fetch(f.card, tp53)
	if err != nil || out.Cached || out.Status != 200 || !strings.Contains(out.Body, "TP53") || !strings.HasPrefix(out.SourceID, "ensembl:") || out.Notice == "" {
		t.Fatalf("%v %+v", err, out)
	}
	// the agent is told to cite the call, not the request
	if out.Call != "c1" || !strings.HasSuffix(out.Notice, "Cite it as call:c1.") {
		t.Fatalf("call %q notice %q", out.Call, out.Notice)
	}
	type callBody struct {
		Server, Tool, Answer, At string
		AnswerSHA256             string `json:"answer_sha256"`
		SourceID                 string `json:"source_id"`
		Args                     connectors.Request
		Truncated                bool
		AgentRunID               uuid.UUID `json:"agent_run_id"`
	}
	call := func(card uuid.UUID, key string) (callBody, string) {
		var b callBody
		var blob string
		f.st.InWorkspace(ctx, f.ws, uuid.Nil, func(tx *store.Tx) error {
			v, _ := records.Load(ctx, tx, f.blobs, card, "grounding")
			r, found := byKey(v, "call", key)
			if !found || r.Role != "agent" {
				t.Errorf("no %s record by the run: %+v", key, r)
			}
			b, blob = records.Body[callBody](r), r.BlobKey
			return nil
		})
		return b, blob
	}
	sum := sha256.Sum256([]byte(out.Body))
	b, blob := call(f.card, "c1")
	if b.Server != "catalogue:ensembl" || b.Tool != "fetch" || b.Answer != out.Body || b.AnswerSHA256 != hex.EncodeToString(sum[:]) ||
		b.SourceID != out.SourceID || b.Args.Path != tp53.Path || b.Truncated || b.At == "" || b.AgentRunID == uuid.Nil || blob != "" {
		t.Fatalf("%+v blob %q", b, blob)
	}
	if n := f.count(`SELECT count(*) FROM record WHERE kind = 'fetch'`); n != 0 {
		t.Fatalf("%d fetch records", n)
	}

	// the same request within a day is served from the cache and still recorded,
	// under the next key
	again, err := f.fetch(f.card, tp53)
	if err != nil || !again.Cached || again.Body != out.Body || f.hits.Load() != 1 {
		t.Fatalf("%v cached %v, %d upstream calls", err, again.Cached, f.hits.Load())
	}
	if b, _ := call(f.card, "c2"); b.Answer != out.Body {
		t.Fatalf("c2: %+v", b)
	}
	// keys are per study
	if _, err := f.fetch(f.card2, tp53); err != nil {
		t.Fatal(err)
	}
	if b, _ := call(f.card2, "c1"); b.Answer != out.Body {
		t.Fatalf("card2 c1: %+v", b)
	}
	f.env.Owner.Exec(ctx, `UPDATE connector_fetch SET fetched_at = now() - interval '25 hours'`)
	if later, err := f.fetch(f.card, tp53); err != nil || later.Cached || f.hits.Load() != 2 {
		t.Fatalf("after a day: %v cached %v, %d upstream calls", err, later.Cached, f.hits.Load())
	}
}

func TestAnAnswerOverTheBodyLimitKeepsItsRestAsABlob(t *testing.T) {
	big := strings.Repeat("a", 300<<10)
	f := newConnFix(t, ok(`{"x":"`+big+`"}`))
	ctx := context.Background()
	out, err := f.fetch(f.card, tp53)
	if err != nil {
		t.Fatal(err)
	}
	f.st.InWorkspace(ctx, f.ws, uuid.Nil, func(tx *store.Tx) error {
		v, _ := records.Load(ctx, tx, f.blobs, f.card, "grounding")
		r, _ := byKey(v, "call", "c1")
		b := records.Body[struct {
			Answer       string
			AnswerSHA256 string `json:"answer_sha256"`
			Truncated    bool
		}](r)
		full, err := v.Text(r)
		sum := sha256.Sum256([]byte(out.Body))
		if !b.Truncated {
			t.Error("a body that keeps part of the answer says it is not truncated")
		}
		if len(b.Answer) != MaxCallAnswer || !strings.HasPrefix(out.Body, b.Answer) || full != out.Body || err != nil || b.AnswerSHA256 != hex.EncodeToString(sum[:]) {
			t.Errorf("body %d bytes, blob %d bytes, err %v", len(b.Answer), len(full), err)
		}
		return nil
	})
}

func TestRefusedRequestsRecordFailures(t *testing.T) {
	f := newConnFix(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte{0x89, 'P', 'N', 'G', 0xff}) })
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prev)
	if _, err := f.fetch(f.card, ConnectorIn{Connector: "string", Path: "/image/network"}); err == nil {
		t.Fatal("a binary response was accepted")
	}
	if _, err := f.fetch(f.card, ConnectorIn{Connector: "chembl", Path: "/../ena/x"}); err == nil {
		t.Fatal("a path out of the base was accepted")
	}
	if n := f.count(`SELECT count(*) FROM record WHERE kind='call' AND body->>'error' <> ''`); n != 2 {
		t.Fatalf("%d failure records for refused requests", n)
	}
	if n := f.count(`SELECT count(*) FROM connector_fetch`); n != 0 {
		t.Fatalf("%d cached refusals", n)
	}
	if strings.Count(logs.String(), "connector request refused") != 2 {
		t.Fatalf("refusals not logged:\n%s", logs.String())
	}
}

func TestUpstreamThrottleNamesRetryAfter(t *testing.T) {
	f := newConnFix(t, func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Retry-After", "7"); w.WriteHeader(429) })
	_, err := f.fetch(f.card, tp53)
	if err == nil || !strings.Contains(err.Error(), "answered 429") || !strings.Contains(err.Error(), "retry after 7") {
		t.Fatalf("%v", err)
	}
	if n := f.count(`SELECT count(*) FROM record WHERE kind='call' AND body->>'error' <> ''`); n != 1 {
		t.Fatalf("%d failure records for throttling", n)
	}
	if n := f.count(`SELECT count(*) FROM connector_fetch`); n != 0 {
		t.Fatalf("%d cached throttled responses", n)
	}
}

func TestAConnectorKeyNeverLeavesTheServer(t *testing.T) {
	const key = "mp-sk+live/123=abc"
	f := newConnFix(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"echo":"` + r.Header.Get("X-API-KEY") + `","q":"` + r.URL.RawQuery + `"}`))
	})
	ctx := context.Background()
	for _, name := range []string{"materials-project", "ncbi-gene"} {
		sealed, err := f.h.Conn.Seal(f.ws, name, key)
		if err != nil {
			t.Fatal(err)
		}
		f.env.Owner.Exec(ctx, `INSERT INTO workspace_connector (workspace_id, name, key_ciphertext, updated_by) VALUES ($1, $2, $3, $4)`, f.ws, name, sealed, f.user)
	}
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prev)

	leak := func(where, s string) {
		t.Helper()
		if strings.Contains(s, key) || strings.Contains(s, url.QueryEscape(key)) {
			t.Errorf("the key reached %s: %s", where, s)
		}
	}
	for _, in := range []ConnectorIn{{Connector: "materials-project", Path: "/materials/summary/mp-149"}, {Connector: "ncbi-gene", Path: "/esearch.fcgi", Query: map[string]string{"db": "gene"}}} {
		out, err := f.fetch(f.card, in)
		if err != nil || !strings.Contains(out.Body, "[key removed]") {
			t.Fatalf("%s: %v %+v", in.Connector, err, out)
		}
		leak("a tool result", out.Body+out.SourceID)
		// an Autoresearch run's fetch is logged as an event: the key must stay out of it too
		in.Query = map[string]string{"db": "gene", "n": "2"}
		if _, err := f.fetch(uuid.Nil, in); err != nil {
			t.Fatal(err)
		}
	}
	f.fetch(f.card, ConnectorIn{Connector: "materials-project", Path: "/../x"})
	leak("the log", logs.String())
	rows, _ := f.env.Owner.Query(ctx, `SELECT body::text FROM record UNION ALL SELECT payload::text FROM event`)
	for rows.Next() {
		var s string
		rows.Scan(&s)
		leak("a record or event", s)
	}
	rows.Close()
	filepath.WalkDir(f.blobs.Dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			b, _ := os.ReadFile(p)
			leak("a blob", string(b))
		}
		return nil
	})
}

func TestAKeyThatNoLongerDecryptsIsNeverSentWithout(t *testing.T) {
	f := newConnFix(t, ok(`{}`))
	old := &connectors.Client{Secret: bytes.Repeat([]byte{9}, 32)}
	sealed, _ := old.Seal(f.ws, "materials-project", "mp-key-123456")
	f.env.Owner.Exec(context.Background(), `INSERT INTO workspace_connector (workspace_id, name, key_ciphertext, updated_by) VALUES ($1, 'materials-project', $2, $3)`, f.ws, sealed, f.user)
	_, err := f.fetch(f.card, ConnectorIn{Connector: "materials-project", Path: "/materials/summary/mp-149"})
	if err == nil || !strings.Contains(err.Error(), "saves it again in Connectors") || f.hits.Load() != 0 {
		t.Fatalf("%v, %d upstream calls", err, f.hits.Load())
	}
}

func TestAnAutoresearchRunFetchesWithoutEvidence(t *testing.T) {
	f := newConnFix(t, ok(`{"display_name":"TP53"}`))
	run := f.role(uuid.Nil, "")
	out, err := f.h.Fetch(context.Background(), run, runTok(run), tp53)
	if err != nil || !strings.Contains(out.Body, "TP53") {
		t.Fatalf("%v %+v", err, out)
	}
	if n := f.count(`SELECT count(*) FROM record`); n != 0 {
		t.Fatalf("an Autoresearch run wrote %d records", n)
	}
	if n := f.count(`SELECT count(*) FROM event WHERE type = 'sources.called' AND payload->>'key' = $1 AND payload->>'agent_run_id' = $2`,
		out.SourceID, run.AgentRunID.String()); n != 1 {
		t.Fatalf("the fetch was logged %d times on the run", n)
	}
}

func TestListConnectorsNamesOnlyReadyOnes(t *testing.T) {
	f := newConnFix(t, ok(`{}`))
	r := f.role(f.card, "agent")
	got := f.names(r)
	for _, want := range []string{"ensembl", "ncbi-gene", "unpaywall", "gnomad"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s missing from %v", want, got)
		}
	}
	for _, not := range []string{"omim", "materials-project", "kegg"} {
		if slices.Contains(got, not) {
			t.Errorf("%s listed before the workspace allowed it", not)
		}
	}
	f.h.Conn.Contact = ""
	if slices.Contains(f.names(r), "unpaywall") {
		t.Error("unpaywall listed with no contact email")
	}
}

func TestTheConnectorToolsAreServedOnlyWithAClient(t *testing.T) {
	f := newConnFix(t, ok(`{}`))
	list := func(h *Hosted) []string {
		ctx := context.Background()
		ct, st := mcp.NewInMemoryTransports()
		if _, err := h.server(f.role(f.card, "agent")).Connect(ctx, st, nil); err != nil {
			t.Fatal(err)
		}
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer cs.Close()
		res, err := cs.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, tl := range res.Tools {
			names = append(names, tl.Name)
		}
		return names
	}
	if got := list(f.h); !slices.Contains(got, "fetch") || !slices.Contains(got, "list_connectors") {
		t.Errorf("%v", got)
	}
	if got := list(&Hosted{Store: f.st, Blobs: f.blobs}); slices.Contains(got, "fetch") {
		t.Errorf("a server with no connector client offers fetch: %v", got)
	}
}

func TestAWorkspaceSearchesOpenAlexUnderItsOwnKey(t *testing.T) {
	f := newConnFix(t, ok(`{}`))
	ctx := context.Background()
	f.h.OA = OpenAlex{Base: "https://api.openalex.test", Key: "server-key-1"}
	role := middleware.Role{WorkspaceID: f.ws}
	if oa, err := f.h.openAlex(ctx, role); err != nil || oa.Key != "server-key-1" {
		t.Fatalf("with no workspace key the server's is used: %q %v", oa.Key, err)
	}
	sealed, _ := f.h.Conn.Seal(f.ws, "openalex", "workspace-key-1")
	f.env.Owner.Exec(ctx, `INSERT INTO workspace_connector (workspace_id, name, key_ciphertext, updated_by) VALUES ($1, 'openalex', $2, $3)`, f.ws, sealed, f.user)
	if oa, err := f.h.openAlex(ctx, role); err != nil || oa.Key != "workspace-key-1" || oa.Base != f.h.OA.Base {
		t.Fatalf("the workspace's key: %q %v", oa.Key, err)
	}
	if f.h.OA.Key != "server-key-1" {
		t.Fatal("a workspace's key replaced the server's")
	}
	if _, err := f.env.Owner.Exec(ctx, `UPDATE workspace_connector SET enabled = false WHERE workspace_id = $1 AND name = 'openalex'`, f.ws); err != nil {
		t.Fatal(err)
	}
	if oa, err := f.h.openAlex(ctx, role); err != nil || oa.Key != "workspace-key-1" {
		t.Fatalf("OpenAlex unavailable despite old setting: %v", err)
	}
	if _, err := f.env.Owner.Exec(ctx, `UPDATE workspace_connector SET enabled = true WHERE workspace_id = $1 AND name = 'openalex'`, f.ws); err != nil {
		t.Fatal(err)
	}
	old, _ := (&connectors.Client{Secret: bytes.Repeat([]byte{9}, 32)}).Seal(f.ws, "openalex", "workspace-key-1")
	f.env.Owner.Exec(ctx, `UPDATE workspace_connector SET key_ciphertext = $2 WHERE workspace_id = $1 AND name = 'openalex'`, f.ws, old)
	if _, err := f.h.openAlex(ctx, role); err == nil || !strings.Contains(err.Error(), "saves it again in Connectors") {
		t.Fatalf("a key that no longer opens: %v", err)
	}
}
