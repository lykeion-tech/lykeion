package handin

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// memberWorking is the problem a lead's hand-in meets while a member's run
// of its column has not ended.
const memberWorking = "a member is still working: wait for its result with member_result"

// HandIns takes the hand-ins daemons post for their runs.
type HandIns struct {
	Runs *service.AgentRuns
	// SentBack is what the review before the study's round sent back; nil
	// reads as nothing sent back.
	SentBack func(context.Context, *store.Tx, db.Card) (Review, error)
	// Autoresearch applies an Autoresearch run's hand-in. It is a field because
	// the autoresearch package imports this one.
	Autoresearch AutoresearchOps
	Aristotle    AristotleOps
}

type DraftOps interface {
	Draft(context.Context, *store.Tx, db.AgentRun, json.RawMessage) ([]string, error)
}

type AristotleOps interface {
	Answer(context.Context, *store.Tx, db.AgentRun, json.RawMessage) ([]string, error)
}

// AutoresearchOps are the two things an Autoresearch run's hand-in does.
type AutoresearchOps interface {
	Report(ctx context.Context, tx *store.Tx, run uuid.UUID, text string) error
	OpenCardForAgentRun(ctx context.Context, tx *store.Tx, run uuid.UUID, title, question string) (db.Card, error)
}

type handInBody struct {
	AgentRunID               uuid.UUID       `json:"agent_run_id"`
	Attempt                  int32           `json:"attempt"`
	HandIn                   json.RawMessage `json:"hand_in"`
	MessageSeq               int32           `json:"message_seq,omitempty"`
	EnvironmentFailures      []string        `json:"environment_failures,omitempty"`
	EnvironmentFailureMember *uuid.UUID      `json:"environment_failure_member,omitempty"`
	ConvertedRecords         []uuid.UUID     `json:"converted_records,omitempty"`
}

// Submit checks the hand-in of run's attempt ref.Attempt and, when nothing is
// wrong with it, records it and the records it becomes, in one transaction
// with the study locked. Each problem is one plain line for the agent; with
// any, no hand-in is accepted. A lab attempt that saw revoked content is
// marked for a fresh session. A hand-in already recorded for the attempt is
// accepted again and writes nothing.
func (s *HandIns) Submit(ctx context.Context, user uuid.UUID, ref service.RuntimeRef, run uuid.UUID, raw json.RawMessage) ([]string, error) {
	return s.SubmitWithMessageSeq(ctx, user, ref, run, raw, 0)
}

func (s *HandIns) SubmitWithMessageSeq(ctx context.Context, user uuid.UUID, ref service.RuntimeRef, run uuid.UUID, raw json.RawMessage, messageSeq int32) ([]string, error) {
	return s.SubmitWithEnvironment(ctx, user, ref, run, raw, messageSeq, nil)
}

