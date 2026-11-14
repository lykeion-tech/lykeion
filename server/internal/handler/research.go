package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func (h *H) listResearches(w http.ResponseWriter, r *http.Request) {
	out := []db.Research{}
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		var err error
		out, err = tx.Q.ListResearches(r.Context(), ws)
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	if out == nil {
		out = []db.Research{}
	}
	writeJSON(w, 200, out)
}

type researchInput struct {
	Name        *string        `json:"name"`
	Description *string        `json:"description"`
	Status      nullableString `json:"status"`
	Priority    nullableString `json:"priority"`
	LeadType    nullableString `json:"lead_type"`
	LeadID      nullableUUID   `json:"lead_id"`
	StartDate   nullableTime   `json:"start_date"`
	DueDate     nullableTime   `json:"due_date"`
}

type nullableTime struct {
	Set bool
	V   *time.Time
}

func (v *nullableTime) UnmarshalJSON(b []byte) error { v.Set = true; return json.Unmarshal(b, &v.V) }

func (in researchInput) valid(create bool) bool {
	if create && in.Name == nil || in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return false
	}
	if !create && in.Name == nil && in.Description == nil && !in.Status.Set && !in.Priority.Set && !in.LeadType.Set && !in.LeadID.Set && !in.StartDate.Set && !in.DueDate.Set {
		return false
	}
	if in.Status.Set {
		if in.Status.V == nil {
			return false
		}
		switch *in.Status.V {
		case "planned", "in_progress", "paused", "completed", "cancelled":
		default:
			return false
		}
	}
	if in.Priority.Set {
		if in.Priority.V == nil {
			return false
		}
		switch *in.Priority.V {
		case "none", "urgent", "high", "medium", "low":
		default:
			return false
		}
	}
	if in.LeadType.Set != in.LeadID.Set {
		return false
	}
	if in.LeadType.Set {
		if (in.LeadType.V == nil) != (in.LeadID.V == nil) {
			return false
		}
		if in.LeadType.V != nil && *in.LeadType.V != "member" && *in.LeadType.V != "agent" {
			return false
		}
	}
	return true
}

func (in researchInput) apply(r *db.Research) {
	if in.Name != nil {
		r.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		r.Description = *in.Description
	}
	if in.Status.Set {
		r.Status = *in.Status.V
	}
	if in.Priority.Set {
		r.Priority = *in.Priority.V
	}
	if in.LeadType.Set {
		r.LeadType, r.LeadID = in.LeadType.V, in.LeadID.V
	}
	if in.StartDate.Set {
		r.StartDate = in.StartDate.V
	}
	if in.DueDate.Set {
		r.DueDate = in.DueDate.V
	}
}
func validResearchDates(r db.Research) bool {
	return r.StartDate == nil || r.DueDate == nil || !r.StartDate.After(*r.DueDate)
}

func sameResearchLead(a, b db.Research) bool {
	if a.LeadType == nil || b.LeadType == nil {
		return a.LeadType == nil && b.LeadType == nil
	}
	return *a.LeadType == *b.LeadType && *a.LeadID == *b.LeadID
}

func (h *H) createResearch(w http.ResponseWriter, r *http.Request) {
	var in researchInput
	if err := readJSON(r, &in); err != nil || !in.valid(true) {
		writeErr(w, 400, "valid project fields and a name required")
		return
	}
	var out db.Research
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		fields := db.Research{Status: "planned", Priority: "none"}
		in.apply(&fields)
		if !validResearchDates(fields) {
			return cardInputError("start date must be on or before the due date")
		}
		if fields.LeadType != nil {
			if err := checkAssignee(r, tx, ws, user, *fields.LeadType, *fields.LeadID); err != nil {
				return err
			}
		}
		var err error
		out, err = tx.Q.CreateResearch(r.Context(), db.CreateResearchParams{WorkspaceID: ws, Name: fields.Name, Description: fields.Description, CreatedBy: user, Status: fields.Status, Priority: fields.Priority, LeadType: fields.LeadType, LeadID: fields.LeadID, StartDate: fields.StartDate, DueDate: fields.DueDate})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "research.created", nil, store.User(user), map[string]any{"research_id": out.ID})
	})
	if err != nil {
		answer(w, r, err)
		return
	}
	writeJSON(w, 201, out)
}

