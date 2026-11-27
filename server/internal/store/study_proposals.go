package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ProposalAssignee struct {
	Type    string     `json:"type"`
	AgentID *uuid.UUID `json:"agent_id,omitempty"`
	PackID  *uuid.UUID `json:"pack_id,omitempty"`
}
type ProposalContent struct {
	Title       string           `json:"title,omitempty"`
	Priority    string           `json:"priority,omitempty"`
	Description string           `json:"description"`
	Stage       string           `json:"stage"`
	Assignee    ProposalAssignee `json:"assignee"`
	ProjectID   *uuid.UUID       `json:"project_id"`
}
type StudyProposal struct {
	ProposalContent
	ID               uuid.UUID  `json:"id"`
	WorkspaceID      uuid.UUID  `json:"workspace_id"`
	OwnerID          uuid.UUID  `json:"owner_id"`
	Revision         int64      `json:"revision"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	DiscardedAt      *time.Time `json:"-"`
	LastSaveRevision *int64     `json:"-"`
	OperationID      *uuid.UUID `json:"-"`
	LaunchRevision   *int64     `json:"-"`
	CardID           *uuid.UUID `json:"-"`
	CardNumber       *int32     `json:"-"`
}

const proposalColumns = `id,workspace_id,owner_id,revision,created_at,updated_at,
COALESCE(title,''),COALESCE(priority,''),COALESCE(description,''),COALESCE(stage,''),COALESCE(assignee,'{}'::jsonb),project_id,discarded_at,last_save_revision,operation_id,launch_revision,card_id,card_number`

func scanProposal(row pgx.Row) (StudyProposal, error) {
	var p StudyProposal
	err := row.Scan(&p.ID, &p.WorkspaceID, &p.OwnerID, &p.Revision, &p.CreatedAt, &p.UpdatedAt, &p.Title, &p.Priority, &p.Description, &p.Stage, &p.Assignee, &p.ProjectID, &p.DiscardedAt, &p.LastSaveRevision, &p.OperationID, &p.LaunchRevision, &p.CardID, &p.CardNumber)
	return p, err
}

func (t *Tx) StudyProposals(ctx context.Context, owner, project *uuid.UUID, before *time.Time, id uuid.UUID, limit int32) ([]StudyProposal, error) {
	rows, err := t.raw.Query(ctx, `SELECT `+proposalColumns+` FROM study_proposal
 WHERE workspace_id=$1 AND card_id IS NULL AND discarded_at IS NULL AND ($2::uuid IS NULL OR owner_id=$2)
 AND ($3::uuid IS NULL OR project_id=$3) AND ($4::timestamptz IS NULL OR (created_at,id)<($4,$5::uuid))
 ORDER BY created_at DESC,id DESC LIMIT $6`, t.WS, owner, project, before, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ps := []StudyProposal{}
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		ps = append(ps, p)
	}
	return ps, rows.Err()
}

// All writers take the workspace execution lock before resolving identity,
// including direct starts where there is no existing row to lock.
func (t *Tx) StudyProposal(ctx context.Context, id uuid.UUID) (StudyProposal, error) {
	return scanProposal(t.raw.QueryRow(ctx, `SELECT `+proposalColumns+` FROM study_proposal WHERE workspace_id=$1 AND id=$2`, t.WS, id))
}

func (t *Tx) WriteStudyProposal(ctx context.Context, id, user uuid.UUID, revision int64, p ProposalContent) (StudyProposal, error) {
	if revision == 0 {
		return scanProposal(t.raw.QueryRow(ctx, `INSERT INTO study_proposal(workspace_id,id,owner_id,description,stage,assignee,project_id,revision,last_save_revision,title,priority)
 VALUES ($1,$2,$3,$4,$5,$6,$7,1,0,$8,$9) RETURNING `+proposalColumns, t.WS, id, user, p.Description, p.Stage, p.Assignee, p.ProjectID, p.Title, p.Priority))
	}
	return scanProposal(t.raw.QueryRow(ctx, `UPDATE study_proposal SET title=$9,priority=$10,description=$4,stage=$5,assignee=$6,project_id=$7,last_save_revision=revision,revision=revision+1,updated_at=now()
 WHERE workspace_id=$1 AND id=$2 AND owner_id=$3 AND revision=$8 AND card_id IS NULL AND discarded_at IS NULL RETURNING `+proposalColumns, t.WS, id, user, p.Description, p.Stage, p.Assignee, p.ProjectID, revision, p.Title, p.Priority))
}

func (t *Tx) RecordProposalLaunch(ctx context.Context, id, operation, card uuid.UUID, revision int64, number int32) error {
	tag, err := t.raw.Exec(ctx, `UPDATE study_proposal SET operation_id=$3,card_id=$4,launch_revision=$5,card_number=$6,updated_at=now()
 WHERE workspace_id=$1 AND id=$2 AND card_id IS NULL AND discarded_at IS NULL`, t.WS, id, operation, card, revision, number)
	if err == nil && tag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return err
}

func (t *Tx) ChangeStudyProposalProjectOrDiscard(ctx context.Context, id uuid.UUID, revision int64, project *uuid.UUID, discard bool) error {
	var changed bool
	err := t.raw.QueryRow(ctx, `SELECT public.change_study_proposal_project_or_discard($1,$2,$3,$4)`, id, revision, project, discard).Scan(&changed)
	if err == nil && !changed {
		return pgx.ErrNoRows
	}
	return err
}