// SubmitWithEnvironment records daemon-attested environment failures with this attempt.
func (s *HandIns) SubmitWithEnvironment(ctx context.Context, user uuid.UUID, ref service.RuntimeRef, run uuid.UUID, raw json.RawMessage, messageSeq int32, failures []string) ([]string, error) {
	var probs []string
	var unavailable bool
	err := s.Runs.OnAttempt(ctx, user, ref, run, func(tx *store.Tx, t db.AgentRun) error {
		probs = nil // A serialization retry starts a fresh validation.
		k, ok := KindFor(t.Kind, t.Role, t.BoardColumn)
		if !ok {
			return service.ErrConflict
		}
		if len(failures) > 3 {
			probs = append(probs, "environment_failures: at most three languages")
		}
		seen := make(map[string]bool, len(failures))
		for _, language := range failures {
			if !slices.Contains([]string{"python", "r", "julia"}, language) || seen[language] {
				probs = append(probs, "environment_failures: invalid or duplicate language")
				break
			}
			seen[language] = true
		}
		if len(probs) > 0 {
			return nil
		}
		if k == Aristotle {
			var err error
			unavailable, err = s.Runs.CancelUnavailableLabInTx(ctx, tx, t)
			if err != nil || unavailable {
				return err
			}
			if s.Aristotle == nil || t.RequesterID == nil || *t.RequesterID != user {
				return service.ErrNotYours
			}
			if t.LabPurpose == string(ManualDecisionDraft) {
				drafts, ok := s.Aristotle.(DraftOps)
				if !ok {
					return service.ErrConflict
				}
				probs, err = drafts.Draft(ctx, tx, t, raw)
			} else {
				probs, err = s.Aristotle.Answer(ctx, tx, t, raw)
			}
			return err
		}
		if k == Autoresearch {
			// A kept hand-in replayed after the run settled was applied
			// already when its report is written: accept it again.
			if t.Status != "running" {
				r, err := tx.Q.RunByAgentRun(ctx, db.RunByAgentRunParams{WorkspaceID: tx.WS, AgentRunID: t.ID})
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
				if err == nil && r.Report != "" {
					return nil
				}
				return service.ErrConflict
			}
			var err error
			probs, err = s.autoresearch(ctx, tx, t.ID, raw)
			return err
		}
		if k == Report {
			var err error
			probs, err = submitReport(ctx, tx, t, raw, messageSeq)
			return err
		}
		if t.CardID == nil {
			probs = []string{"not yet supported"}
			return nil
		}
		w := service.Writer{Card: *t.CardID, AgentRun: t.ID, Column: t.BoardColumn, Role: t.Role, Round: t.Round}
		var study []db.Record
		for _, col := range board.Stages {
			recs, err := tx.Q.ListRecords(ctx, db.ListRecordsParams{WorkspaceID: tx.WS, CardID: w.Card, BoardColumn: col})
			if err != nil {
				return err
			}
			study = append(study, recs...)
		}
		for _, r := range study {
			if r.Kind == "hand_in" && r.AgentRunID != nil && *r.AgentRunID == run && records.Body[handInBody](r).Attempt == t.Attempt {
				return nil
			}
		}
		if t.Status != "running" {
			return service.ErrConflict
		}
		h, shape := Parse(k, raw)
		if probs = shape; len(probs) > 0 {
			return nil
		}
		attested := slices.Clone(failures)
		var failureMember *uuid.UUID
		if h.BlockedByMemberRun != nil {
			memberID, _ := uuid.Parse(*h.BlockedByMemberRun)
			ok, err := delegatedEnvironmentFailure(ctx, tx, t, study, memberID, deref(h.BlockedByEnvironment))
			if err != nil {
				return err
			}
			if !ok {
				probs = append(probs, "blocked_by_member_run: no matching attested failure for this delegated member")
			} else {
				failureMember = &memberID
				if !seen[*h.BlockedByEnvironment] {
					attested = append(attested, *h.BlockedByEnvironment)
					seen[*h.BlockedByEnvironment] = true
				}
			}
		}
		if h.BlockedByEnvironment != nil && !seen[*h.BlockedByEnvironment] {
			probs = append(probs, "blocked_by_environment: the daemon has not confirmed this environment failure")
		}
		if len(probs) > 0 {
			return nil
		}
		lead := Leads(t.Role)
		if lead {
			_, err := tx.Q.InFlightMemberRun(ctx, db.InFlightMemberRunParams{WorkspaceID: tx.WS, CardID: w.Card, BoardColumn: w.Column, Round: w.Round})
			switch {
			case err == nil:
				probs = append(probs, memberWorking)
			case !errors.Is(err, pgx.ErrNoRows):
				return err
			}
		}
		if lead && h.Decision != nil && h.Decision.FromMember != nil {
			p, err := teamMember(ctx, tx, w, *h.Decision.FromMember)
			if err != nil {
				return err
			}
			probs = append(probs, p...)
		}
		var prev Review
		if lead && s.SentBack != nil {
			c, err := tx.Q.GetCard(ctx, db.GetCardParams{WorkspaceID: tx.WS, ID: w.Card})
			if err != nil {
				return err
			}
			// The review a run answers is the one before its own round, even
			// when the study has moved on since.
			c.BoardColumn, c.Round = t.BoardColumn, t.Round
			if prev, err = s.SentBack(ctx, tx, c); err != nil {
				return err
			}
		}
		ev := NewEvidence(study)
		ev.Mode = ModeOf(study)
		recs, more := Convert(k, lead, h, ev, prev)
		if probs = append(probs, more...); len(probs) > 0 {
			return nil
		}
		var converted []uuid.UUID
		for _, r := range recs {
			rec, err := records.WriteAs(ctx, tx, w, r.Kind, r.Body)
			if err != nil {
				return err
			}
			converted = append(converted, rec.ID)
		}
		if _, err := records.WriteAs(ctx, tx, w, "hand_in", handInBody{AgentRunID: t.ID, Attempt: t.Attempt, HandIn: raw, MessageSeq: messageSeq, EnvironmentFailures: attested, EnvironmentFailureMember: failureMember, ConvertedRecords: converted}); err != nil {
			return err
		}
		return nil
	})
	// Return a terminal verdict only after the cancellation transaction commits.
	if err == nil && unavailable {
		return nil, service.ErrConflict
	}
	return probs, err
}

