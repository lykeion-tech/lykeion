package labmemory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
	// Dispatched attempts from an older server may still contain frozen history.
	// Rebuild the base exclusively from the current question, even on upgrade.
	run, err := tx.Q.RefreshLabRunPrompt(ctx, run.ID)
	if err != nil {
		return "", err
	}
	rows, err := tx.Q.LabPriorTurns(ctx, run.ID)
	if err != nil {
		return "", err
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
			return "", e
		}
		if valid {
			history = append(history, lines...)
			sources = append(sources, row.ID)
			included[row.ID] = true
		}
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

func checkedHistory(row db.LabPriorTurnsRow, current func(answerCite) (bool, error)) ([]string, bool, error) {
	var evidence []answerEvidence
	var statements []answerStatement
	if !row.HistorySafe || json.Unmarshal(row.Evidence, &evidence) != nil || json.Unmarshal(row.Statements, &statements) != nil || len(statements) == 0 {
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
			if c.Version == "" || visible[c.Ref] != c.Version {
				return nil, false, nil
			}
		}
		lines = append(lines, "answer ("+s.Kind+"): "+s.Text)
	}
	return lines, true, nil
}
