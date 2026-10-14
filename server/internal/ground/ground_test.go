package ground

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/sources"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
	db "lykeion/server/pkg/db/generated"
)

type fixture struct {
	env   *testutil.Env
	st    *store.Store
	blobs storage.Storage
	src   *sources.Hosted
	ws    uuid.UUID
	card  uuid.UUID
	owner uuid.UUID
}

func setup(t *testing.T) *fixture {
	t.Helper()
	env := testutil.Setup(t)
	f := &fixture{env: env, st: &store.Store{Pool: env.App}, blobs: storage.Local{Dir: t.TempDir()}, ws: uuid.New(), card: uuid.New()}
	f.src = &sources.Hosted{Store: f.st, Blobs: f.blobs}
	ctx := context.Background()
	u, _ := f.st.Global().UpsertUserByEmail(ctx, "a@lab.org")
	f.owner = u.ID
	env.Owner.Exec(ctx, `INSERT INTO workspace (id, slug, name) VALUES ($1, 'w', 'w')`, f.ws)
	env.Owner.Exec(ctx, `INSERT INTO card (id, workspace_id, number, title, question, owner_id, board_column, round) VALUES ($1, $2, 1, 't', 'q', $3, 'grounding', 1)`, f.card, f.ws, u.ID)
	f.record(t, Literature, "search", "", map[string]any{"database": "europepmc", "query": "x y"}, "")
	f.record(t, Contrarian, "search", "", map[string]any{"database": "openalex", "query": "x no effect"}, "")
	f.record(t, Literature, "fetch", "europepmc:PMC1", map[string]any{"source_id": "europepmc:PMC1", "full_text": true}, "In treated mice, X raised\n Y by 40% over controls.")
	f.record(t, Contrarian, "fetch", "openalex:W2", map[string]any{"source_id": "openalex:W2"}, "We found no effect of X on Y in rats.")
	f.record(t, DataScout, "open_dataset", "GSE1", map[string]any{"accession": "GSE1"}, "accession: GSE1\nsamples (2):\n- GSM1: mouse 1 liver\n- GSM2: mouse 2 liver\n")
	return f
}

func tokenFor(run uuid.UUID) string { return "ltk_" + run.String() }

func (f *fixture) role(t *testing.T, name string) middleware.Role {
	t.Helper()
	ctx := context.Background()
	run := uuid.New()
	if _, err := f.env.Owner.Exec(ctx, `INSERT INTO agent_run (id, workspace_id, card_id, responsible_user_id, board_column, round, role, harness, prompt, status, attempt)
		VALUES ($1, $2, $3, $4, 'grounding', 1, $5, 'claude', 'p', 'running', 1)`, run, f.ws, f.card, f.owner, name); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.Owner.Exec(ctx, `INSERT INTO agent_run_token (token_hash, workspace_id, agent_run_id, card_id, role, attempt, expires_at)
		VALUES ($1, $2, $3, $4, $5, 1, now() + interval '1 hour')`, auth.Hash(tokenFor(run)), f.ws, run, f.card, name); err != nil {
		t.Fatal(err)
	}
	return middleware.Role{AgentRunID: run, WorkspaceID: f.ws, CardID: f.card, Role: name, Column: "grounding", Round: 1}
}

func (f *fixture) record(t *testing.T, role, kind, key string, body map[string]any, text string) {
	t.Helper()
	r := f.role(t, role)
	if err := f.src.Record(context.Background(), r, tokenFor(r.AgentRunID), kind, key, body, text); err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.Owner.Exec(context.Background(), `UPDATE agent_run SET status = 'completed', completed_at = now() WHERE id = $1`, r.AgentRunID); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) write(t *testing.T, role, kind string, body any) (string, error) {
	t.Helper()
	k, ok := Kinds()[kind]
	if !ok {
		t.Fatalf("no kind %s", kind)
	}
	raw, _ := json.Marshal(body)
	key := ""
	err := f.st.InWorkspace(context.Background(), f.ws, uuid.Nil, func(tx *store.Tx) error {
		if !contains(k.Roles, role) {
			return service.Invalid(role + " may not write " + kind)
		}
		if k.Validate != nil {
			if err := k.Validate(raw); err != nil {
				return err
			}
		}
		if k.KeyPrefix != "" {
			n, err := tx.Q.CountKindRecords(context.Background(), countParams(f, kind))
			if err != nil {
				return err
			}
			key = k.KeyPrefix + itoa(n+1)
		}
		if k.Prepare != nil {
			var err error
			raw, err = k.Prepare(context.Background(), tx, service.Writer{Card: f.card, Column: "grounding", Role: role, Round: 1}, raw)
			if err != nil {
				return err
			}
		}
		_, err := tx.Q.InsertRecord(context.Background(), insertParams(f, role, kind, key, raw))
		return err
	})
	return key, err
}

