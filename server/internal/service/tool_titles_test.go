package service

import (
	"context"
	"encoding/json"
	"errors"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
	"testing"
)

func TestToolTitlesMustNameAnExistingCallInThisAttempt(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.enqueue(t, 1, "claude")
	ref, id := f.claimStart(t, f.runtime(t, f.Owner, "titles", "claude"))
	call := Message{Seq: 1, Type: "tool_use", Tool: "Bash", Content: json.RawMessage(`{"id":"a","input":{"command":"ls"}}`)}
	if err := f.T.Messages(ctx, f.Owner, ref, id, []Message{call}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		seq int
		id  string
	}{{99, "a"}, {1, "wrong"}} {
		data, _ := json.Marshal(agent.StepTitles{Type: "tool_step_titles", Version: 1, Titles: []agent.StepTitle{{Seq: int32(target.seq), ToolUseID: target.id, Title: "Read the dataset"}}})
		if err := f.T.Messages(ctx, f.Owner, ref, id, []Message{{Seq: 2, Type: "status", Content: data}}); !errors.Is(err, ErrBadReport) {
			t.Fatalf("accepted invalid target %+v: %v", target, err)
		}
	}
	data := json.RawMessage(`{"type":"tool_step_titles","version":1,"titles":[{"seq":1,"tool_use_id":"a","title":"Read the dataset"}]}`)
	for range 2 {
		if err := f.T.Messages(ctx, f.Owner, ref, id, []Message{{Seq: 2, Type: "status", Content: data}}); err != nil {
			t.Fatal(err)
		}
	}
	err := f.T.Store.InWorkspace(ctx, f.WS, f.Owner, func(tx *store.Tx) error {
		got, err := tx.Q.AgentRunMessagesAfter(ctx, db.AgentRunMessagesAfterParams{WorkspaceID: f.WS, AgentRunID: id, Attempt: 1})
		if err == nil && (len(got) != 2 || string(got[0].Content) != `{"id": "a", "input": {"command": "ls"}}`) {
			t.Fatalf("lost original or replay duplicated metadata: %+v", got)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	other := ref
	other.Attempt++
	if err := f.T.Messages(ctx, f.Owner, other, id, []Message{{Seq: 2, Type: "status", Content: data}}); err == nil {
		t.Fatal("accepted another attempt")
	}
}
func TestNamingUsagePreservesActualAndEstimatedCosts(t *testing.T) {
	r := Report{Usage: []agent.Usage{{Model: "gpt-5.6-sol", InputTokens: 10}}, NamingUsage: []agent.Usage{{Model: "gpt-5.6-sol", InputTokens: 2, CostUSD: usd(.02)}}}
	if err := r.check(true); err != nil {
		t.Fatal(err)
	}
	if len(r.Usage) != 1 || r.Usage[0].InputTokens != 12 || r.Usage[0].CostUSD == nil || *r.Usage[0].CostUSD < .020039 || *r.Usage[0].CostUSD > .020041 || r.Usage[0].CostReported {
		t.Fatalf("misreported combined usage: %+v", r.Usage)
	}
}
