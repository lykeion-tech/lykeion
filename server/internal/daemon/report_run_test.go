package daemon

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

// A report run reads only its brief: its CLI runs read-only with none of its
// own tools, its door serves no tool, and it gets no study folder.
func TestReportRunHasNoTools(t *testing.T) {
	ctx := context.Background()
	f := fixture(t)
	_, err := f.env.Owner.Exec(ctx, "DELETE FROM agent_run WHERE id=$1", f.id)
	must(t, err)
	id, err := daemonID(f.cfg.StateDir, f.cfg.Server)
	must(t, err)
	rts, err := f.runs.Register(ctx, f.user, f.ws, id, f.cfg.Device, []string{"claude"}, nil, agent.Platform{})
	must(t, err)
	must(t, f.runs.Store.InWorkspace(ctx, f.ws, f.user, func(tx *store.Tx) error {
		r, err := tx.Q.InsertReportAgentRun(ctx, db.InsertReportAgentRunParams{WorkspaceID: f.ws, CardID: f.card, BoardColumn: "grounding", Round: 1,
			Harness: "claude", PinnedRuntimeID: &rts[0].ID, Prompt: "brief", Skill: "# Reporter"})
		f.id = r.ID
		return err
	}))
	f.handInOutput = `{"status":"done","reason":null,"concepts":[],"facts":[],"links":[]}`
	var tools []string
	calls, paths, failed := handInRun(t, f, func(o agent.ExecOptions) {
		if o.Lab || !o.ReadOnly || !o.NoTools || len(o.ReadDirs) != 0 || o.DoorURL == "" {
			t.Errorf("report run launched with lab %v, read-only %v, no tools %v, read dirs %v, door %q", o.Lab, o.ReadOnly, o.NoTools, o.ReadDirs, o.DoorURL)
			return
		}
		s, err := mcp.NewClient(&mcp.Implementation{Name: "cli", Version: "0"}, nil).Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: o.DoorURL, HTTPClient: &http.Client{Transport: bearer(o.DoorToken)}, DisableStandaloneSSE: true}, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer s.Close()
		for tool, err := range s.Tools(t.Context(), nil) {
			if err != nil {
				t.Error(err)
				return
			}
			tools = append(tools, tool.Name)
		}
	}, nil)
	if len(calls) != 1 || f.status(t) != "completed" {
		t.Fatalf("calls %v, status %s, failure %v", calls, f.status(t), failed)
	}
	if len(tools) != 0 {
		t.Fatalf("the report run's door serves %v", tools)
	}
	for _, p := range paths {
		if strings.HasPrefix(p, "/api/role/") {
			t.Fatalf("the report run reached %s", p)
		}
	}
	var n int
	must(t, f.env.Owner.QueryRow(ctx, "SELECT count(*) FROM record WHERE agent_run_id = $1 AND kind = 'hand_in'", f.id).Scan(&n))
	if n != 1 {
		t.Fatalf("%d hand-in records, want 1", n)
	}
}
