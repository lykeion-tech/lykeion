package handler

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/internal/team"
	db "lykeion/server/pkg/db/generated"
)

type newCard struct {
	ResearchID  *uuid.UUID  `json:"research_id"`
	Title       string      `json:"title"`
	Question    string      `json:"question"`
	Deadline    *time.Time  `json:"deadline"`
	FollowsFrom []uuid.UUID `json:"follows_from"`
	Assignee    *assigneeIn `json:"assignee"`
	Scope       *scopeIn    `json:"scope"`
	// AttachmentIDs are the caller's pending uploads the study takes as its files.
	AttachmentIDs []uuid.UUID `json:"attachment_ids"`
}

// assigneeIn is Teams by column; an Autoresearch may instead name an agent, or
// an agent for the columns no Team covers.
type assigneeIn struct {
	Type    string               `json:"type"`
	ID      *uuid.UUID           `json:"id"`
	Teams   map[string]uuid.UUID `json:"teams"`
	AgentID *uuid.UUID           `json:"agent_id"`
}

type scopeIn struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// scopeOf gives the stages a scope names, every stage when none is sent.
func scopeOf(s *scopeIn) (from, to string, err error) {
	if s == nil {
		return board.Stages[0], board.Stages[len(board.Stages)-1], nil
	}
	if board.StageIndex(s.From) < 0 || board.StageIndex(s.To) < 0 {
		return "", "", cardInputError("scope names a column outside the stages")
	}
	if board.StageIndex(s.From) > board.StageIndex(s.To) {
		return "", "", cardInputError("scope must run from an earlier stage to a later one")
	}
	return s.From, s.To, nil
}

var errNotATeam = cardInputError("a study is worked by a Team")

// studyScope gives the stages a researcher's study covers and the Teams they
// chose for some of them. A lone Team with no scope keeps the study in its
// column.
func studyScope(in newCard) (from, to string, chosen map[string]uuid.UUID, err error) {
	if a := in.Assignee; a != nil && (a.Type != "team" || a.ID != nil || a.AgentID != nil) {
		return "", "", nil, errNotATeam
	}
	if from, to, err = scopeOf(in.Scope); err != nil || in.Assignee == nil {
		return from, to, nil, err
	}
	chosen = in.Assignee.Teams
	if len(chosen) == 0 {
		return "", "", nil, cardInputError("a Team assignment names no Team")
	}
	for col := range chosen {
		i := board.StageIndex(col)
		switch {
		case i < 0:
			return "", "", nil, cardInputError("a Team is placed on a column outside the stages")
		case len(chosen) == 1 && in.Scope == nil:
			return col, col, chosen, nil
		case i < board.StageIndex(from) || i > board.StageIndex(to):
			return "", "", nil, cardInputError("a Team is placed outside the study's scope")
		}
	}
	return from, to, chosen, nil
}

// resolveScope gives the stages an Autoresearch's studies cover. A lone Team
// with no wider scope keeps the study in its column; a Team set names an agent
// for the columns no Team covers.
func resolveScope(in newCard) (from, to string, err error) {
	if from, to, err = scopeOf(in.Scope); err != nil {
		return "", "", err
	}
	a := in.Assignee
	if a == nil || a.Type != "team" {
		return from, to, nil
	}
	if len(a.Teams) == 0 {
		return "", "", cardInputError("a Team assignment names no Team")
	}
	for col := range a.Teams {
		if board.StageIndex(col) < 0 {
			return "", "", cardInputError("a Team is placed on a column outside the stages")
		}
	}
	if len(a.Teams) == 1 {
		for col := range a.Teams {
			switch {
			case in.Scope == nil || (from == col && to == col):
				return col, col, nil
			case a.AgentID == nil:
				return "", "", cardInputError("one Team covers its own column only; name an agent to cover more")
			}
		}
	}
	for i := board.StageIndex(from); i <= board.StageIndex(to); i++ {
		if _, ok := a.Teams[board.Stages[i]]; !ok && a.AgentID == nil {
			return "", "", cardInputError("name an agent for the columns no Team covers")
		}
	}
	for col := range a.Teams {
		if i := board.StageIndex(col); i < board.StageIndex(from) || i > board.StageIndex(to) {
			return "", "", cardInputError("a Team is placed outside the study's scope")
		}
	}
	return from, to, nil
}

