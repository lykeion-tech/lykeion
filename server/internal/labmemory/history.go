package labmemory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/fence"
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
	researcher, err := tx.Q.GetUser(ctx, asker)
	if err != nil {
		return "", err
	}
	prompt := fence.Researcher(researcher.Name, asker.String()) + "\nResearcher's question:\n" + run.Prompt
	if len(history) > 0 {
		prompt += "\n\nEarlier conversation:\n" + strings.Join(history, "\n")
	}
	return prompt, nil
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
	facts := map[string]*Fact{}
	current := func(e answerEvidence) (ok, changed bool, err error) {
		fact, seen := facts[e.Ref]
		if !seen {
			r, err := ParseRef(e.Ref)
			if err != nil {
				return false, false, nil
			}
			f, err := Read(ctx, tx, asker, r)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return false, false, err
			}
			if err == nil {
				fact = &f
			}
			facts[e.Ref] = fact
		}
		if fact == nil {
			return false, false, nil
		}
		return stands(ctx, tx, *fact, e)
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
		lines, valid, changed, e := checkedHistory(row, current)
		if e != nil {
			return nil, nil, e
		}
		if valid {
			if changed {
				lines = append(lines, "(The sources of this answer have changed since. Read them again before repeating it.)")
			}
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

// checkedHistory also reports whether a source the answer read or cites has
// changed. Sources it only listed by title do not count.
func checkedHistory(row db.LabPriorTurnsRow, current func(answerEvidence) (ok, changed bool, err error)) ([]string, bool, bool, error) {
	var evidence []answerEvidence
	var statements []answerStatement
	if !row.HistorySafe || json.Unmarshal(row.Evidence, &evidence) != nil || json.Unmarshal(row.Statements, &statements) != nil || (len(statements) == 0 && len(row.Proposal) == 0) {
		return nil, false, false, nil
	}
	// A source can appear at several versions; a cite stands on one of them.
	visible := map[string]map[string]bool{}
	opened := map[string]bool{}
	var changed bool
	outgrown := map[answerCite]bool{}
	for _, e := range evidence {
		if e.Via == "read" {
			opened[e.Ref] = true
		}
		ok, moved, err := current(e)
		if !ok || err != nil {
			return nil, false, false, err
		}
		if visible[e.Ref] == nil {
			visible[e.Ref] = map[string]bool{}
		}
		visible[e.Ref][e.Version] = true
		changed = changed || moved && e.Via == "read"
		outgrown[answerCite{Ref: e.Ref, Version: e.Version}] = moved
	}
	var lines []string
	for _, s := range statements {
		if s.Kind == "gap" && len(s.Cites) != 0 || s.Kind == "fact" && len(s.Cites) == 0 || s.Kind == "lesson" && len(s.Cites) != 1 {
			return nil, false, false, nil
		}
		if s.Kind != "gap" && s.Kind != "fact" && s.Kind != "lesson" {
			return nil, false, false, nil
		}
		for _, c := range s.Cites {
			if ref, e := ParseRef(c.Ref); e == nil && ref.Node != "" && c.Version != "" {
				// A report node stands on its report, or the node itself,
				// having been read and on the node's own current projection.
				if !opened[ref.Base().String()] && !opened[c.Ref] {
					return nil, false, false, nil
				}
				if ok, _, err := current(answerEvidence{answerCite: c}); !ok || err != nil {
					return nil, false, false, err
				}
				continue
			}
			if c.Version == "" || !visible[c.Ref][c.Version] {
				return nil, false, false, nil
			}
			changed = changed || outgrown[answerCite{Ref: c.Ref, Version: c.Version}]
		}
		lines = append(lines, "answer ("+s.Kind+"): "+s.Text)
	}
	if len(row.Proposal) > 0 {
		var proposal struct {
			NewStudy *StudySuggestion `json:"new_study"`
		}
		if json.Unmarshal(row.Proposal, &proposal) != nil {
			return nil, false, false, nil
		}
		if proposal.NewStudy != nil {
			if len(statements) == 0 || proposal.NewStudy.Description == "" {
				return nil, false, false, nil
			}
			return append(lines, "Suggested new study: "+proposal.NewStudy.Description), true, changed, nil
		}
		var p DraftProposal
		if json.Unmarshal(row.Proposal, &p) != nil || len(handin.ValidateManualDraft(p.Draft, false)) > 0 {
			return nil, false, false, nil
		}
		if p.Draft.CardID != nil {
			if _, ok := visible["study:"+*p.Draft.CardID]; !ok {
				return nil, false, false, nil
			}
		}
		lines = append(lines, "proposal (not a fact): "+string(row.Proposal))
	}
	return lines, true, changed, nil
}
