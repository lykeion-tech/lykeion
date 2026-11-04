package labmemory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/handin"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

// FreshTurnProblem requests a new CLI session when the old one has seen
// evidence that cannot be published under the current access or versions.
const FreshTurnProblem = "Lab evidence changed during this turn. A fresh attempt is required."

type Answers struct{ Store *store.Store }

type answerCite struct {
	Ref     string `json:"ref"`
	Title   string `json:"title"`
	Version string `json:"version"`
}
type answerEvidence struct {
	answerCite
	Via string `json:"via"`
}
type answerStatement struct {
	Kind  string       `json:"kind"`
	Text  string       `json:"text"`
	Cites []answerCite `json:"cites"`
}
type checkedAnswer struct {
	HistorySafe bool              `json:"history_safe"`
	Statements  []answerStatement `json:"statements"`
	Evidence    []answerEvidence  `json:"evidence"`
}

// Include the visible projection as well as its source version. A study or
// lesson can remain open while part of its evidence becomes private or changes.
func pin(f Fact) answerCite {
	data, _ := json.Marshal(f)
	return answerCite{Ref: f.Ref.String(), Title: f.Title, Version: fmt.Sprintf("%x", sha256.Sum256(data))}
}

// Answer runs inside the authenticated, locked attempt, under the requester's
// RLS identity. Read also checks the facts behind each checked, active lesson.
func (a Answers) Answer(ctx context.Context, tx *store.Tx, run db.AgentRun, raw json.RawMessage) ([]string, error) {
	if run.Kind != "aristotle" || run.WorkspaceID != tx.WS || run.RequesterID == nil || run.LabChatID == nil {
		return nil, service.ErrNotYours
	}
	asker := *run.RequesterID
	if _, err := tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: tx.WS, UserID: asker}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, service.ErrNotYours
		}
		return nil, err
	}
	allowed, err := tx.Q.LabAsker(ctx, db.LabAskerParams{WorkspaceID: tx.WS, UserID: asker})
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, service.ErrNotYours
	}
	seen, err := tx.Q.LabTurnHasAnswer(ctx, run.ID)
	if err != nil || seen {
		return nil, err
	}
	if run.Status != "running" {
		return nil, service.ErrConflict
	}
	stale, err := tx.Q.LabAttemptNeedsRestart(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	if stale {
		return []string{FreshTurnProblem}, nil
	}
	denyStale := func() ([]string, error) {
		n, e := tx.Q.MarkLabAttemptStale(ctx, run.ID)
		if e != nil {
			return nil, e
		}
		if n != 1 {
			return nil, service.ErrConflict
		}
		return []string{FreshTurnProblem}, nil
	}
	safe, err := launchStillVisible(ctx, tx, run)
	if err != nil {
		return nil, err
	}
	if !safe {
		return denyStale()
	}
	log, err := tx.Q.LabEvidenceLog(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	titles := map[Ref]answerCite{}
	missing := map[Ref]bool{}
	lookup := func(ref Ref) (answerCite, bool, error) {
		if c, ok := titles[ref]; ok {
			return c, true, nil
		}
		if missing[ref] {
			return answerCite{}, false, nil
		}
		f, e := Read(ctx, tx, asker, ref)
		if errors.Is(e, ErrNotFound) {
			missing[ref] = true
			return answerCite{}, false, nil
		}
		if e != nil {
			return answerCite{}, false, e
		}
		c := pin(f)
		titles[ref] = c
		return c, true, nil
	}
	result := checkedAnswer{Evidence: []answerEvidence{}, HistorySafe: true}
	readHere := map[Ref]bool{}
	for _, row := range log {
		ref, e := ParseRef(row.Ref)
		if e != nil {
			return denyStale()
		}
		cite, visible, e := lookup(ref)
		if e != nil {
			return nil, e
		}
		// The old session can repeat content from any of its reads, including
		// an uncited source in a gap. Rereading cannot erase that exposure.
		navigationOnly := row.Via == "links" && ref.Kind == "study" && row.ReadVersion == studyDirectoryPin
		if !visible || (!navigationOnly && row.ReadVersion != cite.Version) {
			return denyStale()
		}
		readHere[ref] = true
		via := row.Via
		switch via {
		case "read", "search", "links":
		default:
			from, e := ParseRef(via)
			if e != nil {
				return denyStale()
			}
			if _, visible, e = lookup(from); e != nil {
				return nil, e
			}
			if !visible {
				return denyStale()
			}
		}
		result.Evidence = append(result.Evidence, answerEvidence{cite, via})
	}
	h, probs := handin.Parse(handin.Aristotle, raw)
	if len(probs) > 0 {
		return probs, nil
	}
	for i, s := range h.Statements {
		stmt := answerStatement{Kind: s.Kind, Text: s.Text, Cites: []answerCite{}}
		problem := func(p string) { probs = append(probs, fmt.Sprintf("statement %d: %s", i+1, p)) }
		switch {
		case s.Kind == "fact" && len(s.Cites) == 0:
			problem("a fact needs at least one fact reference")
		case s.Kind == "lesson" && len(s.Cites) != 1:
			problem("a lesson needs exactly one lesson reference")
		case s.Kind == "gap" && len(s.Cites) != 0:
			problem("a gap has no references")
		}
		for _, sref := range s.Cites {
			ref, e := ParseRef(sref)
			switch {
			case e != nil:
				problem("invalid reference")
			case s.Kind == "gap":
				continue
			case (s.Kind == "lesson") != (ref.Kind == "lesson"):
				problem("use fact references for facts and lesson references for lessons")
			case !readHere[ref]:
				problem("the reference must have been read in this run and must still be available")
			default:
				stmt.Cites = append(stmt.Cites, titles[ref])
			}
		}
		result.Statements = append(result.Statements, stmt)
	}
	if len(probs) > 0 {
		return probs, nil
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	id, err := tx.Q.RecordLabAnswer(ctx, db.RecordLabAnswerParams{RunID: run.ID, Answer: data})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, service.ErrConflict
	}
	if err != nil {
		return nil, err
	}
	return nil, emitAnswerEvent(ctx, tx, run, id)
}

func emitAnswerEvent(ctx context.Context, tx *store.Tx, run db.AgentRun, id uuid.UUID) error {
	return tx.Emit(ctx, "lab.message.created", nil, store.System, map[string]any{"message_id": id, "chat_id": *run.LabChatID, "owner_id": *run.RequesterID})
}

// Never copy daemon diagnostics, prompts, or model text into failure messages.
func failureBody(reason *string) string {
	why := "the run failed"
	if reason != nil {
		switch *reason {
		case agent.ReasonLabContextChanged:
			why = "lab evidence changed during the turn. Ask again to use the latest evidence"
		case agent.ReasonHandInInvalid:
			why = "the answer did not pass its citation checks"
		case agent.ReasonRuntimeOffline:
			why = "your machine went offline"
		case agent.ReasonSignIn:
			why = "your machine needs to sign in"
		case agent.ReasonContextExhausted:
			why = "the conversation exceeded the model's context limit"
		case agent.ReasonProviderNetwork:
			why = "your machine could not reach the model provider"
		case "timeout":
			why = "the run timed out"
		case agent.ReasonDoor:
			why = "the lab tools were unavailable"
		case agent.ReasonCLIMissing, agent.ReasonCLIBroken, agent.ReasonCLITooOld:
			why = "the agent CLI was unavailable"
		case agent.ReasonSandbox, agent.ReasonFenceBreach:
			why = "the run could not use a safe environment"
		}
	}
	return "Aristotle could not answer: " + why + "."
}
func (a Answers) OnSettlement(ctx context.Context, n service.Settlement) error {
	r := n.AgentRun
	if r.Kind != "aristotle" || (r.Status != "failed" && r.Status != "cancelled") || r.RequesterID == nil || r.LabChatID == nil {
		return nil
	}
	return a.Store.InWorkspace(ctx, r.WorkspaceID, *r.RequesterID, func(tx *store.Tx) error {
		if _, e := tx.Q.LockWorkspaceExecution(ctx, tx.WS); e != nil {
			return e
		}
		current, e := tx.Q.LockAgentRun(ctx, db.LockAgentRunParams{WorkspaceID: tx.WS, ID: r.ID})
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		if (current.Status != "failed" && current.Status != "cancelled") || current.Kind != "aristotle" || current.RequesterID == nil || *current.RequesterID != *r.RequesterID || current.LabChatID == nil {
			return nil
		}
		// A removed member still owns the private chat. Record only a fixed
		// terminal message, so it is present if they later rejoin. Access to
		// the chat remains guarded by membership at the API boundary.
		body := failureBody(current.FailureReason)
		if current.Status == "cancelled" {
			body = "Aristotle could not answer: the run was cancelled."
		}
		id, e := tx.Q.RecordLabFailure(ctx, db.RecordLabFailureParams{RunID: r.ID, Body: body})
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		return emitAnswerEvent(ctx, tx, current, id)
	})
}
func (a Answers) SettlementHook(next func(context.Context, service.Settlement) error) func(context.Context, service.Settlement) error {
	return func(ctx context.Context, n service.Settlement) error {
		if n.AgentRun.Kind == "aristotle" {
			return a.OnSettlement(ctx, n)
		}
		if next != nil {
			return next(ctx, n)
		}
		return nil
	}
}
