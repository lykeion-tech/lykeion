package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

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
			if err := tx.Q.HoldAgentRun(ctx, db.HoldAgentRunParams{WorkspaceID: tx.WS, ID: run.ID}); err != nil {
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
		if err := tx.Q.ReleaseHeldAgentRuns(ctx, db.ReleaseHeldAgentRunsParams{WorkspaceID: ref.WorkspaceID, RuntimeID: &ref.RuntimeID}); err != nil {
			return err
		}
		return tx.Q.ResolveMachineWaits(ctx, db.ResolveMachineWaitsParams{WorkspaceID: ref.WorkspaceID, RuntimeID: &ref.RuntimeID})
	})
}