func (h *H) getResearch(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var out struct {
		db.Research
		Studies []db.Card `json:"studies"`
	}
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		var err error
		out.Research, err = tx.Q.GetResearch(r.Context(), db.GetResearchParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		out.Studies, err = tx.Q.ResearchStudies(r.Context(), db.ResearchStudiesParams{WorkspaceID: ws, ResearchID: &id})
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	if out.Studies == nil {
		out.Studies = []db.Card{}
	}
	writeJSON(w, 200, out)
}

func (h *H) updateResearch(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var in researchInput
	if err := readJSON(r, &in); err != nil || !in.valid(false) {
		writeErr(w, 400, "valid project fields required")
		return
	}
	var out db.Research
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		prev, err := tx.Q.GetResearch(r.Context(), db.GetResearchParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		fields := prev
		in.apply(&fields)
		if !validResearchDates(fields) {
			return cardInputError("start date must be on or before the due date")
		}
		if !sameResearchLead(prev, fields) && fields.LeadType != nil {
			if err := checkAssignee(r, tx, ws, user, *fields.LeadType, *fields.LeadID); err != nil {
				return err
			}
		}
		out, err = tx.Q.UpdateResearch(r.Context(), db.UpdateResearchParams{WorkspaceID: ws, ID: id, Name: fields.Name, Description: fields.Description, Status: fields.Status, Priority: fields.Priority, LeadType: fields.LeadType, LeadID: fields.LeadID, StartDate: fields.StartDate, DueDate: fields.DueDate})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "research.updated", nil, store.User(user), map[string]any{"research_id": id})
	})
	if err != nil {
		answer(w, r, err)
		return
	}
	writeJSON(w, 200, out)
}

func (h *H) deleteResearch(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if _, err := tx.Q.LockWorkspaceExecution(r.Context(), ws); err != nil {
			return err
		}
		if err := tx.Q.LockWorkspaceEvents(r.Context(), ws); err != nil {
			return err
		}
		if _, err := tx.Q.LockResearchForDelete(r.Context(), db.LockResearchForDeleteParams{WorkspaceID: ws, ID: id}); err != nil {
			return err
		}
		studies, err := tx.Q.DetachResearchStudies(r.Context(), db.DetachResearchStudiesParams{WorkspaceID: ws, ResearchID: &id})
		if err != nil {
			return err
		}
		for _, study := range studies {
			if err := tx.Emit(r.Context(), "card.properties_updated", &study.ID, store.User(user), map[string]any{"fields": []string{"research_id"}, "research_id": study.ResearchID, "previous_research_id": id, "assignee_type": study.AssigneeType, "assignee_id": study.AssigneeID}); err != nil {
				return err
			}
		}
		n, err := tx.Q.DeleteResearch(r.Context(), db.DeleteResearchParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		if n == 0 {
			return pgx.ErrNoRows
		}
		return tx.Emit(r.Context(), "research.deleted", nil, store.User(user), map[string]any{"research_id": id})
	})
	if answer(w, r, err) {
		w.WriteHeader(http.StatusNoContent)
	}
}

type nullableUUID struct {
	Set bool
	V   *uuid.UUID
}

func (v *nullableUUID) UnmarshalJSON(b []byte) error {
	v.Set = true
	return json.Unmarshal(b, &v.V)
}

type nullableString struct {
	Set bool
	V   *string
}

func (v *nullableString) UnmarshalJSON(b []byte) error {
	v.Set = true
	return json.Unmarshal(b, &v.V)
}

// updateCardProperties changes a study's research, which is descriptive and
// anyone may change. Who works a study is fixed when it is created.
func (h *H) updateCardProperties(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var in struct {
		ResearchID nullableUUID `json:"research_id"`
	}
	if err := readJSON(r, &in); err != nil || !in.ResearchID.Set {
		writeErr(w, 400, "send research_id; clear it with null")
		return
	}
	var out db.Card
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		prev, err := tx.Q.LockCard(r.Context(), db.LockCardParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		if in.ResearchID.V != nil {
			if _, err := tx.Q.GetResearch(r.Context(), db.GetResearchParams{WorkspaceID: ws, ID: *in.ResearchID.V}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return cardInputError("research is not in this workspace")
				}
				return err
			}
		}
		out, err = tx.Q.UpdateCardProperties(r.Context(), db.UpdateCardPropertiesParams{WorkspaceID: ws, ID: id, ResearchID: in.ResearchID.V})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "card.properties_updated", &id, store.User(user), map[string]any{"fields": []string{"research_id"}, "research_id": out.ResearchID, "previous_research_id": prev.ResearchID, "assignee_type": out.AssigneeType, "assignee_id": out.AssigneeID})
	})
	if answer(w, r, err) {
		writeJSON(w, http.StatusOK, out)
	}
}