func TestEvidenceRules(t *testing.T) {
	f := setup(t)
	if _, err := f.write(t, Literature, "evidence", map[string]string{"claim": "c", "source_id": "openalex:W9", "quote": "q", "stance": "supports"}); err == nil || !strings.Contains(err.Error(), "never fetched") {
		t.Fatalf("unfetched source: %v", err)
	}
	if _, err := f.write(t, Contrarian, "evidence", map[string]string{"claim": "c", "source_id": "openalex:W2", "quote": "no effect", "stance": "supports"}); err == nil {
		t.Fatal("the contrarian recorded supporting evidence")
	}
	key, err := f.write(t, Literature, "evidence", map[string]string{"claim": "X raises Y", "source_id": "europepmc:PMC1", "quote": "X raised Y", "stance": "supports"})
	if err != nil || key != "E1" {
		t.Fatalf("%q %v", key, err)
	}
	if _, err := f.write(t, DataScout, "dataset", map[string]string{"accession": "GSE9"}); err == nil {
		t.Fatal("a dataset never opened was accepted")
	}
	if key, err := f.write(t, DataScout, "dataset", map[string]string{"accession": "GSE1", "note": "liver"}); err != nil || key != "D1" {
		t.Fatalf("%q %v", key, err)
	}
	if _, err := f.write(t, Reviewer, "verdict", map[string]string{"result": "fail"}); err == nil {
		t.Fatal("a failing verdict without a reason was accepted")
	}
	for _, to := range []string{"hypotheses", "grounding", "done", "nowhere"} {
		if _, err := f.write(t, Reviewer, "verdict", map[string]string{"result": "fail", "to": to, "reason": "r"}); err == nil || !strings.Contains(err.Error(), "not a stage before") {
			t.Fatalf("verdict routed to %s: %v", to, err)
		}
	}
	if _, err := f.write(t, Reviewer, "verdict", map[string]string{"result": "fail", "step": "contrarian", "reason": "r"}); err != nil {
		t.Fatalf("a verdict that stays in the stage: %v", err)
	}
}

func TestRoleKindsExcludeServerRecords(t *testing.T) {
	for _, k := range RoleKinds(Literature) {
		if k == "search" || k == "fetch" {
			t.Fatal("a rerun would supersede the role's fetched sources")
		}
	}
	if !contains(RoleKinds(Literature), "evidence") {
		t.Fatal("evidence must be superseded on rerun")
	}
}

func TestSourceRecordSchemasDecodeStoredMetadata(t *testing.T) {
	fetch := records.Body[Fetch](db.Record{Body: []byte(`{"source_id":"europepmc:PMC1","full_text":true,"retracted":true}`)})
	if fetch.SourceID != "europepmc:PMC1" || !fetch.FullText || !fetch.Retracted {
		t.Fatalf("decoded fetch metadata: %+v", fetch)
	}
	search := records.Body[Search](db.Record{Body: []byte(`{"database":"openalex","query":"x no effect"}`)})
	if search.Database != "openalex" || search.Query != "x no effect" {
		t.Fatalf("decoded search metadata: %+v", search)
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
func itoa(n int64) string { return strconv.FormatInt(n, 10) }
func countParams(f *fixture, kind string) db.CountKindRecordsParams {
	return db.CountKindRecordsParams{WorkspaceID: f.ws, CardID: f.card, BoardColumn: "grounding", Kind: kind}
}
func insertParams(f *fixture, role, kind, key string, body []byte) db.InsertRecordParams {
	return db.InsertRecordParams{WorkspaceID: f.ws, CardID: f.card, BoardColumn: "grounding", Round: 1, Kind: kind, Role: role, Key: key, Body: body}
}
