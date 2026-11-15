// Package service owns the agent-run state machine: enqueue, claim, start, stream,
// settle, cancel and sweep.
package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"slices"
	"sort"
	"strings"
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
	"lykeion/server/internal/skill"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	"lykeion/server/internal/toolroute"
	"lykeion/server/internal/workflow"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

// Token scopes. A role run's token acts as its role on its card. An
// autoresearch agent run's token may only post its run's report and open
// cards.
const (
	ScopeAristotle           = "aristotle:ask"
	ScopeCoresearch          = "coresearch:chat"
	ScopeCoresearchConnector = "coresearch:connector"
	ScopeRole                = "card:role"
	ScopeReport              = "report:write"
	ScopeCardCreate          = "card:create"
	// ScopeRerun reads the study's attachments and saves a rerun's outputs,
	// and nothing else: a rerun writes no record of its own.
	ScopeRerun = "rerun:files"
	// ScopeCall records a connector call. Only the daemon's tool door holds a
	// token with it; the agent's own token never does.
	ScopeCall = "call:record"
	// ScopeHandIn is a report run's only scope, and no route accepts it: the
	// run's hand-in is its final answer, which its daemon posts.
	ScopeHandIn = "run:hand-in"
)

// issueClaimTokens keeps the daemon's connector token separate from the CLI
// token. Aristotle has only lab read tools, even if a claim is misconfigured
// with workspace connectors.
func issueClaimTokens(ctx context.Context, tx *store.Tx, t db.AgentRun, workspaceConnectors bool) (string, string, error) {
	mint := func(scopes []string) (string, error) {
		plain, hash, _ := auth.NewToken(AgentRunPrefix)
		err := tx.Q.InsertAgentRunToken(ctx, db.InsertAgentRunTokenParams{TokenHash: hash, WorkspaceID: t.WorkspaceID, AgentRunID: t.ID, CardID: t.CardID, Role: t.Role, Attempt: t.Attempt, Scopes: scopes, ExpiresAt: time.Now().Add(tokenTTL)})
		return plain, err
	}
	plain, err := mint(Scopes(t.Kind))
	if err != nil {
		return "", "", err
	}
	if t.Kind == "aristotle" || t.Kind == "report" || ((t.Kind == "coresearch" || t.Kind == "rerun") && !workspaceConnectors) {
		return plain, "", nil
	}
	scope := ScopeCall
	if t.Kind == "coresearch" {
		scope = ScopeCoresearchConnector
	}
	call, err := mint([]string{scope})
	return plain, call, err
}

