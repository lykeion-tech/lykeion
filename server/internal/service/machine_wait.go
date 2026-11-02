package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

// HeldUntil is the retry_after of a run held for its machine: a date no
// claim ever reaches, standing for "until the machine is marked healthy or
// deleted". It is a real date because Go cannot read Postgres infinity.
var HeldUntil = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// machineWaitText is the owner's Inbox text for a machine that cannot run
// studies. For fence_breach, tool names the tool the run used.
func machineWaitText(machine, harness, reason, tool string) string {
	name := harnessName(harness)
	switch reason {
	case agent.ReasonSignIn:
		return fmt.Sprintf("%s on %s is signed out. Sign in again on that machine (run `%s login`); waiting studies continue by themselves.", name, machine, harness)
	case agent.ReasonCLIMissing:
		return fmt.Sprintf("%s is not installed on %s any more. Reinstall it; waiting studies continue by themselves.", name, machine)
	case agent.ReasonCLITooOld:
		return fmt.Sprintf("%s on %s is too old for Lykeion. Update it; waiting studies continue by themselves.", name, machine)
	case agent.ReasonSandbox:
		return fmt.Sprintf("%s could not start the sandbox for %s. Restart the Lykeion daemon there; waiting studies continue by themselves.", machine, name)
	case agent.ReasonFenceBreach:
		return fmt.Sprintf("A run on %s used %s, which is outside Lykeion's sources, so its work was discarded. Check %s's apps and plugins on that machine.", machine, tool, name)
	}
	return fmt.Sprintf("%s keeps failing to run %s (%s). Waiting studies continue once it recovers.", machine, name, reason)
}

func harnessName(harness string) string {
	switch harness {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	}
	return harness
}

// breachedTool reads the tool's name from the daemon's breach error,
// "used <tool>, which is outside Lykeion's sources".
func breachedTool(detail string) string {
	rest, ok := strings.CutPrefix(detail, "used ")
	if tool, _, found := strings.Cut(rest, ", which is outside"); ok && found && tool != "" {
		return tool
	}
	return "a tool"
}

// parkInfra requeues a study's run whose machine failed it, behind a backoff
// and without spending an attempt, so the agent and its Team never see the
// failure. run is the locked row as it was before this failure. A machine
// that fails three times running, or in a way only its owner can fix, stops
// claiming and its owner is told once per cause.
func (s *AgentRuns) parkInfra(ctx context.Context, tx *store.Tx, run db.AgentRun, reason, detail string, spent float64) error {
	if err := tx.Q.ParkAgentRun(ctx, db.ParkAgentRunParams{WorkspaceID: tx.WS, ID: run.ID, CostUsd: spent, Error: detail, FailureReason: &reason}); err != nil {
		return err
	}
	if run.RuntimeID == nil {
		return nil
	}
	streak, err := tx.Q.BumpRuntimeStreak(ctx, db.BumpRuntimeStreakParams{WorkspaceID: tx.WS, ID: *run.RuntimeID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var raise bool
	switch reason {
	case agent.ReasonSignIn, agent.ReasonCLIMissing, agent.ReasonCLITooOld, agent.ReasonSandbox:
		raise = true
	case agent.ReasonFenceBreach:
		// The first breach is retried. A second one in a row on the same run
		// holds it, so it spends nothing more until the machine is fixed.
		if raise = run.FailureReason != nil && *run.FailureReason == agent.ReasonFenceBreach; raise {
			if err := tx.Q.HoldAgentRun(ctx, db.HoldAgentRunParams{WorkspaceID: tx.WS, ID: run.ID, HeldUntil: &HeldUntil}); err != nil {
				return err
			}
		}
	default:
		raise = streak >= 3
	}
	if !raise {
		return nil
	}
	rt, err := tx.Q.GetRuntime(ctx, db.GetRuntimeParams{WorkspaceID: tx.WS, ID: *run.RuntimeID})
	if err != nil {
		return err
	}
	return flagRuntime(ctx, tx, rt, reason, detail)
}

// flagRuntime stops a runtime claiming and tells its owner, once per cause.
func flagRuntime(ctx context.Context, tx *store.Tx, rt db.Runtime, reason, detail string) error {
	if err := tx.Q.SetRuntimeUnhealthy(ctx, db.SetRuntimeUnhealthyParams{WorkspaceID: tx.WS, ID: rt.ID, Reason: &reason}); err != nil {
		return err
	}
	machine := rt.DeviceName
	if rt.CustomName != nil {
		machine = *rt.CustomName
	}
	// The machine's owner fixes it, whoever's study was waiting on it.
	return tx.Q.UpsertMachineWait(ctx, db.UpsertMachineWaitParams{WorkspaceID: tx.WS, UserID: rt.OwnerID, RuntimeID: &rt.ID, Cause: &reason,
		Text: machineWaitText(machine, rt.Harness, reason, breachedTool(detail))})
}

// ReleaseHeldRuns makes the runs held for a runtime claimable again, when the
// runtime is marked healthy or deleted.
func ReleaseHeldRuns(ctx context.Context, tx *store.Tx, runtime uuid.UUID) error {
	return tx.Q.ReleaseHeldAgentRuns(ctx, db.ReleaseHeldAgentRunsParams{WorkspaceID: tx.WS, RuntimeID: &runtime, HeldUntil: &HeldUntil})
}

// MarkUnhealthy flags the owner's runtime after a failed preflight: it stops
// claiming, and its owner is told once per cause.
func (s *AgentRuns) MarkUnhealthy(ctx context.Context, user uuid.UUID, ref RuntimeRef, reason string) error {
	return s.Store.InWorkspace(ctx, ref.WorkspaceID, user, func(tx *store.Tx) error {
		rt, err := tx.Q.GetRuntime(ctx, db.GetRuntimeParams{WorkspaceID: ref.WorkspaceID, ID: ref.RuntimeID})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && rt.OwnerID != user {
			return ErrNotYours
		}
		if err != nil {
			return err
		}
		return flagRuntime(ctx, tx, rt, reason, "")
	})
}

// MarkHealthy lets the owner's machine claim again, releases the runs it
// held, and resolves its waits.
func (s *AgentRuns) MarkHealthy(ctx context.Context, user uuid.UUID, ref RuntimeRef) error {
	return s.Store.InWorkspace(ctx, ref.WorkspaceID, user, func(tx *store.Tx) error {
		n, err := tx.Q.MarkRuntimeHealthy(ctx, db.MarkRuntimeHealthyParams{WorkspaceID: ref.WorkspaceID, ID: ref.RuntimeID, OwnerID: user})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotYours
		}
		if err := ReleaseHeldRuns(ctx, tx, ref.RuntimeID); err != nil {
			return err
		}
		return tx.Q.ResolveMachineWaits(ctx, db.ResolveMachineWaitsParams{WorkspaceID: ref.WorkspaceID, RuntimeID: &ref.RuntimeID})
	})
}