// submitReport records a report run's hand-in as the run's hand_in record and
// writes nothing else: its study is finished, and the report reads the
// hand-in once the run settles. The attempt's hand-in, once kept, is accepted
// again and writes nothing.
func submitReport(ctx context.Context, tx *store.Tx, t db.AgentRun, raw json.RawMessage, messageSeq int32) ([]string, error) {
	recs, err := tx.Q.AgentRunRecords(ctx, db.AgentRunRecordsParams{WorkspaceID: tx.WS, AgentRunID: &t.ID})
	if err != nil {
		return nil, err
	}
	for _, r := range recs {
		if r.Kind == "hand_in" && records.Body[handInBody](r).Attempt == t.Attempt {
			return nil, nil
		}
	}
	if t.Status != "running" {
		return nil, service.ErrConflict
	}
	if _, probs := ParseReport(raw); len(probs) > 0 {
		return probs, nil
	}
	w := service.Writer{Card: *t.CardID, AgentRun: t.ID, Column: t.BoardColumn, Role: t.Role, Round: t.Round}
	_, err = records.WriteAs(ctx, tx, w, "hand_in", handInBody{AgentRunID: t.ID, Attempt: t.Attempt, HandIn: raw, MessageSeq: messageSeq})
	return nil, err
}

// delegatedEnvironmentFailure verifies the exact member ticket and its current attested hand-in.
func delegatedEnvironmentFailure(ctx context.Context, tx *store.Tx, leader db.AgentRun, study []db.Record, memberID uuid.UUID, language string) (bool, error) {
	if leader.Role != "leader" || leader.CardID == nil || memberID == uuid.Nil || language == "" {
		return false, nil
	}
	member, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: tx.WS, ID: memberID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if member.CardID == nil || *member.CardID != *leader.CardID || member.BoardColumn != leader.BoardColumn || member.Round != leader.Round || member.Status != "completed" || slices.Contains([]string{"", "leader", "agent", "auditor", "objector", "rerun"}, member.Role) {
		return false, nil
	}
	delegated, attested := false, false
	for _, record := range study {
		if record.BoardColumn != leader.BoardColumn || record.Round != leader.Round || record.AgentRunID == nil {
			continue
		}
		if record.Kind == "delegation" && *record.AgentRunID == leader.ID {
			var body struct {
				AgentRunID    uuid.UUID `json:"agent_run_id"`
				LeaderAttempt int32     `json:"leader_attempt"`
			}
			if json.Unmarshal(record.Body, &body) == nil && body.AgentRunID == memberID && body.LeaderAttempt == leader.Attempt {
				delegated = true
			}
		}
		if record.Kind == "hand_in" && *record.AgentRunID == memberID {
			body := records.Body[handInBody](record)
			var hand HandIn
			if body.Attempt == member.Attempt && json.Unmarshal(body.HandIn, &hand) == nil && hand.Status == "could_not_do" && deref(hand.BlockedByEnvironment) == language && slices.Contains(body.EnvironmentFailures, language) {
				attested = true
			}
		}
	}
	return delegated && attested, nil
}

// teamMember is the problem with a decision raised for member: it must be
// one of the column Team's members, and a column with no Team has none.
func teamMember(ctx context.Context, tx *store.Tx, w service.Writer, member string) ([]string, error) {
	packID, err := tx.Q.GetCardTeamPack(ctx, db.GetCardTeamPackParams{WorkspaceID: tx.WS, CardID: w.Card, BoardColumn: w.Column})
	if errors.Is(err, pgx.ErrNoRows) {
		return []string{"decision: from_member must be null: you work this column alone"}, nil
	}
	if err != nil {
		return nil, err
	}
	row, err := tx.Q.GetFieldPack(ctx, db.GetFieldPackParams{WorkspaceID: tx.WS, ID: packID})
	if err != nil {
		return nil, err
	}
	var m pack.Manifest
	if err := json.Unmarshal(row.Manifest, &m); err != nil {
		return nil, err
	}
	for _, x := range m.Members {
		if x.Name == member {
			return nil, nil
		}
	}
	return []string{"decision: from_member must name one of your Team's members, or be null"}, nil
}
