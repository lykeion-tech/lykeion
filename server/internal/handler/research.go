package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

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

func (h *H) createResearch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, 400, "name required and valid research fields expected")
		return
	}
	var out db.Research
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		var err error
		out, err = tx.Q.CreateResearch(r.Context(), db.CreateResearchParams{WorkspaceID: ws, Name: strings.TrimSpace(in.Name), Description: in.Description, CreatedBy: user})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "research.created", nil, store.User(user), map[string]any{"research_id": out.ID})
	})
	if err != nil {
		fail(w, r, err)
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
	var in struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := readJSON(r, &in); err != nil || (in.Name == nil && in.Description == nil) || (in.Name != nil && strings.TrimSpace(*in.Name) == "") {
		writeErr(w, 400, "valid name or description required")
		return
	}
	var out db.Research
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		prev, err := tx.Q.GetResearch(r.Context(), db.GetResearchParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		if in.Name != nil {
			prev.Name = strings.TrimSpace(*in.Name)
		}
		if in.Description != nil {
			prev.Description = *in.Description
		}
		out, err = tx.Q.UpdateResearch(r.Context(), db.UpdateResearchParams{WorkspaceID: ws, ID: id, Name: prev.Name, Description: prev.Description})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "research.updated", nil, store.User(user), map[string]any{"research_id": id})
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, out)
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

// Responsibility and research membership are descriptive properties. They never
// change the card's owner or any input to its scientific execution.
func (h *H) updateCardProperties(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var in struct {
		ResearchID   nullableUUID   `json:"research_id"`
		AssigneeType nullableString `json:"assignee_type"`
		AssigneeID   nullableUUID   `json:"assignee_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid properties: "+err.Error())
		return
	}
	if (!in.ResearchID.Set && !in.AssigneeType.Set && !in.AssigneeID.Set) || in.AssigneeType.Set != in.AssigneeID.Set || (in.AssigneeType.Set && ((in.AssigneeType.V == nil) != (in.AssigneeID.V == nil))) {
		writeErr(w, 400, "send research_id or both assignee_type and assignee_id; clear with null")
		return
	}
	var out db.Card
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		prev, err := tx.Q.LockCard(r.Context(), db.LockCardParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		previousResearchID := prev.ResearchID
		fields := []string{}
		if in.ResearchID.Set {
			if in.ResearchID.V != nil {
				if _, err := tx.Q.GetResearch(r.Context(), db.GetResearchParams{WorkspaceID: ws, ID: *in.ResearchID.V}); err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						return cardInputError("research is not in this workspace")
					}
					return err
				}
			}
			prev.ResearchID = in.ResearchID.V
			fields = append(fields, "research_id")
		}
		if in.AssigneeType.Set {
			if in.AssigneeType.V != nil {
				switch *in.AssigneeType.V {
				case "member":
					if _, err := tx.Q.GetMember(r.Context(), db.GetMemberParams{WorkspaceID: ws, UserID: *in.AssigneeID.V}); err != nil {
						if errors.Is(err, pgx.ErrNoRows) {
							return cardInputError("assignee is not a workspace member")
						}
						return err
					}
				case "agent":
					a, err := tx.Q.GetAgent(r.Context(), db.GetAgentParams{WorkspaceID: ws, ID: *in.AssigneeID.V})
					if err != nil {
						if errors.Is(err, pgx.ErrNoRows) {
							return cardInputError("agent is unavailable")
						}
						return err
					}
					if !agentVisible(r, a, user) || a.ArchivedAt != nil || (a.Visibility == "private" && a.OwnerID != prev.OwnerID) {
						return cardInputError("agent is unavailable for this study owner")
					}
					if _, err := tx.Q.GetMember(r.Context(), db.GetMemberParams{WorkspaceID: ws, UserID: a.OwnerID}); err != nil {
						if errors.Is(err, pgx.ErrNoRows) {
							return cardInputError("agent owner is no longer a member")
						}
						return err
					}
				default:
					return cardInputError("assignee_type must be member or agent")
				}
			}
			prev.AssigneeType = in.AssigneeType.V
			prev.AssigneeID = in.AssigneeID.V
			fields = append(fields, "assignee_type", "assignee_id")
		}
		out, err = tx.Q.UpdateCardProperties(r.Context(), db.UpdateCardPropertiesParams{WorkspaceID: ws, ID: id, ResearchID: prev.ResearchID, AssigneeType: prev.AssigneeType, AssigneeID: prev.AssigneeID})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "card.properties_updated", &id, store.User(user), map[string]any{"fields": fields, "research_id": out.ResearchID, "previous_research_id": previousResearchID, "assignee_type": out.AssigneeType, "assignee_id": out.AssigneeID})
	})
	cardEditFail(w, r, err, out)
}
