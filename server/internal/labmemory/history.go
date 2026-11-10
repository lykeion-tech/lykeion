package labmemory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/handin"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// LaunchPrompt is called only from the serializable requester transaction at
// start. Stored answer bodies, evidence titles and unpinned legacy answers are
// never copied into the model's input.
func LaunchPrompt(ctx context.Context, tx *store.Tx, run db.AgentRun) (string, error) {
	if run.RequesterID == nil || run.Kind != "aristotle" || run.LabChatID == nil {
		return "", service.ErrNotYours
	}
	asker := *run.RequesterID
	if _, err := tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: tx.WS, UserID: asker}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", service.ErrNotYours
		}
		return "", err
	}
	if run.LabPurpose == ManualDraftPurpose {
		question, e := tx.Q.GetLabQuestion(ctx, run.ID)
		if e != nil {
			return "", e
		}
		if _, e = DraftQuestionStudy(ctx, tx, asker, question.Body, question.ContextCardID); e != nil {
			return "", e
		}
	}
	// Dispatched attempts from an older server may still contain frozen history.
	// Rebuild the base exclusively from the current question, even on upgrade.
	run, err := tx.Q.RefreshLabRunPrompt(ctx, run.ID)
	if err != nil {
		return "", err
	}
	history, sources, err := currentHistory(ctx, tx, run)
	if err != nil {
		return "", err
	}
	n, err := tx.Q.RecordLabLaunchSources(ctx, db.RecordLabLaunchSourcesParams{Sources: sources, RunID: run.ID})
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", service.ErrConflict
	}
	if len(history) == 0 {
		return run.Prompt, nil
	}
	return run.Prompt + "\n\nEarlier conversation:\n" + strings.Join(history, "\n"), nil
}

// currentHistory checks the chain of earlier answers both at start and before publishing
// an answer. A model can use any answer it saw, even if it omits its citations.
func currentHistory(ctx context.Context, tx *store.Tx, run db.AgentRun) ([]string, []uuid.UUID, error) {
	asker := *run.RequesterID
	if run.LabPurpose == ManualDraftPurpose {
		chat, e := tx.Q.GetPrivateLabChat(ctx, *run.LabChatID)
		if e != nil {
			return nil, nil, e
		}
		ok, e := DraftReadable(ctx, tx, chat)
		if e != nil {
			return nil, nil, e
		}
		if !ok {
			return nil, nil, ErrNotFound
		}
	}
	rows, err := tx.Q.LabPriorTurns(ctx, run.ID)
	if err != nil {
		return nil, nil, err
	}
	versions := make(map[string]string)
	current := func(c answerCite) (bool, error) {
		if c.Version == "" {
			return false, nil
		}
		version, ok := versions[c.Ref]
		if !ok {
			r, e := ParseRef(c.Ref)
			if e != nil {
				return false, nil
			}
			fact, e := Read(ctx, tx, asker, r)
			if e != nil && !errors.Is(e, ErrNotFound) {
				return false, e
			}
			if e == nil {
				version = pin(fact).Version
			}
			versions[c.Ref] = version
		}
		return c.Version == version, nil
	}
	var history []string
	// An answer could repeat something previously shown without citing it.
	// Check its actual launch dependencies, not unrelated turns in the chat.
	included := map[uuid.UUID]bool{}
	sources := []uuid.UUID{}
	for _, row := range rows {
		if row.Kind == "question" {
			if run.LabPurpose == ManualDraftPurpose {
				if _, e := DraftQuestionStudy(ctx, tx, asker, row.Question, row.ContextCardID); e != nil {
					if errors.Is(e, ErrNotFound) {
						continue
					}
					return nil, nil, e
				}
			}
			history = append(history, "question: "+row.Question)
			continue
		}
		if row.LaunchSources == nil {
			continue
		}
		permitted := true
		for _, source := range row.LaunchSources {
			if !included[source] {
				permitted = false
				break
			}
		}
		if !permitted {
			continue
		}
		lines, valid, e := checkedHistory(row, current)
		if e != nil {
			return nil, nil, e
		}
		if valid {
			history = append(history, lines...)
			sources = append(sources, row.ID)
			included[row.ID] = true
		}
	}
	return history, sources, nil
}

func launchStillVisible(ctx context.Context, tx *store.Tx, run db.AgentRun) (bool, error) {
	launched, err := tx.Q.LabLaunchedSources(ctx, run.ID)
	if err != nil {
		return false, err
	}
	// A missing launch receipt cannot vouch for what this session saw.
	if launched == nil {
		return false, nil
	}
	if len(launched) == 0 {
		return true, nil
	}
	_, current, err := currentHistory(ctx, tx, run)
	if err != nil {
		return false, err
	}
	visible := map[uuid.UUID]bool{}
	for _, id := range current {
		visible[id] = true
	}
	for _, id := range launched {
		if !visible[id] {
			return false, nil
		}
	}
	return true, nil
}

func checkedHistory(row db.LabPriorTurnsRow, current func(answerCite) (bool, error)) ([]string, bool, error) {
	var evidence []answerEvidence
	var statements []answerStatement
	if !row.HistorySafe || json.Unmarshal(row.Evidence, &evidence) != nil || json.Unmarshal(row.Statements, &statements) != nil || (len(statements) == 0 && len(row.Proposal) == 0) {
		return nil, false, nil
	}
	visible := map[string]string{}
	for _, e := range evidence {
		ok, err := current(e.answerCite)
		if !ok || err != nil {
			return nil, false, err
		}
		visible[e.Ref] = e.Version
	}
	var lines []string
	for _, s := range statements {
		if s.Kind == "gap" && len(s.Cites) != 0 || s.Kind == "fact" && len(s.Cites) == 0 || s.Kind == "lesson" && len(s.Cites) != 1 {
			return nil, false, nil
		}
		if s.Kind != "gap" && s.Kind != "fact" && s.Kind != "lesson" {
			return nil, false, nil
		}
		for _, c := range s.Cites {
			if ref, e := ParseRef(c.Ref); e == nil && ref.Node != "" && c.Version != "" {
				// A report node stands on its report having been read and on
				// the node's own current projection.
				if _, read := visible[ref.Base().String()]; !read {
					return nil, false, nil
				}
				if ok, err := current(c); !ok || err != nil {
					return nil, false, err
				}
				continue
			}
			if c.Version == "" || visible[c.Ref] != c.Version {
				return nil, false, nil
			}
		}
		lines = append(lines, "answer ("+s.Kind+"): "+s.Text)
	}
	if len(row.Proposal) > 0 {
		var p DraftProposal
		if json.Unmarshal(row.Proposal, &p) != nil || len(handin.ValidateManualDraft(p.Draft, false)) > 0 {
			return nil, false, nil
		}
		if p.Draft.CardID != nil {
			if _, ok := visible["study:"+*p.Draft.CardID]; !ok {
				return nil, false, nil
			}
		}
		lines = append(lines, "proposal (not a fact): "+string(row.Proposal))
	}
	return lines, true, nil
}
