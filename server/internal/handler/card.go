package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type newCard struct {
	Title       string      `json:"title"`
	Question    string      `json:"question"`
	BudgetUSD   float64     `json:"budget_usd"`
	Deadline    *time.Time  `json:"deadline"`
	FollowsFrom []uuid.UUID `json:"follows_from"`
	StartKind   string      `json:"start_kind"`
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

var errDuplicate = errors.New("duplicate")

func (h *H) createCard(w http.ResponseWriter, r *http.Request) {
	var in newCard
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid card: "+err.Error())
		return
	}
	if strings.TrimSpace(in.Question) == "" {
		writeErr(w, 400, "question required")
		return
	}
	if in.BudgetUSD < 0 {
		writeErr(w, 400, "budget cannot be negative")
		return
	}
	if strings.TrimSpace(in.Title) == "" {
		in.Title = titleFrom(in.Question)
	}
	if in.FollowsFrom == nil {
		in.FollowsFrom = []uuid.UUID{}
	}
	var c db.Card
	var dup db.ActiveDuplicateRow
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		for _, parentID := range in.FollowsFrom {
			if _, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: parentID}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return cardInputError("follows_from contains an unknown card")
				}
				return err
			}
		}
		pk, capacity, err := cardPack(r, tx, user, in.StartKind)
		if err != nil {
			return err
		}
		// A follow-up may ask the same question again; any other card may not
		// while an active card asks it. The workspace lock makes two identical
		// cards created at once see each other.
		if len(in.FollowsFrom) == 0 {
			if err := tx.Q.LockWorkspaceEvents(r.Context(), ws); err != nil {
				return err
			}
			d, err := tx.Q.ActiveDuplicate(r.Context(), db.ActiveDuplicateParams{WorkspaceID: ws, Title: in.Title, Question: in.Question})
			switch {
			case err == nil:
				dup = d
				return errDuplicate
			case !errors.Is(err, pgx.ErrNoRows):
				return err
			}
		}
		n, err := tx.Q.NextCardNumber(r.Context(), ws)
		if err != nil {
			return err
		}
		c, err = tx.Q.CreateCard(r.Context(), db.CreateCardParams{
			WorkspaceID: ws, Number: n, Title: in.Title, Question: in.Question, OwnerID: user,
			BudgetUsd: in.BudgetUSD, Deadline: in.Deadline, FollowsFrom: in.FollowsFrom,
		})
		if err != nil {
			return err
		}
		if pk != nil {
			if c, err = tx.Q.PinNewCard(r.Context(), db.PinNewCardParams{PackID: &pk.ID, StartKind: in.StartKind,
				Cap: capacity, WorkspaceID: ws, ID: c.ID}); err != nil {
				return err
			}
		}
		return tx.Emit(r.Context(), "card.created", &c.ID, store.User(user), map[string]any{"number": c.Number, "title": c.Title})
	})
	if errors.Is(err, errDuplicate) {
		writeJSON(w, 409, map[string]any{"error": fmt.Sprintf("an active card already asks this: #%d", dup.Number),
			"card_id": dup.ID, "number": dup.Number})
		return
	}
	var inputErr cardInputError
	if errors.As(err, &inputErr) {
		writeErr(w, 400, inputErr.Error())
		return
	}
	if !answer(w, r, err) {
		return
	}
	writeJSON(w, 201, c)
}

func (h *H) listCards(w http.ResponseWriter, r *http.Request) {
	cards := []db.ListCardsRow{}
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		var err error
		cards, err = tx.Q.ListCards(r.Context(), ws)
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	if cards == nil {
		cards = []db.ListCardsRow{}
	}
	writeJSON(w, http.StatusOK, cards)
}

// cardRuns lists a card's agent runs, oldest first, for its transcript.
func (h *H) cardRuns(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	runs := []db.AgentRun{}
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		if _, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: id}); err != nil {
			return err
		}
		var err error
		runs, err = tx.Q.CardAgentRuns(r.Context(), db.CardAgentRunsParams{WorkspaceID: ws, CardID: &id})
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	if runs == nil {
		runs = []db.AgentRun{}
	}
	writeJSON(w, http.StatusOK, runs)
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
	if comments == nil {
		comments = []db.Comment{}
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
	if events == nil {
		events = []db.Event{}
	}
	writeJSON(w, http.StatusOK, events)
}

