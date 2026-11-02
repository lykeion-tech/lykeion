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

// parkLimit is how many times a study's run is parked for its machines'
// sake before it is held for its owner to decide on.
const parkLimit = 5

// failureWords names each infrastructure cause as the end of "last because …".
var failureWords = map[string]string{
	agent.ReasonRuntimeOffline:  "its machine went offline",
	"timeout":                   "it ran out of time",
	agent.ReasonProviderNetwork: "the agent's provider could not be reached",
	agent.ReasonSignIn:          "the agent's CLI was signed out",
	agent.ReasonCLIMissing:      "the agent's CLI was not installed",
	agent.ReasonCLITooOld:       "the agent's CLI was too old",
	agent.ReasonDoor:            "the agent's CLI could not reach Lykeion's tools",
	agent.ReasonCLIBroken:       "the agent's CLI failed to start or answer",
	agent.ReasonSandbox:         "the sandbox could not start",
	agent.ReasonFenceBreach:     "it used a tool outside Lykeion's sources",
}

// holdForOwner holds a study's run parked parkLimit times and asks the
// study's owner, once per study, to decide on it; resolving that item
// releases the run (ReleaseParkedRuns).
func holdForOwner(ctx context.Context, tx *store.Tx, run db.AgentRun, reason, detail string) error {
	if err := tx.Q.HoldAgentRun(ctx, db.HoldAgentRunParams{WorkspaceID: tx.WS, ID: run.ID, HeldUntil: &HeldUntil}); err != nil {
		return err
	}
	c, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: tx.WS, ID: *run.CardID})
	if err != nil {
		return err
	}
	name := c.Title
	if name == "" {
		name = c.Question
	}
	if r := []rune(detail); len(r) > 300 {
		detail = string(r[:300]) + "…"
	}
	text := fmt.Sprintf("Study #%d “%s” has stopped trying: its run failed %d times for reasons outside the agent's work, last because %s", c.Number, name, parkLimit, failureWords[reason])
	if detail != "" {
		text += " (" + detail + ")"
	}
	text += ". Resolve this item to try again."
	if err := tx.Q.UpsertParkedRunDecision(ctx, db.UpsertParkedRunDecisionParams{WorkspaceID: tx.WS, CardID: c.ID, UserID: c.OwnerID, Text: text}); err != nil {
		return err
	}
	return tx.Emit(ctx, "inbox.created", &c.ID, store.System, map[string]any{"card_id": c.ID})
}

// ReleaseParkedRuns lets a study's runs held after too many parks try again,
// from a fresh count, once their owner resolved the item that asked.
func ReleaseParkedRuns(ctx context.Context, tx *store.Tx, card uuid.UUID) error {
	return tx.Q.ReleaseParkedAgentRuns(ctx, db.ReleaseParkedAgentRunsParams{WorkspaceID: tx.WS, CardID: card, ParkLimit: parkLimit})
}

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
	case agent.ReasonDoor:
		return fmt.Sprintf("%s on %s cannot reach Lykeion's tools. Waiting studies continue once it recovers.", name, machine)
	case agent.ReasonCLIBroken:
		return fmt.Sprintf("%s on %s keeps failing to start or answer. Waiting studies continue once it recovers.", name, machine)
	case agent.ReasonProviderNetwork:
		return fmt.Sprintf("%s on %s cannot reach its provider. Waiting studies continue once the connection is back.", name, machine)
	case "timeout":
		return fmt.Sprintf("Runs of %s on %s keep running out of time. Waiting studies continue once it recovers.", name, machine)
	case agent.ReasonRuntimeOffline:
		return fmt.Sprintf("%s is offline. Waiting studies continue once it is back online.", machine)
	}
	return fmt.Sprintf("%s keeps failing to run %s. Waiting studies continue once it recovers.", machine, name)
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
	if run.InfraFailures+1 >= parkLimit {
		if err := holdForOwner(ctx, tx, run, reason, detail); err != nil {
			return err
		}
	}
	// A machine that went offline already shows as offline, so a lost
	// attempt neither counts toward its streak nor flags it.
	if run.RuntimeID == nil || reason == agent.ReasonRuntimeOffline {
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
	if err := tx.Q.UpsertMachineWait(ctx, db.UpsertMachineWaitParams{WorkspaceID: tx.WS, UserID: rt.OwnerID, RuntimeID: &rt.ID, Cause: &reason,
		Text: machineWaitText(machine, rt.Harness, reason, breachedTool(detail))}); err != nil {
		return err
	}
	return runtimeHealthChanged(ctx, tx, rt.ID)
}