// Scopes are the powers an agent run's token carries, by the agent run's kind.
func Scopes(kind string) []string {
	if kind == "aristotle" {
		return []string{ScopeAristotle}
	}
	if kind == "coresearch" {
		return []string{ScopeCoresearch}
	}
	if kind == "autoresearch" {
		return []string{ScopeReport, ScopeCardCreate}
	}
	if kind == "rerun" {
		return []string{ScopeRerun}
	}
	if kind == "report" {
		return []string{ScopeHandIn}
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
	return t, s.NotifyQueued(ctx, tx, t)
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

// FailureReasons are how an attempt can end badly. Infra tells the machine's
// and the network's fault from the agent's. Runtime access denial is recorded
// only by the server.
var FailureReasons = []string{"runtime_offline", "timeout", "provider_network", "context_exhausted", "agent_error", "runtime_access_denied", "coresearch_access_revoked",
	agent.ReasonSignIn, agent.ReasonCLIMissing, agent.ReasonCLITooOld, agent.ReasonDoor, agent.ReasonCLIBroken, agent.ReasonSandbox, agent.ReasonFenceBreach, agent.ReasonHandInInvalid, agent.ReasonLabContextChanged}

// Infra reports whether a reason is the machine's or its tooling's fault
// rather than the agent's, so the run is not held against the agent.
func Infra(reason string) bool {
	switch reason {
	case "runtime_offline", "timeout", "provider_network", agent.ReasonSignIn, agent.ReasonCLIMissing, agent.ReasonCLITooOld,
		agent.ReasonDoor, agent.ReasonCLIBroken, agent.ReasonSandbox, agent.ReasonFenceBreach:
		return true
	}
	return false
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
	kind            string // empty is a role run
}

type WorkspaceConnectorClaim struct {
	ID        uuid.UUID         `json:"id"`
	Name      string            `json:"name"`
	Transport string            `json:"transport"`
	Command   []string          `json:"command,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

type ClaimedCoresearch struct {
	Aristotle     bool      `json:"aristotle"`
	ChatID        uuid.UUID `json:"chat_id"`
	ProjectID     uuid.UUID `json:"project_id"`
	PythonVersion string    `json:"python_version"`
}

// RerunStep is a recorded script run a rerun replays: the run record's key,
// what it ran, and the package list it saved, empty when that list is gone.
type RerunStep struct {
	Key      string `json:"key"`
	Path     string `json:"path"`
	Language string `json:"language"`
	Script   string `json:"script"`
	Packages string `json:"packages"`
	// packagesSHA names the saved list's blob, read once the claim's
	// transaction is closed.
	packagesSHA string
}

// SavedOutput is an output a rerun's earlier attempt already saved.
type SavedOutput struct {
	Name   string `json:"name"`
	Sha256 string `json:"sha256"`
}

// ClaimedRerun is what a rerun replays: the scripts of the study's newest
// Experimentation round that exited 0, in the order they ran, in a fresh
// environment rebuilt from each step's package list and the study's Python
// version, with the study's attachments in place. Saved are the outputs an
// earlier attempt of the rerun already saved, which are not saved again.
type ClaimedRerun struct {
	Steps       []RerunStep         `json:"steps"`
	Python      string              `json:"python"`
	Attachments []ClaimedAttachment `json:"attachments"`
	Saved       []SavedOutput       `json:"saved"`
}

type Claimed struct {
	HasCommentSnapshot bool               `json:"has_comment_snapshot,omitempty"`
	ReservedCommentIDs []uuid.UUID        `json:"reserved_comment_ids,omitempty"`
	Coresearch         *ClaimedCoresearch `json:"coresearch,omitempty"`
	Rerun              *ClaimedRerun      `json:"rerun,omitempty"`
	PythonVersion      string             `json:"python_version,omitempty"`
	AgentRun           db.AgentRun        `json:"agent_run"`
	Token              string             `json:"token"`
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
	// OutputSchema is the JSON schema the run's final answer, its hand-in,
	// must fit; empty for a run that owes none. Lead is set for the run that
	// works its column through: a Team's leader or a study's single agent.
	OutputSchema string `json:"output_schema,omitempty"`
	Lead         bool   `json:"lead,omitempty"`
	// Leader is set for a Team leader's lead run, which alone delegates to
	// members through its door.
	Leader bool `json:"leader,omitempty"`
}

type ClaimedService struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Start     string    `json:"start"`
	Client    string    `json:"client"`
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
	// provider is the model provider of the harness that ran the attempt; it
	// picks the price an unlisted model is estimated at.
	provider string
}

// providers maps a harness to the provider whose list prices apply.
var providers = map[string]string{"codex": "openai", "claude": "anthropic"}

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
			cost, r.Usage[i].Estimated = pricing.CostFor(r.provider, u.Model, u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens)
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
		single := Report{Usage: []agent.Usage{named}, provider: r.provider}
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
			existing.Estimated = existing.Estimated || addition.Estimated
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
	LabMCPURL string
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
	if t.Kind == "coresearch" {
		event := map[string]any{"agent_run_id": t.ID}
		if t.CoresearchChatID != nil {
			event["chat_id"] = *t.CoresearchChatID
		}
		return event
	}
	return map[string]any{"agent_run_id": t.ID, "role": t.Role, "column": t.BoardColumn, "round": t.Round, "status": t.Status, "attempt": t.Attempt}
}

func emitAgentRunEvent(ctx context.Context, tx *store.Tx, run db.AgentRun, typ string, actor store.Actor, payload any) error {
	if run.Kind == "aristotle" && run.LabPurpose == "manual_decision_draft" && run.RequesterID != nil {
		return tx.EmitPrivate(ctx, *run.RequesterID, typ, actor, payload)
	}
	return tx.Emit(ctx, typ, run.CardID, actor, payload)
}

// NotifyQueued wakes the responsible user's daemons after this transaction
// commits. The event carries run metadata only.
func (s *AgentRuns) NotifyQueued(ctx context.Context, tx *store.Tx, t db.AgentRun) error {
	p := agentRunEvent(t)
	p["owner_id"] = t.ResponsibleUserID
	return emitAgentRunEvent(ctx, tx, t, "agent_run.queued", store.System, p)
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
		AgentID: n.Agent, Assignment: n.Assignment, Kind: n.kind,
	})
	if err != nil {
		return t, err
	}
	if err := s.NotifyQueued(ctx, tx, t); err != nil {
		return t, err
	}
	if n.Agent != nil {
		a, err := tx.Q.GetAgent(ctx, db.GetAgentParams{WorkspaceID: tx.WS, ID: *n.Agent})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return t, err
		}
		if err == nil && a.Laboratory != "" && a.RuntimeID == nil {
			if err := waitForLaboratoryMachine(ctx, tx, a); err != nil {
				return t, err
			}
		}
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
	return f, emitAgentRunEvent(ctx, tx, f, "agent_run.failed", store.System, agentRunEvent(f))
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
		member, err := tx.Q.GetMember(ctx, db.GetMemberParams{WorkspaceID: ws, UserID: user})
		if err != nil {
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
			if err := tx.Q.ResolveMachineOffline(ctx, db.ResolveMachineOfflineParams{WorkspaceID: ws, RuntimeID: &r.ID}); err != nil {
				return err
			}
			out = append(out, r)
		}
		if member.Role == "owner" {
			if err := bindUnbound(ctx, tx, out); err != nil {
				return err
			}
		}
		return tx.Emit(ctx, "runtime.online", nil, store.User(user), map[string]any{"daemon_id": daemonID, "harnesses": harnesses})
	})
	return out, err
}

var (
	ErrNotLaboratory      = errors.New("only laboratory agents are bound to a machine")
	ErrRuntimeNotAllowed  = errors.New("this machine isn't shared with the workspace owner")
	ErrHarnessUnsupported = errors.New("this agent can't run on that CLI")
)

// BindLaboratoryRuntime sends every run of a laboratory agent to rt, a
// runtime that allows the agent's owner and whose CLI the agent's Team member
// runs on. Its queued runs move to that CLI, and its waiting notice is
// answered.
func BindLaboratoryRuntime(ctx context.Context, tx *store.Tx, a db.Agent, rt db.Runtime) error {
	if a.Laboratory == "" {
		return ErrNotLaboratory
	}
	if rt.WorkspaceID != tx.WS || a.WorkspaceID != tx.WS {
		return pgx.ErrNoRows
	}
	allowed, err := RuntimeAllowsUser(ctx, tx, rt, a.OwnerID)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrRuntimeNotAllowed
	}
	def, err := team.LaboratoryDef(ctx, tx, a.ID)
	if err != nil {
		return err
	}
	model, ok := team.ModelFor(def, rt.Harness)
	if !ok {
		return ErrHarnessUnsupported
	}
	// Claims read the binding and the queued runs' CLI under this lock.
	if _, err := tx.Q.LockWorkspaceExecution(ctx, tx.WS); err != nil {
		return err
	}
	if _, err := tx.Q.BindAgentRuntime(ctx, db.BindAgentRuntimeParams{WorkspaceID: tx.WS, ID: a.ID, RuntimeID: &rt.ID}); err != nil {
		return err
	}
	if err := tx.Q.RetargetLaboratoryRuns(ctx, db.RetargetLaboratoryRunsParams{WorkspaceID: tx.WS, AgentID: &a.ID, Harness: rt.Harness, RequestedModel: model}); err != nil {
		return err
	}
	if err := tx.Q.ResolveLaboratoryWait(ctx, db.ResolveLaboratoryWaitParams{WorkspaceID: tx.WS, Cause: unboundCause(a.ID)}); err != nil {
		return err
	}
	return tx.Emit(ctx, "agent.updated", nil, store.System, map[string]any{"agent_id": a.ID})
}

// KeepLaboratoryOwned hands the laboratory agents of a departed or demoted
// owner to the oldest remaining owner, with their open machine notices, and
// unbinds every laboratory agent whose machine no longer allows its owner.
func KeepLaboratoryOwned(ctx context.Context, tx *store.Tx) error {
	// Claims read the binding under this lock.
	if _, err := tx.Q.LockWorkspaceExecution(ctx, tx.WS); err != nil {
		return err
	}
	owner, err := tx.Q.OldestOwner(ctx, tx.WS)
	if err != nil {
		return err
	}
	moved, err := tx.Q.ReassignLaboratoryAgents(ctx, db.ReassignLaboratoryAgentsParams{WorkspaceID: tx.WS, OwnerID: owner})
	if err != nil {
		return err
	}
	if err := tx.Q.MoveLaboratoryWaits(ctx, db.MoveLaboratoryWaitsParams{WorkspaceID: tx.WS, UserID: owner}); err != nil {
		return err
	}
	changed := map[uuid.UUID]bool{}
	for _, a := range moved {
		changed[a.ID] = true
	}
	bound, err := tx.Q.BoundLaboratoryAgents(ctx, tx.WS)
	if err != nil {
		return err
	}
	for _, a := range bound {
		rt, err := tx.Q.GetRuntime(ctx, db.GetRuntimeParams{WorkspaceID: tx.WS, ID: *a.RuntimeID})
		if err != nil {
			return err
		}
		allowed, err := RuntimeAllowsUser(ctx, tx, rt, a.OwnerID)
		if err != nil {
			return err
		}
		if allowed {
			continue
		}
		if _, err := tx.Q.BindAgentRuntime(ctx, db.BindAgentRuntimeParams{WorkspaceID: tx.WS, ID: a.ID}); err != nil {
			return err
		}
		changed[a.ID] = true
	}
	for id := range changed {
		if err := tx.Emit(ctx, "agent.updated", nil, store.System, map[string]any{"agent_id": id}); err != nil {
			return err
		}
	}
	return nil
}

// bindUnbound binds each unbound laboratory agent to the first of a workspace
// owner's new runtimes, Claude Code before Codex, that it can be bound to.
func bindUnbound(ctx context.Context, tx *store.Tx, runtimes []db.Runtime) error {
	runtimes = slices.Clone(runtimes)
	slices.SortFunc(runtimes, func(a, b db.Runtime) int { return strings.Compare(a.Harness, b.Harness) })
	agents, err := tx.Q.UnboundLaboratoryAgents(ctx, tx.WS)
	if err != nil {
		return err
	}
	for _, a := range agents {
		for _, rt := range runtimes {
			err := BindLaboratoryRuntime(ctx, tx, a, rt)
			if errors.Is(err, ErrRuntimeNotAllowed) || errors.Is(err, ErrHarnessUnsupported) {
				continue
			}
			if err != nil {
				return err
			}
			break
		}
	}
	return nil
}

func unboundCause(agent uuid.UUID) string { return "unbound:" + agent.String() }

// waitForLaboratoryMachine tells the laboratory agent's owner, once while it
// stays unbound, that the studies it works wait for a machine.
func waitForLaboratoryMachine(ctx context.Context, tx *store.Tx, a db.Agent) error {
	return tx.Q.UpsertLaboratoryWait(ctx, db.UpsertLaboratoryWaitParams{WorkspaceID: tx.WS, UserID: a.OwnerID, Cause: unboundCause(a.ID),
		Text: fmt.Sprintf("%s isn't bound to a machine, so the studies it works are waiting. Choose its machine in Agents.", a.Name)})
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
type RuntimeTelemetry struct {
	Metrics json.RawMessage
	Models  map[string]json.RawMessage
}

func (s *AgentRuns) Heartbeat(ctx context.Context, user uuid.UUID, refs []RuntimeRef, bases map[string]string, telemetry ...RuntimeTelemetry) (int, error) {
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
			if k > 0 {
				if err := tx.Q.ResolveMachineOffline(ctx, db.ResolveMachineOfflineParams{WorkspaceID: ref.WorkspaceID, RuntimeID: &ref.RuntimeID}); err != nil {
					return err
				}
			}
			if len(telemetry) > 0 {
				rt, e := tx.Q.GetRuntime(ctx, db.GetRuntimeParams{WorkspaceID: ref.WorkspaceID, ID: ref.RuntimeID})
				if e != nil {
					return e
				}
				if e = tx.Q.SetRuntimeCompute(ctx, db.SetRuntimeComputeParams{WorkspaceID: ref.WorkspaceID, ID: ref.RuntimeID, OwnerID: user, Metrics: telemetry[0].Metrics, Models: telemetry[0].Models[rt.Harness]}); e != nil {
					return e
				}
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

// activeSlots counts the runs holding a runtime's slots. A leader whose
// member's run of the same column and round has not ended holds none: it
// only waits, and its member must be able to start on a one-slot runtime.
func activeSlots(ctx context.Context, tx *store.Tx, ref RuntimeRef) (int, error) {
	runs, err := tx.Q.RuntimeActiveRuns(ctx, db.RuntimeActiveRunsParams{WorkspaceID: ref.WorkspaceID, RuntimeID: &ref.RuntimeID})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range runs {
		if r.Role == "leader" && r.CardID != nil {
			_, err := tx.Q.InFlightMemberRun(ctx, db.InFlightMemberRunParams{WorkspaceID: ref.WorkspaceID, CardID: *r.CardID, BoardColumn: r.BoardColumn, Round: r.Round})
			if err == nil {
				continue
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return 0, err
			}
		}
		n++
	}
	return n, nil
}

// withMemberWork is a re-claimed leader run's prompt with what its members
// already handed in on its column and round, and the member still working,
// so a leader whose session could not be resumed redoes no member's work.
func withMemberWork(ctx context.Context, tx *store.Tx, t db.AgentRun) (string, error) {
	recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: *t.CardID, BoardColumn: t.BoardColumn})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, r := range recs {
		if r.Kind != "hand_in" || r.Round != t.Round || slices.Contains([]string{"", "leader", "auditor", "agent", "objector", "rerun", "reporter"}, r.Role) {
			continue
		}
		var body struct {
			AgentRunID uuid.UUID `json:"agent_run_id"`
			HandIn     struct {
				Answer string `json:"answer"`
			} `json:"hand_in"`
		}
		if json.Unmarshal(r.Body, &body) != nil {
			continue
		}
		fmt.Fprintf(&b, "- %s (ticket %s) handed in: %s\n", r.Role, body.AgentRunID, body.HandIn.Answer)
	}
	working, err := tx.Q.InFlightMemberRun(ctx, db.InFlightMemberRunParams{WorkspaceID: tx.WS, CardID: *t.CardID, BoardColumn: t.BoardColumn, Round: t.Round})
	switch {
	case err == nil:
		fmt.Fprintf(&b, "- A member is still working on ticket %s; wait for it with member_result.\n", working)
	case !errors.Is(err, pgx.ErrNoRows):
		return "", err
	}
	if b.Len() == 0 {
		return t.Prompt, nil
	}
	return t.Prompt + "\n\nYour members' work on this column so far:\n" + b.String() + "Use their work; don't ask them for it again.\n", nil
}

type ClaimOptions struct {
	CoresearchOnly bool

	GuestIsolation string
}

// Claim fills up to slots agent runs across the caller's runtimes, one run per
// runtime per pass, until nothing more can be claimed. An agent run's add-on
// servers were checked when the owner saved them and when it was queued; they
// are checked once more before they reach a daemon, and an agent run that fails
// is settled failed instead of handed out.
func (s *AgentRuns) Claim(ctx context.Context, user uuid.UUID, refs []RuntimeRef, slots int, options ...ClaimOptions) ([]Claimed, error) {
	coresearchOnly := len(options) > 0 && options[0].CoresearchOnly
	guestIsolation := len(options) > 0 && options[0].GuestIsolation == agent.GuestIsolationProtocol
	var out []Claimed
	var refused []db.AgentRun
	teams := map[uuid.UUID]TeamClaim{}
	soloTeams := map[uuid.UUID]db.FieldPack{}
	agentSkills := map[uuid.UUID][]string{}
	workflows := map[uuid.UUID]string{}
	rerunPackages := map[uuid.UUID]string{} // a rerun's package list, by blob key
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
		workspaceCount := 0
		for i, ref := range ordered {
			if i == 0 || ordered[i-1].WorkspaceID != ref.WorkspaceID {
				workspaceCount++
			}
		}
		budget := 64
		quota := min(32, max(1, budget/max(1, workspaceCount)))
		seen := map[uuid.UUID]bool{}
		for _, ref := range ordered {
			if budget == 0 {
				break
			}
			if seen[ref.WorkspaceID] {
				continue
			}
			seen[ref.WorkspaceID] = true
			if err := tx.Workspace(ctx, ref.WorkspaceID); err != nil {
				return err
			}
			removed, err := tx.Q.LockRemovedHarnessQueue(ctx, db.LockRemovedHarnessQueueParams{WorkspaceID: ref.WorkspaceID, OwnerID: user, BatchLimit: int32(min(quota, budget))})
			if err != nil {
				return err
			}
			budget -= len(removed)
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
					active, err := activeSlots(ctx, tx, ref)
					if err != nil {
						return err
					}
					if !coresearchOnly && active >= int(runtime.MaxConcurrent) {
						return nil
					}
					claim, err := tx.Q.ClaimAgentRun(ctx, db.ClaimAgentRunParams{RuntimeID: &ref.RuntimeID, WorkspaceID: ref.WorkspaceID, GuestIsolation: guestIsolation, CoresearchOnly: coresearchOnly})
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
					if t.Kind == "aristotle" && agent.LabAvailability(t.Harness) != nil {
						// The tentative claim never became an attempt or left this transaction.
						if err := tx.Q.UndoClaimAttempt(ctx, db.UndoClaimAttemptParams{WorkspaceID: tx.WS, ID: t.ID}); err != nil {
							return err
						}
						_, err := s.CancelUnavailableLabInTx(ctx, tx, t)
						progress = true
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
					if t.Kind == "autoresearch" || t.Kind == "aristotle" {
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
										isAristotle, e := tx.Q.CoresearchChatIsAristotle(ctx, db.CoresearchChatIsAristotleParams{WorkspaceID: tx.WS, ChatID: chat.ID})
										if e != nil {
											return e
										}
										core = &ClaimedCoresearch{ChatID: chat.ID, ProjectID: project.ID, PythonVersion: python, Aristotle: isAristotle}
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
						if claim.LaboratoryOwner != uuid.Nil {
							// A laboratory agent's bound machine serves whichever
							// study it works, while it allows the agent's owner.
							allowed, err := RuntimeAllowsUser(ctx, tx, current, claim.LaboratoryOwner)
							if err != nil {
								return err
							}
							denied = denied || !allowed
						} else if card.Machine != nil {
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
					// A rerun runs only recorded scripts, and a report run reads only
					// its brief: no tool servers or connectors.
					var mcp []agent.MCPServer
					if t.Kind != "coresearch" && t.Kind != "rerun" && t.Kind != "aristotle" && t.Kind != "report" {
						mcp, err = addon.Servers(t.AddonMcp)
					}
					if accessErr != nil {
						err = accessErr
					}
					var conns []pack.Connector
					var team *TeamClaim
					var workspace []WorkspaceConnectorClaim
					if err == nil && t.Kind != "coresearch" && t.Kind != "rerun" && t.Kind != "aristotle" && t.Kind != "report" {
						conns, team, err = ClaimConnectors(ctx, tx, t)
						if err != nil {
							return err
						}
						workspace, err = s.workspaceConnectorClaims(ctx, tx, t)
						if err != nil {
							return err
						}
						err = claimConnectorNames(conns, team, workspace, mcp)
					} else if err == nil && t.Kind == "coresearch" {
						workspace, err = s.workspaceConnectorClaims(ctx, tx, t)
					}
					// Reject unsafe legacy inputs before issuing tokens or accepting
					// a machine attempt. A refusal belongs to this run, not the
					// whole claim batch: the existing refusal path persists its
					// visible failure and continues looking for other work.
					if err == nil && t.CardID != nil {
						if _, e := tx.Q.LockCard(ctx, db.LockCardParams{WorkspaceID: tx.WS, ID: *t.CardID}); e != nil {
							return e
						}
						if e := tx.Q.BindCommentReservations(ctx, db.BindCommentReservationsParams{WorkspaceID: tx.WS, AgentRunID: t.ID, Attempt: t.Attempt}); e != nil {
							return e
						}
						if _, e := reservedLaunch(ctx, tx, t); e != nil {
							if !errors.Is(e, ErrConflict) {
								return e
							}
							err = e
						}
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
						return emitAgentRunEvent(ctx, tx, f, "agent_run.failed", store.System, agentRunEvent(f))
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
					plain, callPlain, err := issueClaimTokens(ctx, tx, t, len(workspace) > 0)
					if err != nil {
						return err
					}
					if t.Role == "agent" && t.AgentID != nil && t.CardID != nil && t.Kind == "role" {
						row, e := tx.ShippedSoloTeam(ctx, *t.AgentID, t.BoardColumn)
						if e != nil && !errors.Is(e, pgx.ErrNoRows) {
							return e
						}
						if e == nil {
							soloTeams[t.ID] = row
						}
					}
					if team != nil {
						teams[t.ID] = *team
					} else if t.Kind != "coresearch" && t.Kind != "aristotle" && t.Kind != "report" && t.AgentID != nil {
						cfg, err := tx.AgentCapabilities(ctx, *t.AgentID)
						if err != nil && !errors.Is(err, pgx.ErrNoRows) {
							return err
						}
						if err == nil {
							var names []string
							if err := json.Unmarshal(cfg.Skills, &names); err != nil {
								return err
							}
							agentSkills[t.ID] = names
						}
					}
					if team == nil && t.Kind != "coresearch" && t.Kind != "rerun" && t.CardID != nil {
						slug, err := tx.Q.CardWorkflow(ctx, db.CardWorkflowParams{WorkspaceID: ref.WorkspaceID, ID: *t.CardID})
						if err != nil && !errors.Is(err, pgx.ErrNoRows) {
							return err
						}
						if slug != nil {
							workflows[t.ID] = *slug
						}
					}
					var python string
					var files []ClaimedAttachment
					var services []ClaimedService
					// A report run gets none of its study's files: its brief is its evidence.
					if t.CardID != nil && t.Kind != "report" {
						python, err = StudyPython(ctx, tx, *t.CardID)
						if err != nil {
							return err
						}
						rows, err := tx.Q.CardAttachments(ctx, db.CardAttachmentsParams{WorkspaceID: ref.WorkspaceID, CardID: t.CardID})
						if err != nil {
							return err
						}
						snapshot, err := tx.RunAttachmentSnapshot(ctx, t.ID)
						if err != nil {
							return err
						}
						if snapshot != nil {
							rows = slices.DeleteFunc(rows, func(a db.Attachment) bool { return !slices.Contains(snapshot, a.ID) })
						}
						// A rerun replays Experimentation's scripts, which may call
						// its services.
						if t.BoardColumn == "experimentation" || t.Kind == "rerun" {
							rows, err := tx.Q.CardServices(ctx, db.CardServicesParams{WorkspaceID: ref.WorkspaceID, CardID: t.CardID})
							if err != nil {
								return err
							}
							for _, s := range rows {
								client := ""
								if s.ServiceClient != nil {
									client = *s.ServiceClient
								}
								services = append(services, ClaimedService{ID: s.ID, Name: *s.ServiceName, Start: *s.ServiceStart, Client: client, Sha256: s.Sha256, SizeBytes: s.SizeBytes})
							}
						}
						for _, a := range rows {
							files = append(files, ClaimedAttachment{ID: a.ID, DiskName: a.DiskName, Sha256: a.Sha256, SizeBytes: a.SizeBytes, UnpackedBytes: a.UnpackedBytes, InnerFiles: a.InnerFiles})
						}
					}
					var rerun *ClaimedRerun
					if t.Kind == "rerun" {
						st, err := StandingExperimentation(ctx, tx, *t.CardID)
						if err != nil {
							return err
						}
						own, err := tx.Q.AgentRunRecords(ctx, db.AgentRunRecordsParams{WorkspaceID: ref.WorkspaceID, AgentRunID: &t.ID})
						if err != nil {
							return err
						}
						saved := []SavedOutput{}
						for _, r := range own {
							if r.Kind == "rerun_output" {
								var b SavedOutput
								if json.Unmarshal(r.Body, &b) == nil {
									saved = append(saved, b)
								}
							}
						}
						rerun = &ClaimedRerun{Steps: st.RerunSteps(), Python: python, Attachments: files, Saved: saved}
						rerunPackages[t.ID] = ref.WorkspaceID.String() + "/" + t.CardID.String() + "/"
					}
					if t.Kind == "aristotle" {
						t, err = tx.Q.RefreshLabRunPrompt(ctx, t.ID)
						t.ContinuityScope, t.SessionID = "", "" // Each turn starts with its currently permitted history.
						if err != nil {
							return err
						}
					}
					if (core != nil || t.Kind == "aristotle") && t.RequestedModel != "" && !listsModel(current.Models, t.RequestedModel) {
						// No CLI offers the frozen model any longer: run on the CLI's default.
						t.RequestedModel = ""
					}
					if t.Role == "leader" && t.Attempt > 1 && t.CardID != nil {
						if t.Prompt, err = withMemberWork(ctx, tx, t); err != nil {
							return err
						}
					}
					mcpURL := s.MCPURL
					if t.Kind == "aristotle" {
						mcpURL = s.LabMCPURL
					}
					launch, err := reservedLaunch(ctx, tx, t)
					if err != nil {
						return err
					}
					t.Prompt = launch.Prompt
					c = &Claimed{HasCommentSnapshot: launch.HasSnapshot, ReservedCommentIDs: launch.CommentIDs, Coresearch: core, Rerun: rerun, PythonVersion: python, AgentRun: t, Token: plain, CallToken: callPlain, MCPURL: mcpURL, MCP: mcp, Connectors: conns, WorkspaceConnectors: workspace, Attachments: files, Services: services}
					return emitAgentRunEvent(ctx, tx, t, "agent_run.dispatched", store.System, agentRunEvent(t))
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
	// A Team pack and a package list are blobs, read only once the
	// transaction is closed.
	prepare := func(i int) error {
		if prefix, ok := rerunPackages[out[i].AgentRun.ID]; ok {
			lists := map[string]string{}
			for j, step := range out[i].Rerun.Steps {
				if step.packagesSHA == "" {
					continue
				}
				list, ok := lists[step.packagesSHA]
				if !ok {
					// A list that is gone leaves none to rebuild from, which the
					// daemon reports as an environment that could not be rebuilt.
					rc, err := s.Blobs.Get(ctx, prefix+step.packagesSHA)
					if err != nil && !errors.Is(err, storage.ErrNotFound) {
						return fmt.Errorf("reading a rerun's package list: %w", err)
					}
					if err == nil {
						b, err := io.ReadAll(rc)
						rc.Close()
						if err != nil {
							return err
						}
						list = string(b)
					}
					lists[step.packagesSHA] = list
				}
				out[i].Rerun.Steps[j].Packages = list
			}
		}
		if out[i].Coresearch != nil {
			library := skill.Service{Store: s.Store, Blobs: s.Blobs}
			skills, err := library.EnabledFolders(ctx, out[i].AgentRun.WorkspaceID, out[i].AgentRun.ResponsibleUserID)
			if err != nil {
				return err
			}
			out[i].Skills = skills
		} else if team, ok := teams[out[i].AgentRun.ID]; ok {
			conns, skills := s.teamTools(ctx, team)
			out[i].Connectors = slices.DeleteFunc(conns, func(c pack.Connector) bool { return c.Workspace != "" })
			out[i].Skills = skills
		} else if names, ok := agentSkills[out[i].AgentRun.ID]; ok {
			library := skill.Service{Store: s.Store, Blobs: s.Blobs}
			skills, err := library.EnabledFolders(ctx, out[i].AgentRun.WorkspaceID, out[i].AgentRun.ResponsibleUserID)
			if err != nil {
				return err
			}
			out[i].Skills = slices.DeleteFunc(skills, func(s toolroute.RoutedSkill) bool { return !slices.Contains(names, s.Name) })
		}
		if row, ok := soloTeams[out[i].AgentRun.ID]; ok {
			var manifest pack.Manifest
			if err := json.Unmarshal(row.Manifest, &manifest); err != nil {
				return err
			}
			var soloSkills []toolroute.RoutedSkill
			for _, member := range append([]pack.Member{manifest.Leader}, manifest.Members...) {
				_, skills := s.teamTools(ctx, TeamClaim{Pack: row, Role: member.Name, Added: true})
				for _, skill := range skills {
					if !slices.ContainsFunc(soloSkills, func(existing toolroute.RoutedSkill) bool { return existing.Name == skill.Name }) {
						soloSkills = append(soloSkills, skill)
					}
				}
			}
			out[i].Skills = slices.DeleteFunc(out[i].Skills, func(existing toolroute.RoutedSkill) bool {
				return slices.ContainsFunc(soloSkills, func(own toolroute.RoutedSkill) bool { return own.Name == existing.Name })
			})
			out[i].Skills = append(out[i].Skills, soloSkills...)
		}
		// A Workflow study's run carries the Workflow as a skill; one dropped
		// from the release is simply left out.
		if slug, ok := workflows[out[i].AgentRun.ID]; ok {
			if _, found := workflow.Get(slug); found {
				var folder bytes.Buffer
				if err := workflow.SkillTar(&folder, slug); err != nil {
					return err
				}
				out[i].Skills = append(out[i].Skills, toolroute.RoutedSkill{Name: workflow.SkillName(slug), Folder: folder.Bytes()})
			}
		}
		return nil
	}
	var prepared []Claimed
	var firstErr error
	for i := range out {
		if err := prepare(i); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			slog.Warn("claimed run could not be prepared; awaiting lease recovery", "agent_run_id", out[i].AgentRun.ID, "error", err)
			continue
		}
		prepared = append(prepared, out[i])
	}
	if len(prepared) == 0 && firstErr != nil {
		return nil, firstErr
	}
	out = prepared
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
	if t.AgentID != nil {
		cfg, err := tx.AgentCapabilities(ctx, *t.AgentID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, err
		}
		if err == nil {
			var selected []pack.Connector
			if err := json.Unmarshal(cfg.Connectors, &selected); err != nil {
				return nil, nil, err
			}
			return slices.DeleteFunc(selected, func(c pack.Connector) bool { return c.Catalogue == "" }), nil, nil
		}
	}
	out := []pack.Connector{}
	for _, e := range connectors.Catalogue {
		out = append(out, pack.Connector{Catalogue: e.Name})
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
	return s.onRunIn(ctx, user, ref, s.Store.InWorkspace, fn)
}

func (s *AgentRuns) onRunIn(ctx context.Context, user uuid.UUID, ref RuntimeRef, in func(context.Context, uuid.UUID, uuid.UUID, func(*store.Tx) error) error, fn func(*store.Tx) error) error {
	return in(ctx, ref.WorkspaceID, user, func(tx *store.Tx) error {
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
		t, err := lockAttempt(ctx, tx, ref, run)
		if err != nil {
			return err
		}
		if t.Kind == "aristotle" {
			if err := agent.LabAvailability(t.Harness); err != nil {
				return err
			}
		}
		return startLocked(ctx, tx, ref, t)
	})
}

// StartLab builds the only historical prompt sent to the CLI in the same
// requester snapshot that admits the attempt. The claim contains no answers.
func (s *AgentRuns) StartLab(ctx context.Context, user uuid.UUID, ref RuntimeRef, run uuid.UUID, history func(context.Context, *store.Tx, db.AgentRun) (string, error)) (string, error) {
	var prompt string
	err := s.onAttempt(ctx, user, ref, run, "aristotle", func(tx *store.Tx, t db.AgentRun) error {
		if err := agent.LabAvailability(t.Harness); err != nil {
			return err
		}
		if t.RequesterID == nil || *t.RequesterID != user {
			return ErrNotYours
		}
		if err := startLocked(ctx, tx, ref, t); err != nil {
			return err
		}
		var err error
		prompt, err = history(ctx, tx, t)
		return err
	})
	return prompt, err
}

func startLocked(ctx context.Context, tx *store.Tx, ref RuntimeRef, t db.AgentRun) error {
	if err := rows(tx.Q.StartAgentRun(ctx, db.StartAgentRunParams{ID: t.ID, WorkspaceID: tx.WS, RuntimeID: &ref.RuntimeID})); err != nil {
		return err
	}
	t.Status = "running"
	if err := tx.Q.StartMachineAttempt(ctx, db.StartMachineAttemptParams{WorkspaceID: tx.WS, AgentRunID: t.ID, Attempt: ref.Attempt, RuntimeID: ref.RuntimeID}); err != nil {
		return err
	}
	return emitAgentRunEvent(ctx, tx, t, "agent_run.started", store.System, agentRunEvent(t))
}

// OnAttempt runs fn on run's attempt ref, with the run locked, in the
// transaction and under the checks every daemon run operation passes: ref's
// runtime is the caller's and holds that attempt. The run's study, if it has
// one, is locked before the run, as role commands lock them.
func (s *AgentRuns) OnAttempt(ctx context.Context, user uuid.UUID, ref RuntimeRef, run uuid.UUID, fn func(*store.Tx, db.AgentRun) error) error {
	return s.onAttempt(ctx, user, ref, run, "", fn)
}

// onAttempt is OnAttempt for a run of kind, or of any kind when kind is
// empty; a run of another kind is not found.
func (s *AgentRuns) onAttempt(ctx context.Context, user uuid.UUID, ref RuntimeRef, run uuid.UUID, kind string, fn func(*store.Tx, db.AgentRun) error) error {
	// Discover the kind under the usual runtime/attempt locks. Only an Aristotle
	// hand-in needs to re-enter with a single snapshot across all live reads.
	needSnapshot := errors.New("lab hand-in requires a snapshot")
	snapshot := false
	work := func(tx *store.Tx) error {
		t, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: ref.WorkspaceID, ID: run})
		if err != nil {
			return err
		}
		if kind != "" && t.Kind != kind {
			return ErrNotYours
		}
		if t.CardID != nil {
			if _, err := tx.Q.LockCard(ctx, db.LockCardParams{ID: *t.CardID, WorkspaceID: ref.WorkspaceID}); err != nil {
				return err
			}
		}
		if t, err = lockAttempt(ctx, tx, ref, run); err != nil {
			return err
		}
		if !snapshot && t.Kind == "aristotle" {
			return needSnapshot
		}
		return fn(tx, t)
	}
	err := s.onRunIn(ctx, user, ref, s.Store.InWorkspace, work)
	if err != needSnapshot {
		return err
	}
	snapshot = true
	return s.onRunIn(ctx, user, ref, s.Store.InWorkspaceSnapshot, work)
}

func runningOn(t db.AgentRun, ref RuntimeRef) bool {
	return t.Attempt == ref.Attempt && t.Status == "running" && t.RuntimeID != nil && *t.RuntimeID == ref.RuntimeID
}

func (s *AgentRuns) Messages(ctx context.Context, user uuid.UUID, ref RuntimeRef, run uuid.UUID, ms []Message) error {
	return s.onAgentRun(ctx, user, ref, func(tx *store.Tx) error {
		t, err := lockAttempt(ctx, tx, ref, run)
		ended := false
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotYours) {
			if ended, err = endedAttemptOf(ctx, tx, t, ref); err == nil && !ended {
				err = ErrConflict
			}
		}
		if err != nil {
			return err
		}
		// A person may cancel before the daemon drains the final output.
		if !ended && !runningOn(t, ref) && !(t.Status == "cancelled" && t.StartedAt != nil && t.LateUsageAt == nil) {
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
	// An unknown runtime leaves the provider empty; the transaction below
	// rejects the ref.
	_ = s.Store.InWorkspace(ctx, ref.WorkspaceID, user, func(tx *store.Tx) error {
		rt, err := tx.Q.GetRuntime(ctx, db.GetRuntimeParams{ID: ref.RuntimeID, WorkspaceID: ref.WorkspaceID})
		r.provider = providers[rt.Harness]
		return err
	})
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
		if r.FailureReason == agent.ReasonLabContextChanged && t.Kind != "aristotle" {
			return ErrBadReport
		}
		if t.Kind == "aristotle" && t.Status != "cancelled" {
			answered, err := tx.Q.LabTurnHasAnswer(ctx, t.ID)
			if err != nil {
				return err
			}
			if !answered && r.FailureReason == agent.ReasonLabContextChanged {
				stale, err := tx.Q.LabAttemptNeedsRestart(ctx, t.ID)
				if err != nil {
					return err
				}
				if !stale {
					return ErrBadReport
				}
			}
			if ok && !answered {
				return ErrConflict
			}
			// An accepted answer is the authoritative outcome even if the
			// daemon crashed before acknowledging its hand-in.
			if answered {
				ok = true
			}
		}
		spent := 0.0
		for _, u := range r.Usage {
			cost := *u.CostUSD
			if err := tx.Q.InsertAgentRunUsage(ctx, db.InsertAgentRunUsageParams{WorkspaceID: ref.WorkspaceID, AgentRunID: run, Attempt: ref.Attempt, Model: u.Model, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens, CostUsd: cost, CostReported: u.CostReported, Estimated: u.Estimated}); err != nil {
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
			return emitAgentRunEvent(ctx, tx, out, "agent_run.usage_updated", store.System, agentRunEvent(out))
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
			if err = tx.Q.FinishAgentRun(ctx, db.FinishAgentRunParams{WorkspaceID: ref.WorkspaceID, ID: run, Status: "completed", CostUsd: spent, SessionID: r.SessionID}); err == nil {
				err = tx.Q.ResetRuntimeStreak(ctx, db.ResetRuntimeStreakParams{WorkspaceID: ref.WorkspaceID, ID: ref.RuntimeID})
			}
		case Infra(r.FailureReason) && t.CardID != nil:
			err = s.parkInfra(ctx, tx, t, r.FailureReason, r.Error, spent)
		case (Infra(r.FailureReason) || r.FailureReason == agent.ReasonLabContextChanged) && t.Attempt < t.MaxAttempts:
			// A run with no study retries a bounded number of times, then
			// fails where its requester sees it.
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
		if err = emitAgentRunEvent(ctx, tx, out, "agent_run."+out.Status, store.System, agentRunEvent(out)); err != nil {
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

// endedAttemptOf reports whether ref names an attempt of t that ended on
// ref's runtime and is no longer the run's live one: an earlier attempt, or
// the last one while the run waits to be claimed again. A daemon that
// restarted resends such an attempt's last messages after reporting its end.
func endedAttemptOf(ctx context.Context, tx *store.Tx, t db.AgentRun, ref RuntimeRef) (bool, error) {
	if ref.Attempt <= 0 || ref.Attempt > t.Attempt || ref.Attempt == t.Attempt && t.Status != "queued" {
		return false, nil
	}
	rc, err := tx.Q.GetAttemptReceipt(ctx, db.GetAttemptReceiptParams{WorkspaceID: ref.WorkspaceID, AgentRunID: t.ID, Attempt: ref.Attempt})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return rc.RuntimeID != nil && *rc.RuntimeID == ref.RuntimeID, nil
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
		t, err := tx.Q.LockAgentRun(ctx, db.LockAgentRunParams{ID: run, WorkspaceID: ref.WorkspaceID})
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
		disabled, err := s.CancelUnavailableLabInTx(ctx, tx, t)
		if err != nil {
			return err
		}
		if disabled && !terminal(status) {
			status = "cancelled"
		}
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

// cancelLocked cancels a locked open run, revokes its tokens and records it,
// as user, or as the system when user is uuid.Nil.
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
	actor := store.User(user)
	if user == uuid.Nil {
		actor = store.System
	}
	return t, emitAgentRunEvent(ctx, tx, t, "agent_run.cancelled", actor, agentRunEvent(t))
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

// tellOfflineOwners tells the owner of each machine that is offline with
// studies queued for it, once until it comes back.
func tellOfflineOwners(ctx context.Context, tx *store.Tx) error {
	offline, err := tx.Q.OfflineRuntimesWithQueuedRuns(ctx, tx.WS)
	if err != nil {
		return err
	}
	for _, m := range offline {
		text := fmt.Sprintf("%s is offline and %d queued runs wait for it. They continue once it is back online.", m.Machine, m.Queued)
		if m.Queued == 1 {
			text = fmt.Sprintf("%s is offline and a queued run waits for it. It continues once the machine is back online.", m.Machine)
		}
		ids, err := tx.Q.InsertMachineOffline(ctx, db.InsertMachineOfflineParams{WorkspaceID: tx.WS, UserID: m.OwnerID, RuntimeID: &m.ID, Text: text})
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := tx.Emit(ctx, "inbox.created", nil, store.System, map[string]any{"inbox_item_id": id}); err != nil {
				return err
			}
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
			if err := tellOfflineOwners(ctx, tx); err != nil {
				return err
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
			answered := false
			if t.Kind == "aristotle" {
				answered, err = tx.Q.LabTurnHasAnswer(ctx, t.ID)
				if err != nil {
					return err
				}
			}
			switch {
			case answered:
				err = tx.Q.FinishAgentRun(ctx, db.FinishAgentRunParams{WorkspaceID: tx.WS, ID: t.ID, Status: "completed", SessionID: t.SessionID})
			case t.CardID != nil:
				err = s.parkInfra(ctx, tx, t, reason, t.Error, 0)
			case t.Attempt < t.MaxAttempts:
				err = tx.Q.RequeueAgentRun(ctx, db.RequeueAgentRunParams{WorkspaceID: tx.WS, ID: t.ID, Error: t.Error, FailureReason: &reason})
			default:
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
			if terminal(out.Status) {
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
			if terminal(out.Status) {
				event = "agent_run." + out.Status
			}
			if err := emitAgentRunEvent(ctx, tx, out, event, store.System, agentRunEvent(out)); err != nil {
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
	var team *TeamClaim
	refs := []pack.Connector{}
	if run.Kind == "coresearch" {
		ids, err := tx.CoresearchRunConnectors(ctx, run.ID)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			row, err := tx.Q.GetWorkspaceMCPServer(ctx, db.GetWorkspaceMCPServerParams{WorkspaceID: tx.WS, ID: id})
			if err != nil {
				return nil, err
			}
			refs = append(refs, pack.Connector{Name: row.Name, Workspace: id.String()})
		}
	} else {
		var err error
		_, team, err = ClaimConnectors(ctx, tx, run)
		if err != nil {
			return nil, err
		}
	}
	if run.Kind == "coresearch" {
		// The references above are frozen for this one turn.
	} else if team != nil {
		var manifest pack.Manifest
		if err := json.Unmarshal(team.Pack.Manifest, &manifest); err != nil {
			return nil, err
		}
		member, ok := manifest.MemberFor(run.Role)
		if !ok {
			return []WorkspaceConnectorRow{}, nil
		}
		refs = member.Connectors
	} else if run.AgentID != nil {
		cfg, err := tx.AgentCapabilities(ctx, *run.AgentID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			if err := json.Unmarshal(cfg.Connectors, &refs); err != nil {
				return nil, err
			}
		} else {
			rows, err := tx.Q.ListWorkspaceMCPServers(ctx, tx.WS)
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				refs = append(refs, pack.Connector{Name: row.Name, Workspace: row.ID.String()})
			}
		}
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
				Keyless bool
			}
			if json.Unmarshal([]byte(raw), &config) != nil {
				return nil, errors.New("workspace command configuration unavailable")
			}
			if run.Kind == "coresearch" && !config.Keyless {
				return nil, errors.New("workspace command has not been declared keyless")
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

// listsModel reports whether a runtime's reported models include value.
func listsModel(models []byte, value string) bool {
	type model struct {
		Value string `json:"value"`
	}
	var list []model
	if json.Unmarshal(models, &list) != nil {
		return false
	}
	return slices.ContainsFunc(list, func(m model) bool { return m.Value == value })
}
