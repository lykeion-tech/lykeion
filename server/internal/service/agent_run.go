// Package service owns the agent-run state machine: enqueue, claim, start, stream,
// settle, cancel and sweep.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/pricing"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

const (
	AgentRunPrefix  = "ltk_"
	tokenTTL        = 24 * time.Hour
	settlementLease = 5 * time.Minute
)

var (
	ErrConflict  = errors.New("the agent run is not in a state that allows this")
	ErrNotYours  = errors.New("runtime not found")
	ErrBadReport = errors.New("usage must be non-negative and failure_reason a known reason")
)

// FailureReasons are how an attempt can end badly. The first three are the
// machine's or the network's fault, so another attempt may succeed; the other
// two would fail the same way again.
var FailureReasons = []string{"runtime_offline", "timeout", "provider_network", "context_exhausted", "agent_error"}

func Retryable(reason string) bool {
	return reason == "runtime_offline" || reason == "timeout" || reason == "provider_network"
}

type RuntimeRef struct {
	// Attempt is required on run operations, and unused for registration, claim and heartbeat.
	Attempt     int32     `json:"attempt,omitempty"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	RuntimeID   uuid.UUID `json:"runtime_id"`
}

type NewAgentRun struct {
	Card                         uuid.UUID
	Column                       string
	Round                        int32
	Role, Harness, Prompt, Skill string
	MaxAttempts                  int32
}

type Claimed struct {
	AgentRun db.AgentRun       `json:"agent_run"`
	Token    string            `json:"token"`
	MCPURL   string            `json:"mcp_url"`
	MCP      []agent.MCPServer `json:"mcp,omitempty"` // tool servers besides sources
}

type Report struct {
	Usage         []agent.Usage `json:"usage"`
	SessionID     string        `json:"session_id"`
	Error         string        `json:"error"`
	FailureReason string        `json:"failure_reason"`
}

// check refuses a report that would lower spend or name an unknown reason,
// and fills in agent_error for a failure that names none.
func (r *Report) check(ok bool) error {
	if len(r.Usage) > 32 || (r.FailureReason != "" && !slices.Contains(FailureReasons, r.FailureReason)) {
		return ErrBadReport
	}
	seen := make(map[string]bool)
	total := 0.0
	for i, u := range r.Usage {
		if u.Model == "" || len(u.Model) > 200 || seen[u.Model] || u.InputTokens < 0 || u.OutputTokens < 0 || u.CacheReadTokens < 0 || u.CacheWriteTokens < 0 {
			return ErrBadReport
		}
		seen[u.Model] = true
		cost := 0.0
		if u.CostUSD != nil {
			cost = *u.CostUSD
		} else {
			cost = pricing.Cost(u.Model, u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens)
		}
		if cost < 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
			return ErrBadReport
		}
		total += cost
		if math.IsInf(total, 0) {
			return ErrBadReport
		}
		r.Usage[i].CostUSD = &cost
	}
	if ok {
		r.FailureReason = ""
	} else if r.FailureReason == "" {
		r.FailureReason = "agent_error"
	}
	return nil
}

type Message struct {
	Seq     int32           `json:"seq"`
	Type    string          `json:"type"`
	Tool    string          `json:"tool"`
	Content json.RawMessage `json:"content"`
}

type AgentRuns struct {
	Store     *store.Store
	MCPURL    string
	OnSettled func(ctx context.Context, t db.AgentRun)
	// OnSettlement takes precedence over OnSettled. Consumers must deduplicate by
	// Settlement.ID: a crash after callback effects but before acknowledgment can
	// cause redelivery. Errors leave the durable notification pending.
	OnSettlement func(context.Context, Settlement) error
	Stale        time.Duration // a runtime silent this long is offline; default 60 s
}

func agentRunEvent(t db.AgentRun) map[string]any {
	return map[string]any{"agent_run_id": t.ID, "role": t.Role, "column": t.BoardColumn, "round": t.Round, "status": t.Status, "attempt": t.Attempt}
}

// Enqueue adds an agent run in the caller's transaction. The owner is responsible
// for the run, and their id rides on the event so their daemons can be woken.
func (s *AgentRuns) Enqueue(ctx context.Context, tx *store.Tx, owner uuid.UUID, n NewAgentRun) (db.AgentRun, error) {
	if n.MaxAttempts < 0 {
		return db.AgentRun{}, ErrConflict
	}
	if n.MaxAttempts == 0 {
		n.MaxAttempts = 2
	}
	card, err := tx.Q.GetCard(ctx, db.GetCardParams{ID: n.Card, WorkspaceID: tx.WS})
	if err != nil {
		return db.AgentRun{}, err
	}
	if card.OwnerID != owner {
		return db.AgentRun{}, ErrNotYours
	}
	t, err := tx.Q.EnqueueAgentRun(ctx, db.EnqueueAgentRunParams{
		WorkspaceID: tx.WS, CardID: n.Card, ResponsibleUserID: owner, BoardColumn: n.Column, Round: n.Round, Role: n.Role,
		Harness: n.Harness, Prompt: n.Prompt, Skill: n.Skill, MaxAttempts: n.MaxAttempts,
	})
	if err != nil {
		return t, err
	}
	p := agentRunEvent(t)
	p["owner_id"] = owner
	return t, tx.Emit(ctx, "agent_run.queued", &t.CardID, store.System, p)
}

func (s *AgentRuns) Register(ctx context.Context, user, ws uuid.UUID, daemonID, device string, harnesses []string) ([]db.Runtime, error) {
	var out []db.Runtime
	err := s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		if _, err := tx.Q.GetMember(ctx, db.GetMemberParams{WorkspaceID: ws, UserID: user}); err != nil {
			return ErrNotYours
		}
		for _, h := range harnesses {
			r, err := tx.Q.UpsertRuntime(ctx, db.UpsertRuntimeParams{WorkspaceID: ws, OwnerID: user, DaemonID: daemonID, Harness: h, DeviceName: device})
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotYours // the daemon id belongs to someone else
			}
			if err != nil {
				return err
			}
			out = append(out, r)
		}
		return tx.Emit(ctx, "runtime.online", nil, store.User(user), map[string]any{"daemon_id": daemonID, "harnesses": harnesses})
	})
	return out, err
}

func (s *AgentRuns) Heartbeat(ctx context.Context, user uuid.UUID, refs []RuntimeRef) (int, error) {
	n := 0
	for _, ref := range refs {
		err := s.Store.InWorkspace(ctx, ref.WorkspaceID, user, func(tx *store.Tx) error {
			if err := owned(ctx, tx, user, ref); err != nil {
				return err
			}
			k, err := tx.Q.TouchRuntime(ctx, db.TouchRuntimeParams{ID: ref.RuntimeID, WorkspaceID: ref.WorkspaceID, OwnerID: user})
			n += int(k)
			return err
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// owned checks that the caller is a member of the runtime's workspace and owns
// the runtime.
func owned(ctx context.Context, tx *store.Tx, user uuid.UUID, ref RuntimeRef) error {
	if _, err := tx.Q.GetMember(ctx, db.GetMemberParams{WorkspaceID: ref.WorkspaceID, UserID: user}); err != nil {
		return ErrNotYours
	}
	r, err := tx.Q.GetRuntime(ctx, db.GetRuntimeParams{ID: ref.RuntimeID, WorkspaceID: ref.WorkspaceID})
	if err != nil || r.OwnerID != user {
		return ErrNotYours
	}
	return nil
}

// Claim fills up to slots agent runs across the caller's runtimes, one run per
// runtime per pass, until nothing more can be claimed.
func (s *AgentRuns) Claim(ctx context.Context, user uuid.UUID, refs []RuntimeRef, slots int) ([]Claimed, error) {
	var out []Claimed
	for slots > 0 {
		progress := false
		for _, ref := range refs {
			if slots == 0 {
				break
			}
			var c *Claimed
			err := s.Store.InWorkspace(ctx, ref.WorkspaceID, user, func(tx *store.Tx) error {
				if err := owned(ctx, tx, user, ref); err != nil {
					return err
				}
				runtime, err := tx.Q.LockRuntime(ctx, db.LockRuntimeParams{ID: ref.RuntimeID, WorkspaceID: ref.WorkspaceID})
				if err != nil {
					return err
				}
				active, err := tx.Q.CountRuntimeActive(ctx, db.CountRuntimeActiveParams{WorkspaceID: ref.WorkspaceID, RuntimeID: &ref.RuntimeID})
				if err != nil {
					return err
				}
				if active >= int64(runtime.MaxConcurrent) {
					return nil
				}
				t, err := tx.Q.ClaimAgentRun(ctx, db.ClaimAgentRunParams{RuntimeID: &ref.RuntimeID, WorkspaceID: ref.WorkspaceID})
				if errors.Is(err, pgx.ErrNoRows) {
					return nil
				}
				if err != nil {
					return err
				}
				plain, hash, _ := auth.NewToken(AgentRunPrefix)
				if err := tx.Q.InsertAgentRunToken(ctx, db.InsertAgentRunTokenParams{TokenHash: hash, WorkspaceID: ref.WorkspaceID,
					AgentRunID: t.ID, CardID: t.CardID, Role: t.Role, Attempt: t.Attempt, ExpiresAt: time.Now().Add(tokenTTL)}); err != nil {
					return err
				}
				c = &Claimed{AgentRun: t, Token: plain, MCPURL: s.MCPURL, MCP: []agent.MCPServer{}}
				return tx.Emit(ctx, "agent_run.dispatched", &t.CardID, store.System, agentRunEvent(t))
			})
			if err != nil {
				return out, err
			}
			if c != nil {
				out = append(out, *c)
				slots--
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	return out, nil
}

func (s *AgentRuns) onAgentRun(ctx context.Context, user uuid.UUID, ref RuntimeRef, fn func(tx *store.Tx) error) error {
	return s.Store.InWorkspace(ctx, ref.WorkspaceID, user, func(tx *store.Tx) error {
		if err := owned(ctx, tx, user, ref); err != nil {
			return err
		}
		return fn(tx)
	})
}

func rows(n int64, err error) error {
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

func (s *AgentRuns) PrepareLease(ctx context.Context, user uuid.UUID, ref RuntimeRef, run uuid.UUID) error {
	return s.onAgentRun(ctx, user, ref, func(tx *store.Tx) error {
		if _, err := lockAttempt(ctx, tx, ref, run); err != nil {
			return err
		}
		return rows(tx.Q.ExtendLease(ctx, db.ExtendLeaseParams{ID: run, WorkspaceID: ref.WorkspaceID, RuntimeID: &ref.RuntimeID}))
	})
}

func (s *AgentRuns) Start(ctx context.Context, user uuid.UUID, ref RuntimeRef, run uuid.UUID) error {
	return s.onAgentRun(ctx, user, ref, func(tx *store.Tx) error {
		if _, err := lockAttempt(ctx, tx, ref, run); err != nil {
			return err
		}
		if err := rows(tx.Q.StartAgentRun(ctx, db.StartAgentRunParams{ID: run, WorkspaceID: ref.WorkspaceID, RuntimeID: &ref.RuntimeID})); err != nil {
			return err
		}
		t, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{ID: run, WorkspaceID: ref.WorkspaceID})
		if err != nil {
			return err
		}
		return tx.Emit(ctx, "agent_run.started", &t.CardID, store.System, agentRunEvent(t))
	})
}

func runningOn(t db.AgentRun, ref RuntimeRef) bool {
	return t.Attempt == ref.Attempt && t.Status == "running" && t.RuntimeID != nil && *t.RuntimeID == ref.RuntimeID
}

func (s *AgentRuns) Messages(ctx context.Context, user uuid.UUID, ref RuntimeRef, run uuid.UUID, ms []Message) error {
	return s.onAgentRun(ctx, user, ref, func(tx *store.Tx) error {
		t, err := lockAttempt(ctx, tx, ref, run)
		if err != nil {
			return err
		}
		// A person may cancel before the daemon drains the final output.
		if !runningOn(t, ref) && !(t.Status == "cancelled" && t.StartedAt != nil && t.LateUsageAt == nil) {
			return ErrConflict
		}
		for _, m := range ms {
			if err := tx.Q.InsertAgentRunMessage(ctx, db.InsertAgentRunMessageParams{WorkspaceID: ref.WorkspaceID, AgentRunID: run,
				Attempt: ref.Attempt, Seq: m.Seq, Type: m.Type, Tool: m.Tool, Content: m.Content}); err != nil {
				return err
			}
		}
		return nil
	})
}

func terminal(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled"
}

// settle locks the run before consulting the immutable attempt receipt. Replays
// return that receipt, even after this runtime has claimed a newer attempt.
func (s *AgentRuns) settle(ctx context.Context, user uuid.UUID, ref RuntimeRef, run uuid.UUID, r Report, ok bool) (db.AgentRun, error) {
	if ref.Attempt <= 0 {
		return db.AgentRun{}, ErrConflict
	}
	// Work on a copy so callers can safely reuse one report concurrently.
	r.Usage = slices.Clone(r.Usage)
	if err := r.check(ok); err != nil {
		return db.AgentRun{}, err
	}
	var out db.AgentRun
	notify := false
	err := s.onAgentRun(ctx, user, ref, func(tx *store.Tx) error {
		t, err := tx.Q.LockAgentRun(ctx, db.LockAgentRunParams{ID: run, WorkspaceID: ref.WorkspaceID})
		if err != nil {
			return err
		}
		receipt, err := tx.Q.GetAttemptReceipt(ctx, db.GetAttemptReceiptParams{WorkspaceID: ref.WorkspaceID, AgentRunID: run, Attempt: ref.Attempt})
		if err == nil {
			if receipt.RuntimeID == nil || *receipt.RuntimeID != ref.RuntimeID {
				return ErrNotYours
			}
			return json.Unmarshal(receipt.Result, &out)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if t.Attempt != ref.Attempt {
			return ErrConflict
		}
		if t.RuntimeID == nil || *t.RuntimeID != ref.RuntimeID {
			return ErrNotYours
		}
		if terminal(t.Status) && (t.Status != "cancelled" || t.StartedAt == nil || t.LateUsageAt != nil) {
			out = t
			return nil
		}
		if t.Status == "dispatched" && !ok {
			if (r.FailureReason != "agent_error" && r.FailureReason != "runtime_offline") || len(r.Usage) != 0 || r.SessionID != "" {
				return ErrBadReport
			}
		} else if t.Status != "cancelled" && !runningOn(t, ref) {
			return ErrConflict
		}
		spent := 0.0
		for _, u := range r.Usage {
			cost := *u.CostUSD
			if err := tx.Q.InsertAgentRunUsage(ctx, db.InsertAgentRunUsageParams{WorkspaceID: ref.WorkspaceID, AgentRunID: run, Attempt: ref.Attempt, Model: u.Model, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens, CostUsd: cost}); err != nil {
				return err
			}
			spent += cost
		}
		if math.IsInf(t.CostUsd+spent, 0) {
			return ErrBadReport
		}
		if err := tx.Q.AddCardSpend(ctx, db.AddCardSpendParams{WorkspaceID: ref.WorkspaceID, ID: t.CardID, Usd: spent}); err != nil {
			return err
		}
		if t.Status == "cancelled" {
			if err := tx.Q.RecordLateUsage(ctx, db.RecordLateUsageParams{WorkspaceID: ref.WorkspaceID, ID: run, CostUsd: spent}); err != nil {
				return err
			}
			out, err = tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: ref.WorkspaceID, ID: run})
			if err != nil {
				return err
			}
			return saveReceipt(ctx, tx, out, t.RuntimeID)
		}
		if err := tx.Q.DeleteAgentRunTokens(ctx, db.DeleteAgentRunTokensParams{WorkspaceID: ref.WorkspaceID, AgentRunID: run}); err != nil {
			return err
		}
		switch {
		case ok:
			err = tx.Q.FinishAgentRun(ctx, db.FinishAgentRunParams{WorkspaceID: ref.WorkspaceID, ID: run, Status: "completed", CostUsd: spent, SessionID: r.SessionID})
		case Retryable(r.FailureReason) && t.Attempt < t.MaxAttempts:
			err = tx.Q.RequeueAgentRun(ctx, db.RequeueAgentRunParams{WorkspaceID: ref.WorkspaceID, ID: run, CostUsd: spent, Error: r.Error, FailureReason: &r.FailureReason})
		default:
			err = tx.Q.FinishAgentRun(ctx, db.FinishAgentRunParams{WorkspaceID: ref.WorkspaceID, ID: run, Status: "failed", CostUsd: spent, SessionID: r.SessionID, Error: r.Error, FailureReason: &r.FailureReason})
		}
		if err != nil {
			return err
		}
		out, err = tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: ref.WorkspaceID, ID: run})
		if err != nil {
			return err
		}
		if err = saveReceipt(ctx, tx, out, t.RuntimeID); err != nil {
			return err
		}
		if err = tx.Emit(ctx, "agent_run."+out.Status, &out.CardID, store.System, agentRunEvent(out)); err != nil {
			return err
		}
		if terminal(out.Status) {
			notify = true
			return saveSettlement(ctx, tx, out)
		}
		return nil
	})
	if err == nil && notify {
		s.deliverAfterCommit(ctx, out.WorkspaceID, out.ID)
	}
	return out, err
}

func lockAttempt(ctx context.Context, tx *store.Tx, ref RuntimeRef, run uuid.UUID) (db.AgentRun, error) {
	t, err := tx.Q.LockAgentRun(ctx, db.LockAgentRunParams{WorkspaceID: ref.WorkspaceID, ID: run})
	if err != nil {
		return t, err
	}
	if ref.Attempt <= 0 || t.Attempt != ref.Attempt {
		return t, ErrConflict
	}
	if t.RuntimeID == nil || *t.RuntimeID != ref.RuntimeID {
		return t, ErrNotYours
	}
	return t, nil
}

func saveReceipt(ctx context.Context, tx *store.Tx, t db.AgentRun, runtime *uuid.UUID) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return tx.Q.InsertAttemptReceipt(ctx, db.InsertAttemptReceiptParams{WorkspaceID: t.WorkspaceID, AgentRunID: t.ID, Attempt: t.Attempt, RuntimeID: runtime, Result: b})
}

func saveSettlement(ctx context.Context, tx *store.Tx, t db.AgentRun) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return tx.Q.InsertSettlement(ctx, db.InsertSettlementParams{WorkspaceID: t.WorkspaceID, AgentRunID: t.ID, Result: b})
}

func (s *AgentRuns) Complete(ctx context.Context, user uuid.UUID, ref RuntimeRef, run uuid.UUID, r Report) (db.AgentRun, error) {
	return s.settle(ctx, user, ref, run, r, true)
}

func (s *AgentRuns) Fail(ctx context.Context, user uuid.UUID, ref RuntimeRef, run uuid.UUID, r Report) (db.AgentRun, error) {
	return s.settle(ctx, user, ref, run, r, false)
}

func (s *AgentRuns) Status(ctx context.Context, user uuid.UUID, ref RuntimeRef, run uuid.UUID) (string, error) {
	var status string
	err := s.onAgentRun(ctx, user, ref, func(tx *store.Tx) error {
		t, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{ID: run, WorkspaceID: ref.WorkspaceID})
		if err != nil {
			return err
		}
		if ref.Attempt <= 0 || t.Attempt != ref.Attempt {
			return ErrConflict
		}
		if t.RuntimeID == nil || *t.RuntimeID != ref.RuntimeID {
			receipt, err := tx.Q.GetAttemptReceipt(ctx, db.GetAttemptReceiptParams{WorkspaceID: ref.WorkspaceID, AgentRunID: run, Attempt: ref.Attempt})
			if err != nil || receipt.RuntimeID == nil || *receipt.RuntimeID != ref.RuntimeID {
				return ErrNotYours
			}
		}
		status = t.Status
		return nil
	})
	return status, err
}

// Cancel is a workspace member's action. The daemon observes it through Status.
func (s *AgentRuns) Cancel(ctx context.Context, ws, user, run uuid.UUID) error {
	var t db.AgentRun
	err := s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		if _, err := tx.Q.GetMember(ctx, db.GetMemberParams{WorkspaceID: ws, UserID: user}); err != nil {
			return ErrNotYours
		}
		var err error
		t, err = tx.Q.LockAgentRun(ctx, db.LockAgentRunParams{WorkspaceID: ws, ID: run})
		if err != nil {
			return err
		}
		if terminal(t.Status) {
			return ErrConflict
		}
		if err := rows(tx.Q.CancelAgentRun(ctx, db.CancelAgentRunParams{WorkspaceID: ws, ID: run})); err != nil {
			return err
		}
		if err := tx.Q.DeleteAgentRunTokens(ctx, db.DeleteAgentRunTokensParams{WorkspaceID: ws, AgentRunID: run}); err != nil {
			return err
		}
		t, err = tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: ws, ID: run})
		if err != nil {
			return err
		}
		if err = tx.Emit(ctx, "agent_run.cancelled", &t.CardID, store.User(user), agentRunEvent(t)); err != nil {
			return err
		}
		return saveSettlement(ctx, tx, t)
	})
	if err == nil {
		s.deliverAfterCommit(ctx, ws, run)
	}
	return err
}

// Sweep commits runtime status, lost attempts, receipts, events and settlement
// notifications together. Any failure rolls the entire maintenance pass back.
func (s *AgentRuns) Sweep(ctx context.Context) error {
	stale := s.Stale
	if stale <= 0 {
		stale = 60 * time.Second
	}
	err := s.Store.Maintenance(ctx, func(tx *store.Tx) error {
		if _, err := tx.Q.SweepRuntimes(ctx, pgtype.Interval{Microseconds: stale.Microseconds(), Valid: true}); err != nil {
			return err
		}
		candidates, err := tx.Q.SweepCandidates(ctx)
		if err != nil {
			return err
		}
		var changed []db.AgentRun
		for _, candidate := range candidates {
			if err := tx.Workspace(ctx, candidate.WorkspaceID); err != nil {
				return err
			}
			t, err := tx.Q.LockAgentRun(ctx, db.LockAgentRunParams{WorkspaceID: tx.WS, ID: candidate.AgentRunID})
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			reason := ""
			if t.Status == "dispatched" && t.PrepareLeaseExpiresAt != nil && !t.PrepareLeaseExpiresAt.After(time.Now()) {
				reason = "timeout"
			}
			if t.Status == "running" {
				if t.RuntimeID == nil {
					reason = "runtime_offline"
				} else {
					r, err := tx.Q.GetRuntime(ctx, db.GetRuntimeParams{WorkspaceID: tx.WS, ID: *t.RuntimeID})
					if err != nil && !errors.Is(err, pgx.ErrNoRows) {
						return err
					}
					if errors.Is(err, pgx.ErrNoRows) || r.Status == "offline" {
						reason = "runtime_offline"
					}
				}
			}
			if reason == "" {
				continue
			}
			if t.Attempt < t.MaxAttempts {
				err = tx.Q.RequeueAgentRun(ctx, db.RequeueAgentRunParams{WorkspaceID: tx.WS, ID: t.ID, Error: t.Error, FailureReason: &reason})
			} else {
				err = tx.Q.FinishAgentRun(ctx, db.FinishAgentRunParams{WorkspaceID: tx.WS, ID: t.ID, Status: "failed", Error: "lost: the lease expired or the runtime went offline", FailureReason: &reason})
			}
			if err != nil {
				return err
			}
			if err = tx.Q.DeleteAgentRunTokens(ctx, db.DeleteAgentRunTokensParams{WorkspaceID: tx.WS, AgentRunID: t.ID}); err != nil {
				return err
			}
			out, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: tx.WS, ID: t.ID})
			if err != nil {
				return err
			}
			if err = saveReceipt(ctx, tx, out, t.RuntimeID); err != nil {
				return err
			}
			if out.Status == "failed" {
				if err = saveSettlement(ctx, tx, out); err != nil {
					return err
				}
			}
			changed = append(changed, out)
		}
		// Lock every affected run before taking any workspace event lock. Taking
		// an event lock between runs could deadlock a concurrent cancel/settle
		// that holds the next run and is waiting for that same event lock.
		for _, out := range changed {
			if err := tx.Workspace(ctx, out.WorkspaceID); err != nil {
				return err
			}
			event := "agent_run.requeued"
			if out.Status == "failed" {
				event = "agent_run.failed"
			}
			if err := tx.Emit(ctx, event, &out.CardID, store.System, agentRunEvent(out)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.RecoverSettlements(ctx)
}

// Settlement is a durable terminal-state notification. ID is the stable
// idempotency key. Delivery is at least once across failures; the consumer must
// atomically deduplicate this key alongside its own effects.
type Settlement struct {
	ID       uuid.UUID
	AgentRun db.AgentRun
}

func (s *AgentRuns) deliverAfterCommit(ctx context.Context, ws, run uuid.UUID) {
	if err := s.deliverSettlement(ctx, ws, run); err != nil {
		slog.Warn("settlement remains pending", "agent_run_id", run, "error", err)
	}
}

func (s *AgentRuns) deliverSettlement(ctx context.Context, ws, run uuid.UUID) error {
	if s.OnSettlement == nil && s.OnSettled == nil {
		return nil
	}
	deliveryID := uuid.New()
	var n db.AgentRunSettlement
	claimed := false
	err := s.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		var err error
		n, err = tx.Q.ClaimSettlement(ctx, db.ClaimSettlementParams{WorkspaceID: ws, AgentRunID: run, DeliveryID: &deliveryID, Lease: pgtype.Interval{Microseconds: settlementLease.Microseconds(), Valid: true}})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		claimed = true
		return nil
	})
	if err != nil || !claimed {
		return err
	}

	// The claim has committed and released its connection before consumer code
	// runs. Consumers may use this same pool to atomically deduplicate effects.
	// A process dying here leaves an expiring claim for a recovery worker.
	callbackCtx, cancel := context.WithDeadline(ctx, *n.LeaseExpiresAt)
	defer cancel()
	var t db.AgentRun
	consumeErr := json.Unmarshal(n.Result, &t)
	if consumeErr == nil {
		if s.OnSettlement != nil {
			consumeErr = s.OnSettlement(callbackCtx, Settlement{ID: run, AgentRun: t})
		} else {
			s.OnSettled(callbackCtx, t)
		}
		if consumeErr == nil {
			consumeErr = callbackCtx.Err()
		}
	}
	finishErr := s.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		var changed int64
		var err error
		if consumeErr != nil {
			changed, err = tx.Q.ReleaseSettlement(ctx, db.ReleaseSettlementParams{WorkspaceID: ws, AgentRunID: run, DeliveryID: &deliveryID})
		} else {
			changed, err = tx.Q.AcknowledgeSettlement(ctx, db.AcknowledgeSettlementParams{WorkspaceID: ws, AgentRunID: run, DeliveryID: &deliveryID})
		}
		if err != nil {
			return err
		}
		if changed == 0 {
			return errors.New("settlement delivery lease lost")
		}
		return nil
	})
	return errors.Join(consumeErr, finishErr)
}

// RecoverSettlements retries pending notifications after startup or a failed
// delivery. Concurrent workers skip active leases; expired leases are reclaimed.
// Consumers receive a context bounded by the five-minute lease and should honor it.
// A stale worker cannot acknowledge or release a newer worker's claim.
// Configure an idempotent consumer before calling; nil callbacks leave it pending.
func (s *AgentRuns) RecoverSettlements(ctx context.Context) error {
	if s.OnSettlement == nil && s.OnSettled == nil {
		return nil
	}
	pending, err := s.Store.Global().PendingSettlements(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, n := range pending {
		if err := s.deliverSettlement(ctx, n.WorkspaceID, n.AgentRunID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