// runtimeHealthChanged tells open pages that a machine started or stopped
// taking studies, so the studies waiting on it say so.
func runtimeHealthChanged(ctx context.Context, tx *store.Tx, runtime uuid.UUID) error {
	return tx.Emit(ctx, "runtime.updated", nil, store.System, map[string]any{"runtime_id": runtime, "action": "health"})
}

// ReleaseHeldRuns makes the runs held for a runtime claimable again, when the
// runtime is marked healthy or deleted.
func ReleaseHeldRuns(ctx context.Context, tx *store.Tx, runtime uuid.UUID) error {
	return tx.Q.ReleaseHeldAgentRuns(ctx, db.ReleaseHeldAgentRunsParams{WorkspaceID: tx.WS, RuntimeID: &runtime, HeldUntil: &HeldUntil, ParkLimit: parkLimit})
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
// held, and resolves its waits, a fence breach's among them.
func (s *AgentRuns) MarkHealthy(ctx context.Context, user uuid.UUID, ref RuntimeRef) error {
	return s.Store.InWorkspace(ctx, ref.WorkspaceID, user, func(tx *store.Tx) error {
		return ClearRuntime(ctx, tx, user, ref.RuntimeID)
	})
}

// ClearRuntime lets the owner's runtime claim again, whatever flagged it, a
// fence breach included: it clears the flag and streak, releases the runs
// held for it and resolves all its waits, inside the caller's transaction.
// ErrNotYours means the runtime is not the user's.
func ClearRuntime(ctx context.Context, tx *store.Tx, user, runtime uuid.UUID) error {
	n, err := tx.Q.MarkRuntimeHealthy(ctx, db.MarkRuntimeHealthyParams{WorkspaceID: tx.WS, ID: runtime, OwnerID: user})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotYours
	}
	if err := ReleaseHeldRuns(ctx, tx, runtime); err != nil {
		return err
	}
	if err := tx.Q.ResolveMachineWaits(ctx, db.ResolveMachineWaitsParams{WorkspaceID: tx.WS, RuntimeID: &runtime}); err != nil {
		return err
	}
	return runtimeHealthChanged(ctx, tx, runtime)
}

// MarkPassed takes a preflight pass while the daemon runs. It clears the
// machine as MarkHealthy does, except for a fence breach: the hello can't
// see what the run reached for, so a breach stays flagged, its runs held and
// its wait open until the owner resolves the wait or the daemon restarts.
func (s *AgentRuns) MarkPassed(ctx context.Context, user uuid.UUID, ref RuntimeRef) error {
	return s.Store.InWorkspace(ctx, ref.WorkspaceID, user, func(tx *store.Tx) error {
		rt, err := tx.Q.GetRuntime(ctx, db.GetRuntimeParams{WorkspaceID: ref.WorkspaceID, ID: ref.RuntimeID})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && rt.OwnerID != user {
			return ErrNotYours
		}
		if err != nil {
			return err
		}
		if rt.UnhealthyReason != nil && *rt.UnhealthyReason == agent.ReasonFenceBreach {
			return nil
		}
		if _, err := tx.Q.MarkRuntimeHealthy(ctx, db.MarkRuntimeHealthyParams{WorkspaceID: ref.WorkspaceID, ID: ref.RuntimeID, OwnerID: user}); err != nil {
			return err
		}
		if err := tx.Q.ResolveMachineWaitsExcept(ctx, db.ResolveMachineWaitsExceptParams{WorkspaceID: ref.WorkspaceID, RuntimeID: &ref.RuntimeID, Keep: agent.ReasonFenceBreach}); err != nil {
			return err
		}
		// Every hello passes; only one that clears a flag changes anything.
		if rt.UnhealthyReason == nil {
			return nil
		}
		return runtimeHealthChanged(ctx, tx, ref.RuntimeID)
	})
}