// listInbox lists the caller's open items, or with ?archived=1 the resolved ones.
func (h *H) listInbox(w http.ResponseWriter, r *http.Request) {
	archived := r.URL.Query().Get("archived") == "1"
	items := []db.InboxItem{}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		var err error
		if archived {
			items, err = tx.Q.ListArchivedInbox(r.Context(), db.ListArchivedInboxParams{WorkspaceID: ws, UserID: user})
		} else {
			items, err = tx.Q.ListInbox(r.Context(), db.ListInboxParams{WorkspaceID: ws, UserID: user})
		}
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	if items == nil {
		items = []db.InboxItem{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *H) readInbox(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		item, err := tx.Q.MarkInboxRead(r.Context(), db.MarkInboxReadParams{ID: id, WorkspaceID: ws, UserID: user})
		if err != nil {
			return err
		}
		if err := tx.Emit(r.Context(), "inbox.read", nil, store.User(user), map[string]string{"inbox_item_id": id.String()}); err != nil {
			return err
		}
		if item.Kind != "released" && item.Kind != "autoresearch_card" && item.Kind != "autoresearch_report" {
			return nil
		}
		return tx.Emit(r.Context(), "inbox.resolved", item.CardID, store.User(user), map[string]string{"inbox_item_id": id.String()})
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var errNotBacklog = errors.New("only a card in Backlog can be edited or deleted")

// nullableTime remembers whether its key was sent, so an explicit null can
// clear a deadline while a missing key leaves it alone.
type nullableTime struct {
	Set bool
	V   *time.Time
}

func (n *nullableTime) UnmarshalJSON(b []byte) error {
	n.Set = true
	return json.Unmarshal(b, &n.V)
}

type cardEdit struct {
	Title       *string      `json:"title"`
	Question    *string      `json:"question"`
	BudgetUSD   *float64     `json:"budget_usd"`
	Deadline    nullableTime `json:"deadline"`
	FollowsFrom *[]uuid.UUID `json:"follows_from"`
	Position    *float64     `json:"position"`
}

// fields names the keys an edit sets, in a fixed order.
func (e cardEdit) fields() []string {
	var out []string
	for _, f := range []struct {
		name string
		set  bool
	}{{"title", e.Title != nil}, {"question", e.Question != nil}, {"budget_usd", e.BudgetUSD != nil},
		{"deadline", e.Deadline.Set}, {"follows_from", e.FollowsFrom != nil}, {"position", e.Position != nil}} {
		if f.set {
			out = append(out, f.name)
		}
	}
	return out
}

// editCard changes a Backlog card. Scope has locked the workspace row, which
// WIP pull also locks before it starts a card, so the card cannot leave
// Backlog while it is being edited.
func (h *H) editCard(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var in cardEdit
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid card: "+err.Error())
		return
	}
	fields := in.fields()
	switch {
	case len(fields) == 0:
		writeErr(w, 400, "nothing to change")
		return
	case in.Title != nil && strings.TrimSpace(*in.Title) == "":
		writeErr(w, 400, "title cannot be empty")
		return
	case in.Question != nil && strings.TrimSpace(*in.Question) == "":
		writeErr(w, 400, "question cannot be empty")
		return
	case in.BudgetUSD != nil && *in.BudgetUSD < 0:
		writeErr(w, 400, "budget cannot be negative")
		return
	}
	var out db.Card
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		c, err := tx.Q.LockCard(r.Context(), db.LockCardParams{ID: id, WorkspaceID: ws})
		if err != nil {
			return err
		}
		if c.BoardColumn != board.Backlog {
			return errNotBacklog
		}
		p := db.EditBacklogCardParams{Title: c.Title, Question: c.Question, BudgetUsd: c.BudgetUsd, Deadline: c.Deadline,
			FollowsFrom: c.FollowsFrom, Position: c.Position, ID: id, WorkspaceID: ws}
		if in.Title != nil {
			p.Title = strings.TrimSpace(*in.Title)
		}
		if in.Question != nil {
			p.Question = *in.Question
		}
		if in.BudgetUSD != nil {
			p.BudgetUsd = *in.BudgetUSD
		}
		if in.Deadline.Set {
			p.Deadline = in.Deadline.V
		}
		if in.Position != nil {
			p.Position = *in.Position
		}
		if in.FollowsFrom != nil {
			p.FollowsFrom = []uuid.UUID{}
			for _, parent := range *in.FollowsFrom {
				if parent == id {
					return cardInputError("a card cannot follow from itself")
				}
				if _, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: parent}); err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						return cardInputError("follows_from contains an unknown card")
					}
					return err
				}
				p.FollowsFrom = append(p.FollowsFrom, parent)
			}
		}
		if out, err = tx.Q.EditBacklogCard(r.Context(), p); err != nil {
			return err
		}
		event := "card.edited"
		if len(fields) == 1 && fields[0] == "position" {
			event = "card.reordered"
		}
		return tx.Emit(r.Context(), event, &id, store.User(user), map[string]any{"fields": fields, "position": out.Position})
	})
	cardEditFail(w, r, err, out)
}

// deleteCard removes a Backlog card and everything recorded on it. Its
// events stay, with the card's id in the payload; the database clears their
// card reference.
func (h *H) deleteCard(w http.ResponseWriter, r *http.Request) {
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
		c, err := tx.Q.LockCard(r.Context(), db.LockCardParams{ID: id, WorkspaceID: ws})
		if err != nil {
			return err
		}
		if c.BoardColumn != board.Backlog {
			return errNotBacklog
		}
		runs, err := tx.Q.FailRunsForDeletedCard(r.Context(), db.FailRunsForDeletedCardParams{WorkspaceID: ws, CardID: &id})
		if err != nil {
			return err
		}
		for _, run := range runs {
			if err := tx.Emit(r.Context(), "autoresearch_run.failed", nil, store.User(user), map[string]any{
				"run_id": run.ID, "autoresearch_id": run.AutoresearchID, "status": run.Status, "reason": run.Reason,
				"source": run.Source, "card_id": run.CardID, "agent_run_id": run.AgentRunID,
			}); err != nil {
				return err
			}
		}
		if _, err := tx.Q.DeleteBacklogCard(r.Context(), db.DeleteBacklogCardParams{ID: id, WorkspaceID: ws}); err != nil {
			return err
		}
		return tx.Emit(r.Context(), "card.deleted", nil, store.User(user), map[string]any{"card_id": id, "number": c.Number})
	})
	if err != nil {
		cardEditFail(w, r, err, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// cardEditFail answers an edit or delete: 409 off Backlog, 400 for bad input,
// otherwise the card (or the server error).
func cardEditFail(w http.ResponseWriter, r *http.Request, err error, out any) {
	var inputErr cardInputError
	switch {
	case errors.Is(err, errNotBacklog):
		writeErr(w, 409, err.Error())
	case errors.As(err, &inputErr):
		writeErr(w, 400, inputErr.Error())
	case err != nil:
		fail(w, r, err)
	default:
		writeJSON(w, http.StatusOK, out)
	}
}
