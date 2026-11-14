package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// ratingView is one column of a study offered for rating to its owner.
type ratingView struct {
	Column    string `json:"column"`
	Rating    *int16 `json:"rating"`
	Dismissed bool   `json:"dismissed"`
}

func viewOfRating(t db.TeamRating) ratingView {
	return ratingView{Column: t.BoardColumn, Rating: t.Rating, Dismissed: t.DismissedAt != nil}
}

// cardRatings lists the caller's own offers on a study; anyone else sees none.
func (h *H) cardRatings(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	out := []ratingView{}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if _, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: id}); err != nil {
			return err
		}
		rows, err := tx.Q.CardRatings(r.Context(), db.CardRatingsParams{WorkspaceID: ws, CardID: &id, UserID: user})
		for _, row := range rows {
			out = append(out, viewOfRating(row))
		}
		return err
	})
	if answer(w, r, err) {
		writeJSON(w, 200, out)
	}
}

// ratingOf parses a rating body: an integer from 1 to 4, or null to clear.
func ratingOf(r *http.Request) (*int16, bool) {
	var in struct {
		Rating json.RawMessage `json:"rating"`
	}
	if err := readJSON(r, &in); err != nil || len(in.Rating) == 0 {
		return nil, false
	}
	if string(in.Rating) == "null" {
		return nil, true
	}
	var n int16
	if err := json.Unmarshal(in.Rating, &n); err != nil || n < 1 || n > 4 {
		return nil, false
	}
	return &n, true
}

// ownOffer finds the study in the caller's workspace, then refuses a column
// offered to someone else; a column offered to no one is not found.
func ownOffer(r *http.Request, tx *store.Tx, ws, card, user uuid.UUID, column string) error {
	if _, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: card}); err != nil {
		return err
	}
	owner, err := tx.Q.RatingOwner(r.Context(), db.RatingOwnerParams{WorkspaceID: ws, CardID: &card, BoardColumn: column})
	if err != nil {
		return err
	}
	if owner != user {
		return forbidden("only the study's owner rates its Teams")
	}
	return nil
}

func (h *H) setRating(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	rating, valid := ratingOf(r)
	column := chi.URLParam(r, "column")
	var out ratingView
	now := time.Now()
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if err := ownOffer(r, tx, ws, id, user, column); err != nil {
			return err
		}
		if !valid {
			return cardInputError("rate from 1 to 4")
		}
		row, err := tx.Q.SetRating(r.Context(), db.SetRatingParams{Rating: rating, RatedAt: &now,
			WorkspaceID: ws, CardID: &id, BoardColumn: column, UserID: user})
		out = viewOfRating(row)
		return err
	})
	if answer(w, r, err) {
		writeJSON(w, 200, out)
	}
}

func (h *H) dismissRating(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	column := chi.URLParam(r, "column")
	var out ratingView
	now := time.Now()
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if err := ownOffer(r, tx, ws, id, user, column); err != nil {
			return err
		}
		row, err := tx.Q.DismissRating(r.Context(), db.DismissRatingParams{DismissedAt: &now,
			WorkspaceID: ws, CardID: &id, BoardColumn: column, UserID: user})
		out = viewOfRating(row)
		return err
	})
	if answer(w, r, err) {
		writeJSON(w, 200, out)
	}
}
