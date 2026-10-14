package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type newCard struct {
	Title       string      `json:"title"`
	Question    string      `json:"question"`
	BudgetUSD   float64     `json:"budget_usd"`
	Deadline    *time.Time  `json:"deadline"`
	FollowsFrom []uuid.UUID `json:"follows_from"`
}

type cardInputError string

func (e cardInputError) Error() string { return string(e) }

func titleFrom(question string) string {
	question = strings.Join(strings.Fields(question), " ")
	if utf8.RuneCountInString(question) > 80 {
		runes := []rune(question)
		return string(runes[:79]) + "…"
	}
	return question
}

func cardID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return uuid.Nil, false
	}
	return id, true
}

func (h *H) createCard(w http.ResponseWriter, r *http.Request) {
	var in newCard
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid card: "+err.Error())
		return
	}
	if strings.TrimSpace(in.Question) == "" {
		writeErr(w, http.StatusBadRequest, "question required")
		return
	}
	if in.BudgetUSD < 0 {
		writeErr(w, http.StatusBadRequest, "budget cannot be negative")
		return
	}
	if strings.TrimSpace(in.Title) == "" {
		in.Title = titleFrom(in.Question)
	}
	if in.FollowsFrom == nil {
		in.FollowsFrom = []uuid.UUID{}
	}
	var created db.Card
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		for _, parentID := range in.FollowsFrom {
			if _, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: parentID}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return cardInputError("follows_from contains an unknown card")
				}
				return err
			}
		}
		number, err := tx.Q.NextCardNumber(r.Context(), ws)
		if err != nil {
			return err
		}
		created, err = tx.Q.CreateCard(r.Context(), db.CreateCardParams{
			WorkspaceID: ws, Number: number, Title: in.Title, Question: in.Question,
			OwnerID: user, BudgetUsd: in.BudgetUSD, Deadline: in.Deadline, FollowsFrom: in.FollowsFrom,
		})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "card.created", &created.ID, store.User(user), map[string]any{
			"number": created.Number, "title": created.Title,
		})
	})
	var inputErr cardInputError
	if errors.As(err, &inputErr) {
		writeErr(w, http.StatusBadRequest, inputErr.Error())
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *H) listCards(w http.ResponseWriter, r *http.Request) {
	cards := []db.Card{}
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		var err error
		cards, err = tx.Q.ListCards(r.Context(), ws)
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, cards)
}

func (h *H) getCard(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var card db.Card
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		var err error
		card, err = tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: id})
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, card)
}

func (h *H) createComment(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Body) == "" {
		writeErr(w, http.StatusBadRequest, "body required")
		return
	}
	var comment db.Comment
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if _, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: id}); err != nil {
			return err
		}
		var err error
		comment, err = tx.Q.CreateComment(r.Context(), db.CreateCommentParams{
			WorkspaceID: ws, CardID: id, AuthorID: user, Body: in.Body,
		})
		if err != nil {
			return err
		}
		return tx.Emit(r.Context(), "comment.created", &id, store.User(user), map[string]string{
			"comment_id": comment.ID.String(),
		})
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, comment)
}

func (h *H) listComments(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	comments := []db.Comment{}
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		if _, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: id}); err != nil {
			return err
		}
		var err error
		comments, err = tx.Q.ListComments(r.Context(), db.ListCommentsParams{WorkspaceID: ws, CardID: id})
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, comments)
}

func (h *H) cardEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var after int64
	if value, present := r.URL.Query()["after"]; present {
		if len(value) != 1 {
			writeErr(w, http.StatusBadRequest, "after must be a non-negative integer")
			return
		}
		parsed, err := strconv.ParseInt(value[0], 10, 64)
		if err != nil || parsed < 0 {
			writeErr(w, http.StatusBadRequest, "after must be a non-negative integer")
			return
		}
		after = parsed
	}
	events := []db.Event{}
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		if _, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: id}); err != nil {
			return err
		}
		var err error
		events, err = tx.Q.CardEventsAfter(r.Context(), db.CardEventsAfterParams{WorkspaceID: ws, CardID: id, After: after})
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (h *H) listInbox(w http.ResponseWriter, r *http.Request) {
	items := []db.InboxItem{}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		var err error
		items, err = tx.Q.ListInbox(r.Context(), db.ListInboxParams{WorkspaceID: ws, UserID: user})
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *H) readInbox(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		n, err := tx.Q.MarkInboxRead(r.Context(), db.MarkInboxReadParams{ID: id, WorkspaceID: ws, UserID: user})
		if err != nil {
			return err
		}
		if n == 0 {
			return pgx.ErrNoRows
		}
		return tx.Emit(r.Context(), "inbox.read", nil, store.User(user), map[string]string{"inbox_item_id": id.String()})
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
