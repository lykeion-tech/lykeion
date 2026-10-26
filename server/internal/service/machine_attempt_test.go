package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

// Exercise real claim/start/report/requeue, not a mutable runtime fixture alone.
func TestMachineAccountingSurvivesRetryAndRuntimeDeletion(t *testing.T) {
	for _, own := range []bool{false, true} {
		name := "other shared machine"
		if own {
			name = "researcher machine"
		}
		t.Run(name, func(t *testing.T) {
			f, ownerA, a := sharedClaimFixture(t, true)
			ctx := t.Context()
			q := db.New(f.Env.Owner)
			exec := func(sql string, args ...any) {
				t.Helper()
				if _, err := f.Env.Owner.Exec(ctx, sql, args...); err != nil {
					t.Fatal(err)
				}
			}
			claim := func(owner uuid.UUID, ref RuntimeRef) Claimed {
				t.Helper()
				got, err := f.T.Claim(ctx, owner, []RuntimeRef{ref}, 1, ClaimOptions{GuestIsolation: agent.GuestIsolationProtocol})
				if err != nil || len(got) != 1 {
					t.Fatalf("claim: %+v %v", got, err)
				}
				ref.Attempt = got[0].AgentRun.Attempt
				if err := f.T.Start(ctx, owner, ref, got[0].AgentRun.ID); err != nil {
					t.Fatal(err)
				}
				return got[0]
			}
			first := claim(ownerA, a)
			id := first.AgentRun.ID
			report := Report{FailureReason: "provider_network", Usage: []agent.Usage{{Model: "model", InputTokens: 10, OutputTokens: 2, CostUSD: usd(1)}}}
			if got, err := f.T.Fail(ctx, ownerA, a, id, report); err != nil || got.Status != "queued" {
				t.Fatalf("retry: %+v %v", got, err)
			}
			check := func(owner uuid.UUID, daemon string, tokens int64, cost float64, status string) {
				t.Helper()
				totals, err := q.MachineGuestUsage(ctx, db.MachineGuestUsageParams{WorkspaceID: f.WS, OwnerID: owner, DaemonID: daemon, AsOf: time.Now().Add(time.Second)})
				if err != nil || len(totals) != 1 || totals[0].Tokens7Days != tokens || totals[0].Tokens30Days != tokens || totals[0].Cost7Days != cost || totals[0].Cost30Days != cost {
					t.Fatalf("%s totals: %+v %v", daemon, totals, err)
				}
				runs, err := q.MachineGuestRuns(ctx, db.MachineGuestRunsParams{WorkspaceID: f.WS, OwnerID: owner, DaemonID: daemon})
				if err != nil || len(runs) != 1 || runs[0].RunID != id || runs[0].CostUsd != cost || runs[0].InputTokens+runs[0].OutputTokens != tokens || runs[0].Status != status {
					t.Fatalf("%s runs: %+v %v", daemon, runs, err)
				}
			}
			check(ownerA, "shared", 12, 1, "queued")
			ownerC := ownerA
			if own {
				ownerC = f.Owner
			} else {
				u, err := f.T.Store.Global().UpsertUserByEmail(ctx, "machine-c@lab.org")
				if err != nil {
					t.Fatal(err)
				}
				ownerC = u.ID
				exec(`INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member')`, f.WS, ownerC)
			}
			c := f.runtime(t, ownerC, "machine-c", "codex")
			exec(`UPDATE runtime SET visibility='workspace' WHERE id=$1`, c.RuntimeID)
			exec(`UPDATE card SET machine='machine-c' WHERE id=$1`, f.Card)
			second := claim(ownerC, c)
			c.Attempt = second.AgentRun.Attempt
			if c.Attempt != 2 {
				t.Fatalf("attempt=%d", c.Attempt)
			}
			if _, err := f.T.Complete(ctx, ownerC, c, id, Report{Usage: []agent.Usage{{Model: "model", InputTokens: 20, OutputTokens: 3, CostUSD: usd(2)}}}); err != nil {
				t.Fatal(err)
			}
			// A replay from the prior machine must return its receipt and add no charge.
			if got, err := f.T.Fail(ctx, ownerA, a, id, report); err != nil || got.Status != "queued" {
				t.Fatalf("replay: %+v %v", got, err)
			}
			check(ownerA, "shared", 12, 1, "completed")
			if !own {
				check(ownerC, "machine-c", 23, 2, "completed")
			} else {
				rows, err := q.MachineGuestRuns(ctx, db.MachineGuestRunsParams{WorkspaceID: f.WS, OwnerID: ownerC, DaemonID: "machine-c"})
				if err != nil || len(rows) != 0 {
					t.Fatalf("own run shown as guest: %+v %v", rows, err)
				}
			}
			// Delayed usage ingestion for an earlier accepted attempt must stay on A.
			exec(`INSERT INTO agent_run_usage(workspace_id,agent_run_id,attempt,model,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,cost_usd) VALUES($1,$2,1,'late',5,1,0,0,0.5)`, f.WS, id)
			check(ownerA, "shared", 18, 1.5, "completed")
			if !own {
				check(ownerC, "machine-c", 23, 2, "completed")
			}
			f.runtime(t, ownerA, "shared", "claude") // another owned CLI keeps endpoint authorization
			exec(`DELETE FROM runtime WHERE id=$1`, a.RuntimeID)
			check(ownerA, "shared", 18, 1.5, "completed")
			again := f.runtime(t, ownerA, "shared", "codex")
			if again.RuntimeID == a.RuntimeID {
				t.Fatal("runtime UUID reused")
			}
			check(ownerA, "shared", 18, 1.5, "completed")
		})
	}
}

func TestSharedMachineLateCancelledReportIsAttributedOnce(t *testing.T) {
	f, owner, ref := sharedClaimFixture(t, true)
	ctx := t.Context()
	claims, err := f.T.Claim(ctx, owner, []RuntimeRef{ref}, 1, ClaimOptions{GuestIsolation: agent.GuestIsolationProtocol})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim: %+v %v", claims, err)
	}
	id := claims[0].AgentRun.ID
	if err = f.T.Start(ctx, owner, ref, id); err != nil {
		t.Fatal(err)
	}
	if err = f.T.Cancel(ctx, f.WS, f.Owner, id); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = f.T.Fail(ctx, owner, ref, id, Report{FailureReason: "runtime_offline", Usage: []agent.Usage{{Model: "late", InputTokens: 7, CostUSD: usd(0.5)}}}); err != nil {
			t.Fatal(err)
		}
	}
	totals, err := db.New(f.Env.Owner).MachineGuestUsage(ctx, db.MachineGuestUsageParams{WorkspaceID: f.WS, OwnerID: owner, DaemonID: "shared", AsOf: time.Now().Add(time.Second)})
	if err != nil || len(totals) != 1 || totals[0].Tokens7Days != 7 || totals[0].Cost7Days != 0.5 {
		t.Fatalf("late usage: %+v %v", totals, err)
	}
}
