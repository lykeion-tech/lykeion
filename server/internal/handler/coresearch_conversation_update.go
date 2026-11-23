package handler

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func (h *H) updateCoresearchConversation(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title          *string      `json:"title"`
		ResearchID     nullableUUID `json:"research_id"`
		NewProjectName *string      `json:"new_project_name"`
		Pinned         *bool        `json:"pinned"`
		Archived       *bool        `json:"archived"`
	}
	if err := readJSON(r, &in); err != nil ||
		(in.ResearchID.Set && in.NewProjectName != nil) ||
		(in.Title == nil && !in.ResearchID.Set && in.NewProjectName == nil && in.Pinned == nil && in.Archived == nil) {
		writeErr(w, http.StatusBadRequest, "valid conversation changes required")
		return
	}
	if in.Title != nil {
		*in.Title = strings.TrimSpace(*in.Title)
		if !validCoresearchName(*in.Title) || strings.ContainsRune(*in.Title, 0) {
			writeErr(w, http.StatusBadRequest, "conversation title must be between 1 and 120 characters")
			return
		}
	}
	if in.NewProjectName != nil {
		*in.NewProjectName = strings.TrimSpace(*in.NewProjectName)
		if !(researchInput{Name: in.NewProjectName}).valid(true) || strings.ContainsRune(*in.NewProjectName, 0) {
			writeErr(w, http.StatusBadRequest, "valid project name required")
			return
		}
	}
	id, err := coresearchID(r, "conversation")
	if !coresearchAnswer(w, r, err) {
		return
	}
	ctx := r.Context()
	var out db.CoresearchConversation
	err = h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		before, err := tx.Q.GetCoresearchConversation(ctx, id)
		if err != nil {
			return err
		}
		fields := db.UpdateCoresearchConversationParams{
			ID: id, Title: before.Title, ResearchID: before.ResearchID,
			PinnedAt: before.PinnedAt, ArchivedAt: before.ArchivedAt,
		}
		if in.Title != nil {
			fields.Title = *in.Title
		}
		if in.ResearchID.Set {
			fields.ResearchID = in.ResearchID.V
			if fields.ResearchID != nil {
				if _, err := tx.Q.GetResearch(ctx, db.GetResearchParams{WorkspaceID: ws, ID: *fields.ResearchID}); err != nil {
					return err
				}
			}
		}
		if in.NewProjectName != nil {
			project, err := tx.Q.CreateResearch(ctx, db.CreateResearchParams{
				WorkspaceID: ws, Name: *in.NewProjectName, CreatedBy: who, Status: "planned", Priority: "none",
			})
			if err != nil {
				return err
			}
			fields.ResearchID = &project.ID
			if err := tx.Emit(ctx, "research.created", nil, store.User(who), map[string]uuid.UUID{"research_id": project.ID}); err != nil {
				return err
			}
		}
		fields.PinnedAt = conversationToggleTime(in.Pinned, fields.PinnedAt)
		fields.ArchivedAt = conversationToggleTime(in.Archived, fields.ArchivedAt)
		if fields.Title != before.Title {
			project, err := tx.Q.RenameCoresearchProject(ctx, db.RenameCoresearchProjectParams{WorkspaceID: ws, ID: before.ProjectID, Name: fields.Title})
			if err != nil {
				return err
			}
			if project.CreatedBy != who {
				return pgx.ErrNoRows
			}
			if project.Mode == "cowrite" {
				members, err := tx.Q.ListCoresearchMembers(ctx, db.ListCoresearchMembersParams{WorkspaceID: ws, ProjectID: before.ProjectID})
				if err != nil {
					return err
				}
				if err := emitCoresearchProjectUpdate(ctx, tx, members, who, before.ProjectID); err != nil {
					return err
				}
			}
		}
		out, err = tx.Q.UpdateCoresearchConversation(ctx, fields)
		if err != nil {
			return err
		}
		if err := tx.EmitPrivate(ctx, who, "coresearch.conversation.updated", store.User(who), map[string]uuid.UUID{"project_id": before.ProjectID}); err != nil {
			return err
		}
		return emitResearchCoresearchChanged(ctx, tx, who, before.ResearchID, out.ResearchID)
	})
	if coresearchAnswer(w, r, err) {
		writeJSON(w, http.StatusOK, out)
	}
}

func conversationToggleTime(set *bool, current *time.Time) *time.Time {
	if set == nil {
		return current
	}
	if !*set {
		return nil
	}
	if current != nil {
		return current
	}
	now := time.Now().UTC()
	return &now
}

func emitResearchCoresearchChanged(ctx context.Context, tx *store.Tx, who uuid.UUID, previous, current *uuid.UUID) error {
	if previous == nil && current == nil {
		return nil
	}
	return tx.Emit(ctx, "research.coresearch.changed", nil, store.User(who), map[string]*uuid.UUID{
		"research_id": current, "previous_research_id": previous,
	})
}

func emitCoresearchProjectUpdate(ctx context.Context, tx *store.Tx, members []db.CoresearchMember, who, project uuid.UUID) error {
	for _, member := range members {
		if err := tx.EmitPrivate(ctx, member.UserID, "coresearch.project.updated", store.User(who), map[string]uuid.UUID{"project_id": project}); err != nil {
			return err
		}
	}
	return nil
}