// checkAssigneeShape refuses an assignee whose fields don't fit its type.
func checkAssigneeShape(a *assigneeIn) error {
	if a == nil {
		return nil
	}
	switch a.Type {
	case "member", "agent":
		if a.ID == nil {
			return cardInputError("assignee id required")
		}
		if a.Teams != nil || a.AgentID != nil {
			return cardInputError("a member or agent assignee takes only an id")
		}
	case "team":
		if a.ID != nil {
			return cardInputError("a Team assignee takes teams and agent_id, not id")
		}
	default:
		return cardInputError("assignee type must be member, agent or team")
	}
	return nil
}

// checkAssigneeRefs refuses an assignee the person cannot assign: a member or
// agent they cannot see, or a Team they have not added.
func checkAssigneeRefs(r *http.Request, tx *store.Tx, ws, user uuid.UUID, a *assigneeIn) error {
	if a.Type != "team" {
		return checkAssignee(r, tx, ws, user, a.Type, *a.ID)
	}
	for _, pack := range a.Teams {
		if err := ensureTeam(r, tx, user, pack); err != nil {
			return err
		}
	}
	if a.AgentID != nil {
		return checkAssignee(r, tx, ws, user, "agent", *a.AgentID)
	}
	return nil
}

// checkAssignee refuses a member or agent the creator cannot assign.
func checkAssignee(r *http.Request, tx *store.Tx, ws, creator uuid.UUID, typ string, id uuid.UUID) error {
	if typ == "member" {
		if _, err := tx.Q.GetMember(r.Context(), db.GetMemberParams{WorkspaceID: ws, UserID: id}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return cardInputError("assignee is not a workspace member")
			}
			return err
		}
		return nil
	}
	a, err := tx.Q.GetAgent(r.Context(), db.GetAgentParams{WorkspaceID: ws, ID: id})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return cardInputError("agent is unavailable")
		}
		return err
	}
	if !agentVisible(r, a, creator) || a.ArchivedAt != nil || (a.Visibility == "private" && a.OwnerID != creator) {
		return cardInputError("agent is unavailable")
	}
	return nil
}

// ensureTeam readies a Team for the person: a shipped Team gives them their
// agents on the spot, an uploaded one must already be added.
func ensureTeam(r *http.Request, tx *store.Tx, user, pack uuid.UUID) error {
	err := team.Ensure(r.Context(), tx, user, pack)
	if errors.Is(err, team.ErrNotAdded) {
		return cardInputError(err.Error())
	}
	return teamInput(err)
}

type cardTeamView struct {
	PackID uuid.UUID `json:"pack_id"`
	Name   string    `json:"name"`
	Stage  string    `json:"stage"`
}

// studyView is a card with the Team of each column and the columns whose Team
// is labelled for another stage.
type studyView struct {
	db.Card
	Teams    map[string]cardTeamView `json:"teams"`
	OffLabel []string                `json:"off_label"`
	// Attachments are the files attached at creation, never their storage.
	Attachments []attachmentView `json:"attachments"`
}

type attachmentView struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	SizeBytes   int64     `json:"size_bytes"`
	ContentType string    `json:"content_type"`
}

