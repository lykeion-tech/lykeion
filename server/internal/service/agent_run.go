// Package service owns the agent-run state machine: enqueue, claim, start, stream,
// settle, cancel and sweep.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"lykeion/server/internal/addon"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/availability"
	"lykeion/server/internal/connectors"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/pricing"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/toolroute"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

// Token scopes. A role run's token acts as its role on its card. An
// autoresearch agent run's token may only post its run's report and open
// cards.
const (
	ScopeCoresearch = "coresearch:chat"
	ScopeRole       = "card:role"
	ScopeReport     = "report:write"
	ScopeCardCreate = "card:create"
	// ScopeCall records a connector call. Only the daemon's tool door holds a
	// token with it; the agent's own token never does.
	ScopeCall = "call:record"
)

// Scopes are the powers an agent run's token carries, by the agent run's kind.
func Scopes(kind string) []string {
	if kind == "coresearch" {
		return []string{ScopeCoresearch}
	}
	if kind == "autoresearch" {
		return []string{ScopeReport, ScopeCardCreate}
	}
	return []string{ScopeRole}
}

// AutoresearchAgentRun is an Autoresearch's run_only agent run. It has no card, and only its
// requester's runtimes claim it.
type AutoresearchAgentRun struct {
	Harness, Model, Prompt, Skill string
}

// EnqueueAutoresearch adds an autoresearch agent run in the caller's transaction. The
// requester is responsible for it and pays for it, and their id rides on the
// event so their daemons wake up.
func (s *AgentRuns) EnqueueAutoresearch(ctx context.Context, tx *store.Tx, requester uuid.UUID, n AutoresearchAgentRun) (db.AgentRun, error) {
	if _, err := tx.Q.LockWorkspaceExecution(ctx, tx.WS); err != nil {
		return db.AgentRun{}, err
	}
	t, err := tx.Q.EnqueueAutoresearchAgentRun(ctx, db.EnqueueAutoresearchAgentRunParams{WorkspaceID: tx.WS, RequesterID: requester,
		Harness: n.Harness, Prompt: n.Prompt, Skill: n.Skill, RequestedModel: n.Model})
	if err != nil {
		return t, err
	}
	p := agentRunEvent(t)
	p["owner_id"] = requester
	return t, tx.Emit(ctx, "agent_run.queued", nil, store.System, p)
}

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
// reasons do not retry. Runtime access denial is recorded only by the server.
var FailureReasons = []string{"runtime_offline", "timeout", "provider_network", "context_exhausted", "agent_error", "runtime_access_denied", "coresearch_access_revoked"}

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
	// Model is the agent's model; empty runs the harness default.
	Model       string
	MaxAttempts int32
	// Agent is the agent the run is started as, when there is one.
	Agent *uuid.UUID
	// Assignment is what the run was asked to do, as researchers read it:
	// the prompt without Lykeion's own framing.
	Assignment string
	// ContinuityScope is computed by internal callers before any terminal snapshot.
	ContinuityScope string `json:"-"`
}

