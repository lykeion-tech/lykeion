package store

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

type StudyDraft struct {
	ResearchID *uuid.UUID
	StartDate  *time.Time
	Deadline   *time.Time
	CardID     *uuid.UUID
	Completed  bool
	Priority   string
}

func (t *Tx) CreateStudyDraft(ctx context.Context, id uuid.UUID, d StudyDraft) error {
	if d.Priority == "" {
		d.Priority = "none"
	}
	_, err := t.raw.Exec(ctx, `INSERT INTO study_draft_request (workspace_id, autoresearch_id, research_id, start_date, deadline, priority) VALUES ($1,$2,$3,$4,$5,$6)`, t.WS, id, d.ResearchID, d.StartDate, d.Deadline, d.Priority)
	return err
}
func (t *Tx) LockStudyDraft(ctx context.Context, id uuid.UUID) (*StudyDraft, error) {
	var d StudyDraft
	err := t.raw.QueryRow(ctx, `SELECT research_id, start_date, deadline, card_id, completed, priority FROM study_draft_request WHERE workspace_id=$1 AND autoresearch_id=$2 FOR UPDATE`, t.WS, id).Scan(&d.ResearchID, &d.StartDate, &d.Deadline, &d.CardID, &d.Completed, &d.Priority)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}
func (t *Tx) CompleteStudyDraft(ctx context.Context, id, card uuid.UUID, d StudyDraft) error {
	if _, err := t.raw.Exec(ctx, `UPDATE card SET research_id=$3,start_date=$4,deadline=$5,priority=$6 WHERE workspace_id=$1 AND id=$2`, t.WS, card, d.ResearchID, d.StartDate, d.Deadline, d.Priority); err != nil {
		return err
	}
	_, err := t.raw.Exec(ctx, `UPDATE study_draft_request SET card_id=$3, completed=true WHERE workspace_id=$1 AND autoresearch_id=$2`, t.WS, id, card)
	return err
}
