package store

import (
	"context"

	"github.com/google/uuid"

	db "lykeion/server/pkg/db/generated"
)

// Suggestions remain in the private chat. Saving or launching consumes the
// message ID as the proposal ID, including when another device performs it.
func (t *Tx) PrivateStudySuggestions(ctx context.Context, owner uuid.UUID) ([]db.LabMessage, error) {
	rows, err := t.raw.Query(ctx, `SELECT m.id,m.chat_id,m.proposal->'new_study',m.statements,m.evidence
 FROM lab_message m JOIN lab_chat c ON (c.workspace_id,c.id,c.owner_id)=(m.workspace_id,m.chat_id,m.owner_id)
 WHERE m.workspace_id=$1 AND m.owner_id=$2 AND m.kind='answer' AND c.purpose='study_suggestion' AND c.deleted_at IS NULL
 AND m.proposal ? 'new_study' AND NOT EXISTS (SELECT 1 FROM study_proposal p WHERE p.workspace_id=m.workspace_id AND p.id=m.id AND p.owner_id=m.owner_id)
 ORDER BY m.created_at DESC,m.id DESC LIMIT 50`, t.WS, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []db.LabMessage
	for rows.Next() {
		var m db.LabMessage
		if err := rows.Scan(&m.ID, &m.ChatID, &m.Proposal, &m.Statements, &m.Evidence); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Bind an unambiguous display name to the worker visible at suggestion time.
// Launch rechecks eligibility using the stored ID.
func (t *Tx) SuggestedAssignee(ctx context.Context, owner uuid.UUID, stage, name string) (*ProposalAssignee, error) {
	agents, err := t.Q.ListAgents(ctx, db.ListAgentsParams{WorkspaceID: t.WS, UserID: owner})
	if err != nil {
		return nil, err
	}
	var matches []ProposalAssignee
	for _, a := range agents {
		if a.Name == name && a.Stage == stage && a.BuiltInKey != "aristotle" && a.ArchivedAt == nil {
			matches = append(matches, ProposalAssignee{Type: "agent", AgentID: &a.ID})
		}
	}
	teams, err := t.Q.StoreTeams(ctx, db.StoreTeamsParams{WorkspaceID: t.WS, UserID: owner, Stage: stage})
	if err != nil {
		return nil, err
	}
	for _, team := range teams {
		if team.Name == name && team.Added {
			matches = append(matches, ProposalAssignee{Type: "team", PackID: &team.ID})
		}
	}
	if len(matches) == 1 {
		return &matches[0], nil
	}
	return nil, nil
}

// QuestionRequest finds a replay inside the locked private chat.
func (t *Tx) QuestionRequest(ctx context.Context, chat, user, operation uuid.UUID) (db.LabMessage, string, string, error) {
	var run uuid.UUID
	var harness, model string
	err := t.raw.QueryRow(ctx, `SELECT r.id,r.harness,r.requested_model FROM lab_message m
 JOIN agent_run r ON r.workspace_id=m.workspace_id AND r.id=m.agent_run_id
 WHERE m.workspace_id=$1 AND m.owner_id=$2 AND m.chat_id=$3 AND m.request_id=$4 AND m.kind='question'`,
		t.WS, user, chat, operation).Scan(&run, &harness, &model)
	if err != nil {
		return db.LabMessage{}, "", "", err
	}
	msg, err := t.Q.GetLabQuestion(ctx, run)
	return msg, harness, model, err
}

func (t *Tx) SuggestionSource(ctx context.Context, user, id uuid.UUID) (db.LabMessage, error) {
	var msg db.LabMessage
	err := t.raw.QueryRow(ctx, `SELECT m.id,m.chat_id,m.statements,m.evidence FROM lab_message m
 JOIN lab_chat c ON (c.workspace_id,c.owner_id,c.id)=(m.workspace_id,m.owner_id,m.chat_id)
 WHERE m.workspace_id=$1 AND m.owner_id=$2 AND m.id=$3 AND m.kind='answer' AND c.purpose='study_suggestion' AND m.proposal ? 'new_study' AND c.deleted_at IS NULL`, t.WS, user, id).Scan(&msg.ID, &msg.ChatID, &msg.Statements, &msg.Evidence)
	return msg, err
}