func viewOf(r *http.Request, tx *store.Tx, c db.Card) (studyView, error) {
	v := studyView{Card: c, Teams: map[string]cardTeamView{}, OffLabel: []string{}, Attachments: []attachmentView{}}
	files, err := tx.Q.CardAttachments(r.Context(), db.CardAttachmentsParams{WorkspaceID: c.WorkspaceID, CardID: &c.ID})
	if err != nil {
		return v, err
	}
	for _, f := range files {
		v.Attachments = append(v.Attachments, attachmentView{ID: f.ID, Name: f.Name, SizeBytes: f.SizeBytes, ContentType: f.ContentType})
	}
	rows, err := tx.Q.CardTeams(r.Context(), db.CardTeamsParams{WorkspaceID: c.WorkspaceID, CardID: c.ID})
	if err != nil {
		return v, err
	}
	for _, row := range rows {
		v.Teams[row.BoardColumn] = cardTeamView{PackID: row.PackID, Name: row.Name, Stage: row.Stage}
	}
	for _, col := range board.Stages {
		if t, ok := v.Teams[col]; ok && t.Stage != "" && t.Stage != col {
			v.OffLabel = append(v.OffLabel, col)
		}
	}
	return v, nil
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
	if strings.TrimSpace(in.Title) == "" {
		in.Title = titleFrom(in.Question)
	}
	if in.FollowsFrom == nil {
		in.FollowsFrom = []uuid.UUID{}
	}
	from, to, chosen, err := studyScope(in)
	if err == nil && len(in.FollowsFrom) > 0 && (from != board.Grounding || to != board.Reporting) {
		err = cardInputError("only an end-to-end study can follow from another")
	}
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	files, err := h.studyFiles(r, in.AttachmentIDs)
	if !answer(w, r, err) {
		return
	}
	var c studyView
	var dup db.ActiveDuplicateRow
	var teams map[string]uuid.UUID
	err = h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if in.ResearchID != nil {
			if _, err := tx.Q.GetResearch(r.Context(), db.GetResearchParams{WorkspaceID: ws, ID: *in.ResearchID}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return cardInputError("research is not in this workspace")
				}
				return err
			}
		}
		for _, parentID := range in.FollowsFrom {
			if _, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: parentID}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return cardInputError("follows_from contains an unknown card")
				}
				return err
			}
		}
		// Every column has a Team from the start, so the study starts at once.
		var err error
		if teams, err = team.ColumnTeams(r.Context(), tx, from, to, chosen); err != nil {
			return err
		}
		for _, col := range board.Stages[board.StageIndex(from) : board.StageIndex(to)+1] {
			if err := ensureTeam(r, tx, user, teams[col]); err != nil {
				return err
			}
		}
		assignee := "team"
		params := db.CreateCardParams{
			WorkspaceID: ws, Title: in.Title, Question: in.Question, OwnerID: user, ResearchID: in.ResearchID,
			Deadline: in.Deadline, FollowsFrom: in.FollowsFrom, ScopeFrom: from, ScopeTo: to, BoardColumn: from,
			AssigneeType: &assignee, Round: 1,
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
		params.Number = n
		card, err := tx.Q.CreateCard(r.Context(), params)
		if err != nil {
			return err
		}
		for col, pack := range teams {
			if err := tx.Q.InsertCardTeam(r.Context(), db.InsertCardTeamParams{WorkspaceID: ws, CardID: card.ID, BoardColumn: col, PackID: pack}); err != nil {
				return err
			}
		}
		if err := linkStudyFiles(r, tx, h.Blobs, card, files); err != nil {
			return err
		}
		if c, err = viewOf(r, tx, card); err != nil {
			return err
		}
		return tx.Emit(r.Context(), "card.created", &c.ID, store.User(user), map[string]any{"number": c.Number, "title": c.Title})
	})
	if errors.Is(err, errDuplicate) {
		writeJSON(w, 409, map[string]any{"error": fmt.Sprintf("an active study already asks this: #%d", dup.Number),
			"card_id": dup.ID, "number": dup.Number, "follows_allowed": from == board.Grounding && to == board.Reporting})
		return
	}
	var inputErr cardInputError
	var noBuiltIn team.NoBuiltInError
	switch {
	case errors.As(err, &inputErr):
		writeErr(w, 400, inputErr.Error())
		return
	case errors.As(err, &noBuiltIn):
		writeErr(w, 422, noBuiltIn.Error())
		return
	}
	if !answer(w, r, err) {
		return
	}
	if h.Engine != nil {
		h.Engine.Kick(c.WorkspaceID, c.ID)
	}
	writeJSON(w, 201, c)
}

