package labmemory

import (
	"context"
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

type Answers struct{ Store *store.Store }

type answerCite struct {
	Ref   string `json:"ref"`
	Title string `json:"title"`
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
	Statements []answerStatement `json:"statements"`
	Evidence   []answerEvidence  `json:"evidence"`
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
	h, probs := handin.Parse(handin.Aristotle, raw)
	if len(probs) > 0 {
		return probs, nil
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
		c := answerCite{ref.String(), f.Title}
		titles[ref] = c
		return c, true, nil
	}
	result := checkedAnswer{Evidence: []answerEvidence{}}
	readHere := map[Ref]bool{}
	for _, row := range log {
		ref, e := ParseRef(row.Ref)
		if e != nil {
			continue
		}
		cite, visible, e := lookup(ref)
		if e != nil {
			return nil, e
		}
		if !visible {
			continue
		}
		readHere[ref] = true
		via := row.Via
		switch via {
		case "read", "search", "links":
		default:
			from, e := ParseRef(row.Via)
			if e != nil {
				via = ""
				break
			}
			if _, visible, e = lookup(from); e != nil {
				return nil, e
			}
			if !visible {
				via = ""
			}
		}
		result.Evidence = append(result.Evidence, answerEvidence{cite, via})
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
	if r.Kind != "aristotle" || r.Status != "failed" || r.RequesterID == nil || r.LabChatID == nil {
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
		if current.Status != "failed" || current.Kind != "aristotle" || current.RequesterID == nil || *current.RequesterID != *r.RequesterID || current.LabChatID == nil {
			return nil
		}
		if _, e = tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: tx.WS, UserID: *r.RequesterID}); errors.Is(e, pgx.ErrNoRows) {
			return nil
		} else if e != nil {
			return e
		}
		id, e := tx.Q.RecordLabFailure(ctx, db.RecordLabFailureParams{RunID: r.ID, Body: failureBody(current.FailureReason)})
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
