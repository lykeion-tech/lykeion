package sources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lykeion/server/internal/middleware"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
)

func excluded(t *testing.T, lines ...string) *Exclusions {
	t.Helper()
	path := filepath.Join(t.TempDir(), "exclude.txt")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	x, err := LoadExclusions(path)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func TestExclusionsMatchDOIArXivAndTitle(t *testing.T) {
	x := excluded(t, "# kept from agents", "10.1103/Q5R1-WHJR", "2509.13272", "Ionization and temperature measurements in warm dense copper", "Short")
	for _, s := range []string{
		"https://doi.org/10.1103/q5r1-whjr",
		"arXiv:2509.13272v2",
		"10.48550/arxiv.2509.13272",
		"IONIZATION and Temperature Measurements in Warm-Dense Copper using x-ray absorption",
	} {
		if !x.Match(s) {
			t.Errorf("%q passed", s)
		}
	}
	for _, s := range []string{"arXiv:2509.132721", "12509.13272", "warm dense copper", "Short", "10.1103/q5r1-whj", "10.1103/q5r1-whjr2"} {
		if x.Match(s) {
			t.Errorf("%q was kept back", s)
		}
	}
	var none *Exclusions
	if none.Match("10.1103/q5r1-whjr") {
		t.Error("no exclusions kept something back")
	}
}

func TestExcludedPapersAreHiddenFromSearchAndFetch(t *testing.T) {
	env := testutil.Setup(t)
	api := fakeAPIs(t)
	st := &store.Store{Pool: env.App}
	ctx := context.Background()
	u, err := st.Global().UpsertUserByEmail(ctx, "x@lab.org")
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
		{`INSERT INTO agent_run_token (token_hash,workspace_id,agent_run_id,card_id,role,attempt,expires_at) VALUES ($1,$2,$3,$4,'literature-scout',1,now()+interval '1 hour')`, []any{hashToken("ltk_excl"), ws, run, card}},
	} {
		if _, err := env.Owner.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	// "No effect" is OpenAlex W2 (DOI 10.1/b); "X raises Y" is Europe PMC PMC1 (DOI 10.1/a).
	h := &Hosted{OA: OpenAlex{Base: api.URL + "/oa"}, EPMC: EuropePMC{Base: api.URL + "/epmc"}, Store: st, Blobs: storage.Local{Dir: t.TempDir()},
		Exclude: excluded(t, "10.1/b")}
	srv := httptest.NewServer(middleware.AgentRunAuth(st)(h.Handler()))
	defer srv.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: &http.Client{Transport: bearer("ltk_excl")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	call := func(name string, args map[string]any) *mcp.CallToolResult {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := call("search_literature", map[string]any{"database": "openalex", "query": "no effect"}); res.IsError || strings.Contains(textOf(res), "W2") {
		t.Errorf("search showed the excluded paper: %s", textOf(res))
	}
	if res := call("fetch_text", map[string]any{"source_id": "openalex:W2"}); !res.IsError {
		t.Errorf("fetch read the excluded paper: %s", textOf(res))
	}
	if res := call("search_literature", map[string]any{"database": "europepmc", "query": "x y"}); res.IsError || !strings.Contains(textOf(res), "PMC1") {
		t.Errorf("search hid a paper not excluded: %s", textOf(res))
	}
	if res := call("fetch_text", map[string]any{"source_id": "europepmc:PMC1"}); res.IsError {
		t.Errorf("fetch refused a paper not excluded: %s", textOf(res))
	}
}

func TestAConnectorAnswerNamingAnExcludedPaperIsRefused(t *testing.T) {
	f := newConnFix(t, ok(`{"id":"ENSG00000141510","display_name":"TP53","doi":"10.1/b"}`))
	f.h.Exclude = excluded(t, "10.1/b")
	if out, err := f.fetch(f.card, tp53); err == nil || strings.Contains(out.Body, "TP53") {
		t.Fatalf("connector answer naming an excluded paper passed: %v %+v", err, out)
	}
	f.h.Exclude = excluded(t, "10.1/c")
	if _, err := f.fetch(f.card, tp53); err != nil {
		t.Fatalf("connector answer refused without an excluded paper: %v", err)
	}
}

func textOf(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestExclusionsMatchPMCIDsAndCloseTitles(t *testing.T) {
	x := excluded(t, "PMC12345", "Spatial transcriptomics of the developing mouse cortex at single cell resolution")
	if !x.Match("europepmc:PMC12345") || !x.Match("pmc12345") || x.Match("PMC123456") || x.Match("PMC1234") {
		t.Error("PMC ids match whole only")
	}
	for title, want := range map[string]bool{
		"Spatial transcriptomics of the developing mouse cortex at single-cell resolution":                                                    true,
		"High-resolution spatial transcriptomics of the developing mouse cortex at single cell resolution":                                    true,
		"Spatial transcriptomics of the developing mouse cortex":                                                                              false,
		"Spatial transcriptomics of the developing human and mouse cortex at single cell resolution, revisited":                               true,
		"Spatial transcriptomics and proteomics of the developing mouse brain cortex in health, disease and ageing at single cell resolution": false,
	} {
		if got := x.Hides(Hit{Title: title}); got != want {
			t.Errorf("Hides(%q) = %v", title, got)
		}
	}
	if x.Match("the developing mouse cortex spatial transcriptomics resolution single cell at of") {
		t.Error("a body matched a title by its words alone")
	}
}