var errTeamStarted = errors.New("that column has been reached")

// setCardTeams changes the Team of columns the study has not reached.
func (h *H) setCardTeams(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var in map[string]uuid.UUID
	if err := readJSON(r, &in); err != nil || len(in) == 0 {
		writeErr(w, 400, "send a Team id for each column to change")
		return
	}
	var out studyView
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		c, err := tx.Q.LockCard(r.Context(), db.LockCardParams{ID: id, WorkspaceID: ws})
		if err != nil {
			return err
		}
		if c.OwnerID != user {
			return forbidden("only the study's owner changes its Teams")
		}
		if c.AssigneeType == nil || *c.AssigneeType != "team" {
			return cardInputError("only a study assigned a Team has Teams to change")
		}
		for col, pack := range in {
			i := board.StageIndex(col)
			if i < board.StageIndex(c.ScopeFrom) || i > board.StageIndex(c.ScopeTo) {
				return cardInputError("column " + col + " is outside the study's scope")
			}
			if c.Ending != "" || i <= board.StageIndex(c.BoardColumn) {
				return errTeamStarted
			}
			if err := ensureTeam(r, tx, c.OwnerID, pack); err != nil {
				return err
			}
			if err := tx.Q.SetCardTeam(r.Context(), db.SetCardTeamParams{WorkspaceID: ws, CardID: id, BoardColumn: col, PackID: pack}); err != nil {
				return err
			}
		}
		if out, err = viewOf(r, tx, c); err != nil {
			return err
		}
		return tx.Emit(r.Context(), "card.teams_changed", &id, store.User(user), map[string]any{"teams": in})
	})
	var inputErr cardInputError
	switch {
	case errors.Is(err, errTeamStarted):
		writeErr(w, 409, err.Error())
	case errors.As(err, &inputErr):
		writeErr(w, 400, inputErr.Error())
	default:
		if answer(w, r, err) {
			writeJSON(w, 200, out)
		}
	}
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
	runs := []db.CardAgentRunsRow{}
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
		runs = []db.CardAgentRunsRow{}
	}
	writeJSON(w, http.StatusOK, runs)
}

func (h *H) getCard(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	// The card names its Teams, so a colleague who cannot see an uploaded
	// Team still reads which ones the study runs on.
	var v studyView
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		c, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		v, err = viewOf(r, tx, c)
		return err
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
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
	if h.Engine != nil {
		h.Engine.OnComment(comment.WorkspaceID, id, comment.AuthorID, comment.Body)
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

// readResolves are the Inbox kinds that only inform: reading one resolves it.
var readResolves = []string{"done", "middle_score", "no_tool", "autoresearch_card", "autoresearch_report"}

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
		if !slices.Contains(readResolves, item.Kind) {
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

// deleteCard removes a study and everything recorded on it, at any point of
// its work; only its owner may. Its open agent runs are cancelled first, so
// their tokens stop working. Its events stay, with the card's id in the
// payload; the database clears their card reference and drops its runs.
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
		if c.OwnerID != user {
			return forbidden("only the study's owner deletes it")
		}
		if err := service.CancelCardRuns(r.Context(), tx, user, id); err != nil {
			return err
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
		files, err := tx.Q.CardAttachments(r.Context(), db.CardAttachmentsParams{WorkspaceID: ws, CardID: &id})
		if err != nil {
			return err
		}
		if err := tx.Q.DeleteCard(r.Context(), db.DeleteCardParams{ID: id, WorkspaceID: ws}); err != nil {
			return err
		}
		h.deleteFilesAfterCommit(r, tx, files)
		return tx.Emit(r.Context(), "card.deleted", nil, store.User(user), map[string]any{"card_id": id, "number": c.Number})
	})
	if answer(w, r, err) {
		w.WriteHeader(http.StatusNoContent)
	}
}