type WorkspaceConnectorClaim struct {
	ID        uuid.UUID         `json:"id"`
	Name      string            `json:"name"`
	Transport string            `json:"transport"`
	Command   []string          `json:"command,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

type ClaimedCoresearch struct {
	ChatID        uuid.UUID `json:"chat_id"`
	ProjectID     uuid.UUID `json:"project_id"`
	PythonVersion string    `json:"python_version"`
}

type Claimed struct {
	Coresearch    *ClaimedCoresearch `json:"coresearch,omitempty"`
	PythonVersion string             `json:"python_version,omitempty"`
	AgentRun      db.AgentRun        `json:"agent_run"`
	Env           map[string]string  `json:"env"`
	Token         string             `json:"token"`
	// CallToken records the run's connector calls. The daemon keeps it from
	// the agent, so only the door can write a call record.
	CallToken string            `json:"call_token"`
	MCPURL    string            `json:"mcp_url"`
	MCP       []agent.MCPServer `json:"mcp,omitempty"` // tool servers besides sources
	// Connectors are what the run may call through the daemon's tool door.
	Connectors          []pack.Connector          `json:"connectors"`
	WorkspaceConnectors []WorkspaceConnectorClaim `json:"workspace_connectors,omitempty"`
	// Attachments are the study's files, which the daemon places in the run
	// folder before the CLI starts.
	Attachments []ClaimedAttachment `json:"attachments,omitempty"`
	Services    []ClaimedService    `json:"services,omitempty"`
	// Skills are a Team member's own skills, which the daemon places where
	// the CLI reads skills before it starts.
	Skills []toolroute.RoutedSkill `json:"skills,omitempty"`
}

type ClaimedService struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Start     string    `json:"start"`
	Client    string    `json:"client"`
	Env       []string  `json:"env"`
	Sha256    string    `json:"sha256"`
	SizeBytes int64     `json:"size_bytes"`
}

// ClaimedAttachment is an attached file as the daemon fetches it: by id,
// under its sanitised name, checked against its digest and size.
type ClaimedAttachment struct {
	ID            uuid.UUID `json:"id"`
	DiskName      string    `json:"disk_name"`
	Sha256        string    `json:"sha256"`
	SizeBytes     int64     `json:"size_bytes"`
	UnpackedBytes int64     `json:"unpacked_bytes"`
	InnerFiles    int32     `json:"inner_files"`
}

type Report struct {
	Usage         []agent.Usage `json:"usage"`
	NamingUsage   []agent.Usage `json:"naming_usage,omitempty"`
	SessionID     string        `json:"session_id"`
	Error         string        `json:"error"`
	FailureReason string        `json:"failure_reason"`
	// Mode and PolicyDigest record how the daemon ran the attempt.
	Mode         string `json:"mode"`
	PolicyDigest string `json:"policy_digest"`
	// Model is the model the attempt ran on; pricing and review use it.
	Model string `json:"model"`
}

// check refuses a report that would lower spend or name an unknown reason,
// and fills in agent_error for a failure that names none.
func (r *Report) check(ok bool) error {
	if len(r.Usage) > 32 || len(r.NamingUsage) > 64 || (r.FailureReason != "" && (!slices.Contains(FailureReasons, r.FailureReason) || (r.FailureReason == "runtime_access_denied" || r.FailureReason == "coresearch_access_revoked"))) ||
		!slices.Contains([]string{"", "workspace_continuity", "private", "sandboxed"}, r.Mode) || len(r.PolicyDigest) > 128 || len(r.Model) > 200 {
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
		r.Usage[i].CostReported = u.CostUSD != nil
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
	for _, named := range r.NamingUsage {
		single := Report{Usage: []agent.Usage{named}}
		if err := single.check(ok); err != nil {
			return err
		}
		addition := single.Usage[0]
		total += *addition.CostUSD
		if math.IsInf(total, 0) {
			return ErrBadReport
		}
		found := false
		for i := range r.Usage {
			existing := &r.Usage[i]
			if existing.Model != addition.Model {
				continue
			}
			for _, pair := range [][2]int64{{existing.InputTokens, addition.InputTokens}, {existing.OutputTokens, addition.OutputTokens}, {existing.CacheReadTokens, addition.CacheReadTokens}, {existing.CacheWriteTokens, addition.CacheWriteTokens}} {
				if pair[1] > math.MaxInt64-pair[0] {
					return ErrBadReport
				}
			}
			existing.InputTokens += addition.InputTokens
			existing.OutputTokens += addition.OutputTokens
			existing.CacheReadTokens += addition.CacheReadTokens
			existing.CacheWriteTokens += addition.CacheWriteTokens
			cost := *existing.CostUSD + *addition.CostUSD
			if math.IsInf(cost, 0) {
				return ErrBadReport
			}
			existing.CostUSD = &cost
			existing.CostReported = existing.CostReported && addition.CostReported
			found = true
			break
		}
		if !found {
			r.Usage = append(r.Usage, addition)
		}
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
	Keys      *connectors.Client
	Store     *store.Store
	Blobs     storage.Storage // holds the Team packs a claimed run's connectors come from
	MCPURL    string
	OnSettled func(ctx context.Context, t db.AgentRun)
	// OnSettlement takes precedence over OnSettled. Consumers must deduplicate by
	// Settlement.ID: a crash after callback effects but before acknowledgment can
	// cause redelivery. Errors leave the durable notification pending.
	OnSettlement func(context.Context, Settlement) error
	// OnAttemptEnded releases transient resources after a committed attempt end.
	// Replayed receipts do not invoke it.
	OnAttemptEnded func(workspace, run uuid.UUID, attempt int32)
	afterClaim     func(context.Context, *store.Tx, db.AgentRun) error
	Stale          time.Duration // a runtime silent this long is offline; default 60 s
}

func agentRunEvent(t db.AgentRun) map[string]any {
	return map[string]any{"agent_run_id": t.ID, "role": t.Role, "column": t.BoardColumn, "round": t.Round, "status": t.Status, "attempt": t.Attempt}
}

// Enqueue adds an agent run in the caller's transaction. The owner is responsible
// for the run, and their id rides on the event so their daemons can be woken.
func (s *AgentRuns) Enqueue(ctx context.Context, tx *store.Tx, owner uuid.UUID, n NewAgentRun) (db.AgentRun, error) {
	if _, err := tx.Q.LockWorkspaceExecution(ctx, tx.WS); err != nil {
		return db.AgentRun{}, err
	}
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
	scope := n.ContinuityScope
	if scope == "" {
		scope = "card:" + n.Card.String() + "/role:" + n.Role
	}
	t, err := tx.Q.EnqueueAgentRun(ctx, db.EnqueueAgentRunParams{
		WorkspaceID: tx.WS, CardID: &n.Card, ResponsibleUserID: owner, BoardColumn: n.Column, Round: n.Round, Role: n.Role,
		Harness: n.Harness, Prompt: n.Prompt, Skill: n.Skill, MaxAttempts: n.MaxAttempts,
		ContinuityScope: scope, RequestedModel: n.Model,
		AgentID: n.Agent, Assignment: n.Assignment,
	})
	if err != nil {
		return t, err
	}
	p := agentRunEvent(t)
	p["owner_id"] = owner
	if err := tx.Emit(ctx, "agent_run.queued", t.CardID, store.System, p); err != nil {
		return t, err
	}
	if t.Harness == "cursor" || t.Harness == "antigravity" {
		return failRemovedHarness(ctx, tx, t)
	}
	return t, nil
}

// failRemovedHarness ends queued work without inventing a dispatch or an
// attempt receipt. Its durable settlement is delivered after the caller commits.
func failRemovedHarness(ctx context.Context, tx *store.Tx, t db.AgentRun) (db.AgentRun, error) {
	reason := "agent_error"
	if err := tx.Q.FinishAgentRun(ctx, db.FinishAgentRunParams{WorkspaceID: tx.WS, ID: t.ID, Status: "failed", SessionID: t.SessionID, Error: agent.ErrHarnessRemoved.Error(), FailureReason: &reason}); err != nil {
		return t, err
	}
	f, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: tx.WS, ID: t.ID})
	if err != nil {
		return f, err
	}
	if err := saveSettlement(ctx, tx, f); err != nil {
		return f, err
	}
	return f, tx.Emit(ctx, "agent_run.failed", f.CardID, store.System, agentRunEvent(f))
}

// Register brings the daemon's runtimes online. A missing version or platform
// field stores an empty string.
func (s *AgentRuns) Register(ctx context.Context, user, ws uuid.UUID, daemonID, device string, harnesses []string, versions map[string]string, platform agent.Platform) ([]db.Runtime, error) {
	platform = platform.Clean()
	var out []db.Runtime
	err := s.Store.InWorkspace(ctx, ws, user, func(tx *store.Tx) error {
		// Registration may update an existing runtime before emitting its event.
		// Match workspace mutations so the event's FK check cannot invert locks.
		if _, err := tx.Q.LockWorkspaceExecution(ctx, ws); err != nil {
			return err
		}
		if _, err := tx.Q.GetMember(ctx, db.GetMemberParams{WorkspaceID: ws, UserID: user}); err != nil {
			return ErrNotYours
		}
		for _, h := range harnesses {
			r, err := tx.Q.UpsertRuntime(ctx, db.UpsertRuntimeParams{WorkspaceID: ws, OwnerID: user, DaemonID: daemonID, Harness: h, DeviceName: device, CliVersion: agent.VersionLine(versions[h]),
				Os: platform.OS, Arch: platform.Arch, Kernel: platform.Kernel, Language: platform.Language})
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

// Runtime batches commit atomically: a later invalid reference cannot hide an
// earlier dispatch token or a partially acknowledged heartbeat.
func (s *AgentRuns) runtimeBatch(ctx context.Context, user uuid.UUID, refs []RuntimeRef, fn func(*store.Tx, []RuntimeRef) error) error {
	refs = append([]RuntimeRef(nil), refs...)
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].WorkspaceID != refs[j].WorkspaceID {
			return refs[i].WorkspaceID.String() < refs[j].WorkspaceID.String()
		}
		return refs[i].RuntimeID.String() < refs[j].RuntimeID.String()
	})
	return s.Store.AsUser(ctx, user, "", func(tx *store.Tx) error {
		// Token/event FK checks need workspace row locks. Take these before
		// runtime and event locks, matching normal workspace mutations.
		for _, ref := range refs {
			if err := tx.Workspace(ctx, ref.WorkspaceID); err != nil {
				return err
			}
			if _, err := tx.Q.LockWorkspaceExecution(ctx, ref.WorkspaceID); err != nil {
				return err
			}
		}
		for _, ref := range refs {
			if err := tx.Workspace(ctx, ref.WorkspaceID); err != nil {
				return err
			}
			if err := owned(ctx, tx, user, ref); err != nil {
				return err
			}
			if _, err := tx.Q.LockRuntime(ctx, db.LockRuntimeParams{ID: ref.RuntimeID, WorkspaceID: ref.WorkspaceID}); err != nil {
				return err
			}
		}
		return fn(tx, refs)
	})
}

// Heartbeat marks the runtimes online. A non-nil bases replaces each one's
// language base states, and each workspace whose states changed gets one
// runtime.updated event.
func (s *AgentRuns) Heartbeat(ctx context.Context, user uuid.UUID, refs []RuntimeRef, bases map[string]string) (int, error) {
	var states json.RawMessage
	if bases != nil {
		var err error
		if states, err = json.Marshal(bases); err != nil {
			return 0, err
		}
	}
	n := 0
	err := s.runtimeBatch(ctx, user, refs, func(tx *store.Tx, refs []RuntimeRef) error {
		changed := map[uuid.UUID]bool{}
		for _, ref := range refs {
			if err := tx.Workspace(ctx, ref.WorkspaceID); err != nil {
				return err
			}
			k, err := tx.Q.TouchRuntime(ctx, db.TouchRuntimeParams{ID: ref.RuntimeID, WorkspaceID: ref.WorkspaceID, OwnerID: user})
			if err != nil {
				return err
			}
			n += int(k)
			if states == nil {
				continue
			}
			k, err = tx.Q.SetRuntimeBases(ctx, db.SetRuntimeBasesParams{Bases: states, ID: ref.RuntimeID, WorkspaceID: ref.WorkspaceID, OwnerID: user})
			if err != nil {
				return err
			}
			if k > 0 && !changed[ref.WorkspaceID] {
				changed[ref.WorkspaceID] = true
				// refs are in workspace order, so event locks are taken in
				// the same order as every other batch.
				if err := tx.Emit(ctx, "runtime.updated", nil, store.User(user), map[string]any{"action": "bases"}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
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

type ClaimOptions struct {
	GuestIsolation string
}

// Claim fills up to slots agent runs across the caller's runtimes, one run per
// runtime per pass, until nothing more can be claimed. An agent run's add-on
// servers were checked when the owner saved them and when it was queued; they
// are checked once more before they reach a daemon, and an agent run that fails
// is settled failed instead of handed out.
func (s *AgentRuns) Claim(ctx context.Context, user uuid.UUID, refs []RuntimeRef, slots int, options ...ClaimOptions) ([]Claimed, error) {
	guestIsolation := len(options) > 0 && options[0].GuestIsolation == agent.GuestIsolationProtocol
	var out []Claimed
	var refused []db.AgentRun
	teams := map[uuid.UUID]TeamClaim{}
	dispatchRefs := refs
	err := s.runtimeBatch(ctx, user, refs, func(tx *store.Tx, ordered []RuntimeRef) error {
		// Event locks also span workspaces; acquire them in canonical order,
		// then preserve the caller's scheduling order for dispatch.
		for _, ref := range ordered {
			if err := tx.Q.LockWorkspaceEvents(ctx, ref.WorkspaceID); err != nil {
				return err
			}
		}
		// Upgraded daemons advertise only supported harnesses. Reject old queued
		// work before matching a runtime, under the same owner and workspace scope.
		seen := map[uuid.UUID]bool{}
		for _, ref := range ordered {
			if seen[ref.WorkspaceID] {
				continue
			}
			seen[ref.WorkspaceID] = true
			if err := tx.Workspace(ctx, ref.WorkspaceID); err != nil {
				return err
			}
			removed, err := tx.Q.LockRemovedHarnessQueue(ctx, db.LockRemovedHarnessQueueParams{WorkspaceID: ref.WorkspaceID, OwnerID: user})
			if err != nil {
				return err
			}
			for _, t := range removed {
				f, err := failRemovedHarness(ctx, tx, t)
				if err != nil {
					return err
				}
				refused = append(refused, f)
			}
		}
		for slots > 0 {
			progress := false
			for _, ref := range dispatchRefs {
				if slots == 0 {
					break
				}
				var c *Claimed
				err := func() error {
					if err := tx.Workspace(ctx, ref.WorkspaceID); err != nil {
						return err
					}
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
					claim, err := tx.Q.ClaimAgentRun(ctx, db.ClaimAgentRunParams{RuntimeID: &ref.RuntimeID, WorkspaceID: ref.WorkspaceID, GuestIsolation: guestIsolation})
					if errors.Is(err, pgx.ErrNoRows) {
						return nil
					}
					if err != nil {
						return err
					}
					t, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: ref.WorkspaceID, ID: claim.ID})
					if err != nil {
						return err
					}
					if s.afterClaim != nil {
						if err := s.afterClaim(ctx, tx, t); err != nil {
							return err
						}
					}
					current, err := tx.Q.GetRuntime(ctx, db.GetRuntimeParams{WorkspaceID: ref.WorkspaceID, ID: ref.RuntimeID})
					if err != nil {
						return err
					}
					denied := current.OwnerID != t.ResponsibleUserID && (!guestIsolation || current.Os != "linux")
					if t.Kind == "autoresearch" {
						denied = t.RequesterID == nil || *t.RequesterID != current.OwnerID || t.ResponsibleUserID != current.OwnerID
					}
					var accessErr error
					var core *ClaimedCoresearch
					if t.Kind == "coresearch" {
						denied = true
						if t.CoresearchChatID != nil {
							chat, e := tx.Q.LockCoresearchChat(ctx, db.LockCoresearchChatParams{WorkspaceID: tx.WS, ID: *t.CoresearchChatID})
							if e != nil && !errors.Is(e, pgx.ErrNoRows) {
								return e
							}
							if e == nil {
								project, e := tx.Q.GetCoresearchProject(ctx, db.GetCoresearchProjectParams{WorkspaceID: tx.WS, ID: chat.ProjectID})
								if e != nil && !errors.Is(e, pgx.ErrNoRows) {
									return e
								}
								if e == nil {
									_, memberErr := tx.Q.CoresearchMember(ctx, db.CoresearchMemberParams{WorkspaceID: tx.WS, ProjectID: project.ID, UserID: chat.OwnerID})
									if memberErr != nil && !errors.Is(memberErr, pgx.ErrNoRows) {
										return memberErr
									}
									denied = memberErr != nil || project.ArchivedAt != nil || chat.OwnerID != current.OwnerID || t.ResponsibleUserID != current.OwnerID || t.RequesterID == nil || *t.RequesterID != current.OwnerID || chat.Cli != current.Harness || t.Harness != current.Harness || (chat.DaemonID != nil && *chat.DaemonID != current.DaemonID)
									if !denied {
										python := ""
										if project.PythonVersion != nil {
											python = *project.PythonVersion
										}
										core = &ClaimedCoresearch{ChatID: chat.ID, ProjectID: project.ID, PythonVersion: python}
										if err := tx.Q.BindCoresearchChat(ctx, db.BindCoresearchChatParams{WorkspaceID: tx.WS, ID: chat.ID, DaemonID: &current.DaemonID}); err != nil {
											return err
										}
									}
								}
							}
						}
					}
					if t.CardID != nil {
						card, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: ref.WorkspaceID, ID: *t.CardID})
						if err != nil {
							return err
						}
						changed := (card.Machine == nil) != (claim.ClaimedMachine == nil) || (card.Machine != nil && claim.ClaimedMachine != nil && *card.Machine != *claim.ClaimedMachine)
						if changed {
							if err := tx.Q.RequeueAgentRun(ctx, db.RequeueAgentRunParams{WorkspaceID: ref.WorkspaceID, ID: t.ID}); err != nil {
								return err
							}
							return tx.Q.UndoClaimAttempt(ctx, db.UndoClaimAttemptParams{WorkspaceID: ref.WorkspaceID, ID: t.ID})
						}
						denied = denied || t.ResponsibleUserID != card.OwnerID
						if card.Machine != nil {
							allowed, err := RuntimeAllowsUser(ctx, tx, current, card.OwnerID)
							if err != nil {
								return err
							}
							denied = denied || !allowed
						}
					}
					if denied {
						accessErr = errors.New("This machine is no longer shared with you; choose another for this study.")
						if t.Kind == "coresearch" {
							accessErr = errors.New("coresearch_access_revoked")
						}
						if err := tx.Q.UndoClaimAttempt(ctx, db.UndoClaimAttemptParams{WorkspaceID: ref.WorkspaceID, ID: t.ID}); err != nil {
							return err
						}
					}
					t, err = tx.Q.SetRunMachine(ctx, db.SetRunMachineParams{WorkspaceID: ref.WorkspaceID, ID: t.ID})
					if err != nil {
						return err
					}
					var mcp []agent.MCPServer
					if t.Kind != "coresearch" {
						mcp, err = addon.Servers(t.AddonMcp)
					}
					if accessErr != nil {
						err = accessErr
					}
					var conns []pack.Connector
					var team *TeamClaim
					var workspace []WorkspaceConnectorClaim
					if err == nil && t.Kind != "coresearch" {
						conns, team, err = ClaimConnectors(ctx, tx, t)
						if err != nil {
							return err
						}
						workspace, err = s.workspaceConnectorClaims(ctx, tx, t)
						if err != nil {
							return err
						}
						err = claimConnectorNames(conns, team, workspace, mcp)
					}
					if err != nil {
						// No token is issued. Access refusals settle without an execution
						// receipt because the CLI never launched.
						reason := "agent_error"
						message := "refused at claim: " + err.Error()
						if accessErr != nil {
							reason = "runtime_access_denied"
							if t.Kind == "coresearch" {
								reason = "coresearch_access_revoked"
							}
							message = accessErr.Error()
						}
						if err := tx.Q.FinishAgentRun(ctx, db.FinishAgentRunParams{Status: "failed", SessionID: t.SessionID,
							Error: message, FailureReason: &reason, ID: t.ID, WorkspaceID: ref.WorkspaceID}); err != nil {
							return err
						}
						f, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{ID: t.ID, WorkspaceID: ref.WorkspaceID})
						if err != nil {
							return err
						}
						if accessErr == nil {
							if err := saveReceipt(ctx, tx, f, &ref.RuntimeID); err != nil {
								return err
							}
						}
						if err := saveSettlement(ctx, tx, f); err != nil {
							return err
						}
						refused = append(refused, f)
						progress = true
						return tx.Emit(ctx, "agent_run.failed", f.CardID, store.System, agentRunEvent(f))
					}
					if mcp == nil {
						mcp = []agent.MCPServer{}
					}
					if err := tx.Q.AcceptMachineAttempt(ctx, db.AcceptMachineAttemptParams{
						WorkspaceID: ref.WorkspaceID, AgentRunID: t.ID, Attempt: t.Attempt,
						RuntimeID: current.ID, DaemonID: current.DaemonID, OwnerID: current.OwnerID,
						ResponsibleUserID: t.ResponsibleUserID,
					}); err != nil {
						return err
					}
					plain, hash, _ := auth.NewToken(AgentRunPrefix)
					if err := tx.Q.InsertAgentRunToken(ctx, db.InsertAgentRunTokenParams{TokenHash: hash, WorkspaceID: ref.WorkspaceID,
						AgentRunID: t.ID, CardID: t.CardID, Role: t.Role, Attempt: t.Attempt, Scopes: Scopes(t.Kind), ExpiresAt: time.Now().Add(tokenTTL)}); err != nil {
						return err
					}
					callPlain := ""
					if t.Kind != "coresearch" {
						var callHash string
						callPlain, callHash, _ = auth.NewToken(AgentRunPrefix)
						if err := tx.Q.InsertAgentRunToken(ctx, db.InsertAgentRunTokenParams{TokenHash: callHash, WorkspaceID: ref.WorkspaceID,
							AgentRunID: t.ID, CardID: t.CardID, Role: t.Role, Attempt: t.Attempt, Scopes: []string{ScopeCall}, ExpiresAt: time.Now().Add(tokenTTL)}); err != nil {
							return err
						}
					}
					if team != nil {
						teams[t.ID] = *team
					}
					var python string
					var files []ClaimedAttachment
					var services []ClaimedService
					env := map[string]string{}
					if t.CardID != nil {
						python, err = StudyPython(ctx, tx, *t.CardID)
						if err != nil {
							return err
						}
						card, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: ref.WorkspaceID, ID: *t.CardID})
						if err != nil {
							return err
						}
						if err := json.Unmarshal(card.Env, &env); err != nil {
							return err
						}
						if env == nil {
							env = map[string]string{}
						}
						rows, err := tx.Q.CardAttachments(ctx, db.CardAttachmentsParams{WorkspaceID: ref.WorkspaceID, CardID: t.CardID})
						if err != nil {
							return err
						}
						if t.BoardColumn == "experimentation" {
							rows, err := tx.Q.CardServices(ctx, db.CardServicesParams{WorkspaceID: ref.WorkspaceID, CardID: t.CardID})
							if err != nil {
								return err
							}
							for _, s := range rows {
								client := ""
								if s.ServiceClient != nil {
									client = *s.ServiceClient
								}
								services = append(services, ClaimedService{ID: s.ID, Name: *s.ServiceName, Start: *s.ServiceStart, Client: client, Env: s.ServiceEnv, Sha256: s.Sha256, SizeBytes: s.SizeBytes})
							}
						}
						for _, a := range rows {
							files = append(files, ClaimedAttachment{ID: a.ID, DiskName: a.DiskName, Sha256: a.Sha256, SizeBytes: a.SizeBytes, UnpackedBytes: a.UnpackedBytes, InnerFiles: a.InnerFiles})
						}
					}
					c = &Claimed{Coresearch: core, PythonVersion: python, AgentRun: t, Env: env, Token: plain, CallToken: callPlain, MCPURL: s.MCPURL, MCP: mcp, Connectors: conns, WorkspaceConnectors: workspace, Attachments: files, Services: services}
					return tx.Emit(ctx, "agent_run.dispatched", t.CardID, store.System, agentRunEvent(t))
				}()
				if err != nil {
					return err
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
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, f := range refused {
		s.deliverAfterCommit(ctx, f.WorkspaceID, f.ID)
	}
	// A Team pack is a blob, read only once the transaction is closed.
	for i := range out {
		if team, ok := teams[out[i].AgentRun.ID]; ok {
			conns, skills := s.teamTools(ctx, team)
			out[i].Connectors = slices.DeleteFunc(conns, func(c pack.Connector) bool { return c.Workspace != "" })
			out[i].Skills = skills
		}
	}
	return out, nil
}

// TeamClaim is what a Team run's connectors and skills are worked out from:
// the pack of the Team on its column, its role, and whether the run's
// researcher has added that Team version, without which no local command is
// handed out.
type TeamClaim struct {
	Pack  db.FieldPack
	Role  string
	Added bool
}

// ClaimConnectors answers a single agent's run with every catalogue connector
// the workspace has not disabled, or a Team run's TeamClaim.
func ClaimConnectors(ctx context.Context, tx *store.Tx, t db.AgentRun) ([]pack.Connector, *TeamClaim, error) {
	if t.CardID != nil && t.Role != "agent" {
		packID, err := tx.Q.GetCardTeamPack(ctx, db.GetCardTeamPackParams{WorkspaceID: tx.WS, CardID: *t.CardID, BoardColumn: t.BoardColumn})
		switch {
		case err == nil:
			row, err := tx.Q.GetFieldPack(ctx, db.GetFieldPackParams{WorkspaceID: tx.WS, ID: packID})
			if err != nil {
				return nil, nil, err
			}
			added, err := tx.Q.TeamAgentsFor(ctx, db.TeamAgentsForParams{WorkspaceID: tx.WS, PackID: packID, UserID: t.ResponsibleUserID})
			if err != nil {
				return nil, nil, err
			}
			return nil, &TeamClaim{Pack: row, Role: t.Role, Added: len(added) > 0}, nil
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, nil, err
		}
	}
	rows, err := tx.Q.ListWorkspaceConnectors(ctx, tx.WS)
	if err != nil {
		return nil, nil, err
	}
	disabled := map[string]bool{}
	for _, r := range rows {
		disabled[r.Name] = !r.Enabled
	}
	out := []pack.Connector{}
	for _, e := range connectors.Catalogue {
		if !disabled[e.Name] {
			out = append(out, pack.Connector{Catalogue: e.Name})
		}
	}
	return out, nil, nil
}

// TeamMember loads the Team's pack, a blob read to do with no transaction
// open, and answers the run's member with only the connectors it may call.
func TeamMember(ctx context.Context, blobs storage.Storage, team TeamClaim) (*pack.Pack, pack.Member, error) {
	p, err := pack.Load(ctx, blobs, team.Pack)
	if err != nil {
		return nil, pack.Member{}, err
	}
	member, _ := p.Manifest.MemberFor(team.Role)
	conns := []pack.Connector{}
	for _, c := range member.Connectors {
		if len(c.Command) > 0 && !team.Added {
			continue
		}
		conns = append(conns, c)
	}
	member.Connectors = conns
	return p, member, nil
}

// teamTools is the run's Team member's declared connectors and its own
// skills, each with its whole folder. A pack that cannot be read hands out
// neither: the run is already claimed, and calling nothing is safer than
// calling what was never checked.
func (s *AgentRuns) teamTools(ctx context.Context, team TeamClaim) ([]pack.Connector, []toolroute.RoutedSkill) {
	if s.Blobs == nil {
		return []pack.Connector{}, nil
	}
	p, member, err := TeamMember(ctx, s.Blobs, team)
	if err != nil {
		slog.Warn("team pack unreadable at claim; the run gets no connectors or skills", "pack", team.Pack.ID, "err", err)
		return []pack.Connector{}, nil
	}
	c := toolroute.ForTeam(p, member, nil, nil)
	all := toolroute.Result{}
	for _, sk := range c.Skills {
		all.Skills = append(all.Skills, toolroute.RoutedSkill{Name: sk.Name})
	}
	if err := c.AttachFolders(&all); err != nil {
		slog.Warn("team skill unreadable at claim; the run gets none of its skills", "pack", team.Pack.ID, "err", err)
		return member.Connectors, nil
	}
	return member.Connectors, all.Skills
}

func (s *AgentRuns) onAgentRun(ctx context.Context, user uuid.UUID, ref RuntimeRef, fn func(tx *store.Tx) error) error {
	return s.Store.InWorkspace(ctx, ref.WorkspaceID, user, func(tx *store.Tx) error {
		// Match board and role mutations before taking run or card locks.
		if _, err := tx.Q.LockWorkspaceExecution(ctx, ref.WorkspaceID); err != nil {
			return err
		}
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
		if err := tx.Q.StartMachineAttempt(ctx, db.StartMachineAttemptParams{WorkspaceID: ref.WorkspaceID, AgentRunID: run, Attempt: ref.Attempt, RuntimeID: ref.RuntimeID}); err != nil {
			return err
		}
		return tx.Emit(ctx, "agent_run.started", t.CardID, store.System, agentRunEvent(t))
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
			meta, err := agent.ParseStepTitles(m.Content)
			if err != nil {
				return ErrBadReport
			}
			if meta != nil {
				if m.Type != "status" {
					return ErrBadReport
				}
				for _, title := range meta.Titles {
					if title.Seq >= m.Seq {
						return ErrBadReport
					}
					target, err := tx.Q.AgentRunMessage(ctx, db.AgentRunMessageParams{WorkspaceID: ref.WorkspaceID, AgentRunID: run, Attempt: ref.Attempt, Seq: title.Seq})
					if err != nil {
						if errors.Is(err, pgx.ErrNoRows) {
							return ErrBadReport
						}
						return err
					}
					id, _, _, ok := agent.ToolNamingEvidence(agent.Message{Type: target.Type, Tool: target.Tool, Content: target.Content})
					if !ok || title.ToolUseID != id {
						return ErrBadReport
					}
				}
			}
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
	r.NamingUsage = slices.Clone(r.NamingUsage)
	if err := r.check(ok); err != nil {
		return db.AgentRun{}, err
	}
	var out db.AgentRun
	notify := false
	ended := false
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
			if err := tx.Q.InsertAgentRunUsage(ctx, db.InsertAgentRunUsageParams{WorkspaceID: ref.WorkspaceID, AgentRunID: run, Attempt: ref.Attempt, Model: u.Model, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens, CostUsd: cost, CostReported: u.CostReported}); err != nil {
				return err
			}
			spent += cost
		}
		if math.IsInf(t.CostUsd+spent, 0) {
			return ErrBadReport
		}
		// A card-less run has no card budget to charge.
		if t.CardID != nil {
			if err := tx.Q.AddCardSpend(ctx, db.AddCardSpendParams{WorkspaceID: ref.WorkspaceID, ID: *t.CardID, Usd: spent}); err != nil {
				return err
			}
		}
		if t.Status == "cancelled" {
			if err := tx.Q.RecordLateUsage(ctx, db.RecordLateUsageParams{WorkspaceID: ref.WorkspaceID, ID: run, CostUsd: spent}); err != nil {
				return err
			}
			out, err = tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: ref.WorkspaceID, ID: run})
			if err != nil {
				return err
			}
			if err := saveReceipt(ctx, tx, out, t.RuntimeID); err != nil {
				return err
			}
			return tx.Emit(ctx, "agent_run.usage_updated", out.CardID, store.System, agentRunEvent(out))
		}
		if err := tx.Q.DeleteAgentRunTokens(ctx, db.DeleteAgentRunTokensParams{WorkspaceID: ref.WorkspaceID, AgentRunID: run}); err != nil {
			return err
		}
		if r.Mode != "" || r.Model != "" {
			if err := tx.Q.RecordAgentRunProfile(ctx, db.RecordAgentRunProfileParams{WorkspaceID: ref.WorkspaceID, ID: run, ProfileMode: r.Mode, PolicyDigest: r.PolicyDigest, Model: r.Model}); err != nil {
				return err
			}
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
		if err = tx.Emit(ctx, "agent_run."+out.Status, out.CardID, store.System, agentRunEvent(out)); err != nil {
			return err
		}
		ended = true
		if terminal(out.Status) {
			notify = true
			return saveSettlement(ctx, tx, out)
		}
		return nil
	})
	if err == nil && ended && s.OnAttemptEnded != nil {
		s.OnAttemptEnded(out.WorkspaceID, out.ID, ref.Attempt)
	}
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
		if _, err := tx.Q.LockWorkspaceExecution(ctx, ws); err != nil {
			return err
		}
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
		if t, err = cancelLocked(ctx, tx, user, run); err != nil {
			return err
		}
		return saveSettlement(ctx, tx, t)
	})
	if err == nil {
		s.deliverAfterCommit(ctx, ws, run)
	}
	return err
}

// cancelLocked cancels a locked open run, revokes its tokens and records it.
func cancelLocked(ctx context.Context, tx *store.Tx, user, run uuid.UUID) (db.AgentRun, error) {
	if err := rows(tx.Q.CancelAgentRun(ctx, db.CancelAgentRunParams{WorkspaceID: tx.WS, ID: run})); err != nil {
		return db.AgentRun{}, err
	}
	if err := tx.Q.DeleteAgentRunTokens(ctx, db.DeleteAgentRunTokensParams{WorkspaceID: tx.WS, AgentRunID: run}); err != nil {
		return db.AgentRun{}, err
	}
	t, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: tx.WS, ID: run})
	if err != nil {
		return t, err
	}
	return t, tx.Emit(ctx, "agent_run.cancelled", t.CardID, store.User(user), agentRunEvent(t))
}

// CancelCardRuns cancels every open run of a study about to be deleted, in the
// caller's transaction. Nothing is settled: the study and its runs go with it.
func CancelCardRuns(ctx context.Context, tx *store.Tx, user, card uuid.UUID) error {
	open, err := tx.Q.CardInflightAgentRuns(ctx, db.CardInflightAgentRunsParams{WorkspaceID: tx.WS, CardID: &card})
	if err != nil {
		return err
	}
	for _, run := range open {
		t, err := tx.Q.LockAgentRun(ctx, db.LockAgentRunParams{WorkspaceID: tx.WS, ID: run})
		if err != nil {
			return err
		}
		if terminal(t.Status) {
			continue
		}
		if _, err := cancelLocked(ctx, tx, user, run); err != nil {
			return err
		}
	}
	return nil
}

// Sweep commits runtime status, lost attempts, receipts, events and settlement
// notifications together. Any failure rolls the entire maintenance pass back.
func (s *AgentRuns) Sweep(ctx context.Context) error {
	stale := s.Stale
	if stale <= 0 {
		stale = 60 * time.Second
	}
	var ended []db.AgentRun
	err := s.Store.Maintenance(ctx, func(tx *store.Tx) error {
		interval := pgtype.Interval{Microseconds: stale.Microseconds(), Valid: true}
		runtimes, err := tx.Q.RuntimeSweepCandidates(ctx, interval)
		if err != nil {
			return err
		}
		attempts, err := tx.Q.SweepCandidates(ctx)
		if err != nil {
			return err
		}
		workspaces := make(map[uuid.UUID]bool)
		for _, runtime := range runtimes {
			workspaces[runtime.WorkspaceID] = true
		}
		for _, attempt := range attempts {
			if _, ok := workspaces[attempt.WorkspaceID]; !ok {
				workspaces[attempt.WorkspaceID] = false
			}
		}
		ids := make([]uuid.UUID, 0, len(workspaces))
		for id := range workspaces {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
		// Take the whole maintenance batch in workspace order before child rows.
		// Event locks precede runtime rows, matching runtime registration.
		for _, id := range ids {
			if err := tx.Workspace(ctx, id); err != nil {
				return err
			}
			if _, err := tx.Q.LockWorkspaceExecution(ctx, id); errors.Is(err, pgx.ErrNoRows) {
				delete(workspaces, id)
			} else if err != nil {
				return err
			}
		}
		for _, id := range ids {
			if _, ok := workspaces[id]; !ok {
				continue
			}
			if err := tx.Q.LockWorkspaceEvents(ctx, id); err != nil {
				return err
			}
		}
		for _, id := range ids {
			if !workspaces[id] {
				continue
			}
			if err := tx.Workspace(ctx, id); err != nil {
				return err
			}
			n, err := tx.Q.SweepWorkspaceRuntimes(ctx, db.SweepWorkspaceRuntimesParams{WorkspaceID: id, Stale: interval})
			if err != nil {
				return err
			}
			if n > 0 {
				if err := tx.Emit(ctx, "runtime.updated", nil, store.System, map[string]any{"action": "stale_sweep"}); err != nil {
					return err
				}
			}
		}
		candidates, err := tx.Q.SweepCandidates(ctx)
		if err != nil {
			return err
		}
		var changed []db.AgentRun
		for _, candidate := range candidates {
			// A concurrent new candidate outside this locked batch is handled next pass.
			if _, ok := workspaces[candidate.WorkspaceID]; !ok {
				continue
			}
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
			ended = append(ended, t)
		}
		// Workspace execution locks protect the complete batch through event writes.
		for _, out := range changed {
			if err := tx.Workspace(ctx, out.WorkspaceID); err != nil {
				return err
			}
			event := "agent_run.requeued"
			if out.Status == "failed" {
				event = "agent_run.failed"
			}
			if err := tx.Emit(ctx, event, out.CardID, store.System, agentRunEvent(out)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if s.OnAttemptEnded != nil {
		for _, run := range ended {
			s.OnAttemptEnded(run.WorkspaceID, run.ID, run.Attempt)
		}
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

// WorkspaceConnectorRow is a current authorized library entry with its door name.
// Its sealed configuration is only opened on the server.
type WorkspaceConnectorRow struct {
	db.WorkspaceMcpServer
	DoorName      string
	Attempt       int32
	PolicyEnabled bool
}

// WorkspaceConnectorRows resolves the run's current Team declarations, or the
// solo library. Canonical stored manifests require no blob I/O in this transaction.
func WorkspaceConnectorRows(ctx context.Context, tx *store.Tx, run db.AgentRun) ([]WorkspaceConnectorRow, error) {
	_, team, err := ClaimConnectors(ctx, tx, run)
	if err != nil {
		return nil, err
	}
	refs := []pack.Connector{}
	if team != nil {
		var manifest pack.Manifest
		if err := json.Unmarshal(team.Pack.Manifest, &manifest); err != nil {
			return nil, err
		}
		member, ok := manifest.MemberFor(run.Role)
		if !ok {
			return []WorkspaceConnectorRow{}, nil
		}
		refs = member.Connectors
	} else {
		rows, err := tx.Q.ListWorkspaceMCPServers(ctx, tx.WS)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			refs = append(refs, pack.Connector{Name: r.Name, Workspace: r.ID.String()})
		}
	}
	out := []WorkspaceConnectorRow{}
	for _, ref := range refs {
		if ref.Workspace == "" {
			continue
		}
		id, err := uuid.Parse(ref.Workspace)
		if err != nil {
			return nil, ErrNotYours
		}
		row, err := tx.Q.LockWorkspaceMCPServer(ctx, db.LockWorkspaceMCPServerParams{WorkspaceID: tx.WS, ID: id})
		if err != nil {
			return nil, err
		}
		if row.Transport == "command" && team != nil && !team.Added {
			continue
		}
		policy, err := availability.Get(ctx, tx, "mcp", row.ID.String())
		if err != nil {
			return nil, err
		}
		out = append(out, WorkspaceConnectorRow{row, ref.Name, run.Attempt, policy.Enabled})
	}
	return out, nil
}
func (s *AgentRuns) workspaceConnectorClaims(ctx context.Context, tx *store.Tx, run db.AgentRun) ([]WorkspaceConnectorClaim, error) {
	rows, err := WorkspaceConnectorRows(ctx, tx, run)
	if err != nil {
		return nil, err
	}
	out := make([]WorkspaceConnectorClaim, 0, len(rows))
	for _, row := range rows {
		if !row.PolicyEnabled {
			continue
		}
		claim := WorkspaceConnectorClaim{ID: row.ID, Name: row.DoorName, Transport: row.Transport}
		if row.Transport == "command" {
			if s.Keys == nil {
				return nil, errors.New("workspace command configuration unavailable")
			}
			raw, err := s.Keys.Open(tx.WS, "mcp-servers/"+row.ID.String(), row.ConfigCiphertext)
			if err != nil {
				return nil, err
			}
			var config struct {
				Command string
				Args    []string
				Env     map[string]string
			}
			if json.Unmarshal([]byte(raw), &config) != nil {
				return nil, errors.New("workspace command configuration unavailable")
			}
			claim.Command = append([]string{config.Command}, config.Args...)
			claim.Env = config.Env
			if pack.ValidateConnector(pack.Connector{Command: claim.Command, Env: claim.Env}) != nil {
				return nil, errors.New("workspace command configuration invalid")
			}
		}
		out = append(out, claim)
	}
	return out, nil
}

// claimConnectorNames refuses ambiguity before either run token is issued.
func claimConnectorNames(conns []pack.Connector, team *TeamClaim, workspace []WorkspaceConnectorClaim, addons []agent.MCPServer) error {
	if team != nil {
		var manifest pack.Manifest
		if json.Unmarshal(team.Pack.Manifest, &manifest) != nil {
			return errors.New("Team connectors unavailable")
		}
		member, _ := manifest.MemberFor(team.Role)
		conns = member.Connectors
	}
	names := map[string]bool{"sources": true}
	add := func(name string) error {
		if name == "" || names[name] {
			return errors.New("connector names are ambiguous or reserved")
		}
		names[name] = true
		return nil
	}
	for _, c := range conns {
		if c.Workspace != "" {
			continue
		}
		name := c.Name
		if name == "" {
			name = c.Catalogue
		}
		if err := add(name); err != nil {
			return err
		}
	}
	for _, c := range workspace {
		if err := add(c.Name); err != nil {
			return err
		}
	}
	for _, a := range addons {
		if err := add(a.Name); err != nil {
			return err
		}
	}
	return nil
}

// CommandFingerprint binds daemon inventory to the exact non-secret configuration
// in its claim, including argument order and canonical environment key order.
func CommandFingerprint(command []string, env map[string]string) string {
	raw, _ := json.Marshal(struct {
		Command []string
		Env     map[string]string
	}{command, env})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// CancelInTx settles a run in the caller's authorized workspace transaction.
// The caller must hold the workspace and resource locks before invoking it.
func (s *AgentRuns) CancelInTx(ctx context.Context, tx *store.Tx, user uuid.UUID, t db.AgentRun) error {
	if terminal(t.Status) {
		return ErrConflict
	}
	done, err := cancelLocked(ctx, tx, user, t.ID)
	if err != nil {
		return err
	}
	if err = saveSettlement(ctx, tx, done); err != nil {
		return err
	}
	tx.AfterCommit(func() { s.deliverAfterCommit(context.WithoutCancel(ctx), tx.WS, t.ID) })
	return nil
}
