package handler

import (
	"context"
	"encoding/json"
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
	"lykeion/server/internal/workflow"
	db "lykeion/server/pkg/db/generated"
)

type newCard struct {
	Machine    *string     `json:"machine"`
	StudyCLI   *string     `json:"study_cli"`
	AuditorCLI *string     `json:"auditor_cli"`
	ResearchID *uuid.UUID  `json:"research_id"`
	Title      string      `json:"title"`
	Question   string      `json:"question"`
	Deadline   *time.Time  `json:"deadline"`
	StartDate  *time.Time  `json:"start_date"`
	Assignee   *assigneeIn `json:"assignee"`
	Scope      *scopeIn    `json:"scope"`
	// AttachmentIDs are the caller's pending uploads the study takes as its files.
	AttachmentIDs []uuid.UUID  `json:"attachment_ids"`
	Services      []newService `json:"services"`
	Workflow      *string      `json:"workflow"`
}

func checkWorkflow(in newCard, from, to string, agent *uuid.UUID) error {
	if in.Workflow == nil {
		return nil
	}
	switch _, ok := workflow.Get(*in.Workflow); {
	case !ok:
		return cardInputError("no workflow named " + *in.Workflow)
	case agent == nil:
		return cardInputError("a workflow study is worked by one agent")
	case from != board.Experimentation || to != board.Experimentation:
		return cardInputError("a workflow study runs Experimentation only")
	case len(in.AttachmentIDs) == 0:
		return cardInputError("A Workflow runs its method on your data: attach at least one file.")
	}
	return nil
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

// studyScope gives the stages a researcher's study covers and who works it:
// the Teams they chose for some of its columns, or one agent for all of them.
// A lone Team with no scope keeps the study in its column.
func studyScope(in newCard) (from, to string, chosen map[string]uuid.UUID, agent *uuid.UUID, err error) {
	a := in.Assignee
	switch {
	case a == nil:
	case a.Type == "member":
		return "", "", nil, nil, cardInputError("a study is worked by one agent or by Teams")
	case (a.Type == "agent" && (a.Teams != nil || a.AgentID != nil)) || (a.Type == "team" && a.AgentID != nil):
		return "", "", nil, nil, cardInputError("a study is worked by one agent or by Teams, not both")
	case a.Type == "agent":
		if a.ID == nil {
			return "", "", nil, nil, cardInputError("assignee id required")
		}
		from, to, err = scopeOf(in.Scope)
		return from, to, nil, a.ID, err
	case a.Type != "team" || a.ID != nil:
		return "", "", nil, nil, checkAssigneeShape(a)
	}
	if from, to, err = scopeOf(in.Scope); err != nil || a == nil {
		return from, to, nil, nil, err
	}
	chosen = a.Teams
	if len(chosen) == 0 {
		return "", "", nil, nil, cardInputError("a Team assignment names no Team")
	}
	for col := range chosen {
		i := board.StageIndex(col)
		switch {
		case i < 0:
			return "", "", nil, nil, cardInputError("a Team is placed on a column outside the stages")
		case len(chosen) == 1 && in.Scope == nil:
			return col, col, chosen, nil, nil
		case i < board.StageIndex(from) || i > board.StageIndex(to):
			return "", "", nil, nil, cardInputError("a Team is placed outside the study's scope")
		}
	}
	return from, to, chosen, nil, nil
}

// checkStudyAgent refuses an agent the creator may not assign.
func checkStudyAgent(r *http.Request, tx *store.Tx, ws, creator, id uuid.UUID) error {
	refused := cardInputError("you can't start a study with this agent")
	var inputErr cardInputError
	if err := checkAssignee(r, tx, ws, creator, "agent", id); errors.As(err, &inputErr) {
		return refused
	} else if err != nil {
		return err
	}
	return nil
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
	a, err := tx.Q.GetAgentForShare(r.Context(), db.GetAgentForShareParams{WorkspaceID: ws, ID: id})
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
	StartDate     *time.Time `json:"start_date"`
	MachineLabel  string     `json:"machine_label"`
	MachineNotice string     `json:"machine_notice"`
	// HoldText says why a study held outside a low score waits for its owner.
	HoldText string                  `json:"hold_text"`
	Teams    map[string]cardTeamView `json:"teams"`
	OffLabel []string                `json:"off_label"`
	// Attachments are the files attached at creation, never their storage.
	Attachments []attachmentView  `json:"attachments"`
	Services    []serviceNameView `json:"services"`
}

type serviceNameView struct {
	Name string `json:"name"`
}

type attachmentView struct {
	RecordKey   string    `json:"record_key,omitempty"`
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	SizeBytes   int64     `json:"size_bytes"`
	ContentType string    `json:"content_type"`
}

func viewOf(r *http.Request, tx *store.Tx, c db.Card) (studyView, error) {
	v := studyView{Card: c, Teams: map[string]cardTeamView{}, OffLabel: []string{}, Attachments: []attachmentView{}, Services: []serviceNameView{}}
	var err error
	v.StartDate, err = tx.StudyStartDate(r.Context(), c.ID)
	if err != nil {
		return v, err
	}
	files, err := tx.Q.CardAttachments(r.Context(), db.CardAttachmentsParams{WorkspaceID: c.WorkspaceID, CardID: &c.ID})
	if err != nil {
		return v, err
	}
	for _, f := range files {
		attachment := attachmentView{ID: f.ID, Name: f.Name, SizeBytes: f.SizeBytes, ContentType: f.ContentType}
		if f.BlobKey == c.WorkspaceID.String()+"/attachments/"+f.ID.String() {
			attachment.RecordKey = f.RecordKey
		}
		v.Attachments = append(v.Attachments, attachment)
	}
	services, err := tx.Q.CardServices(r.Context(), db.CardServicesParams{WorkspaceID: c.WorkspaceID, CardID: &c.ID})
	if err != nil {
		return v, err
	}
	for _, s := range services {
		v.Services = append(v.Services, serviceNameView{Name: *s.ServiceName})
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
	if c.State == "blocked" && c.BlockKind == "" && c.Ending == "" {
		text, err := tx.Q.CardHoldText(r.Context(), db.CardHoldTextParams{WorkspaceID: c.WorkspaceID, CardID: &c.ID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return v, err
		}
		v.HoldText = text
	}
	if err := machineView(r.Context(), tx, &v); err != nil {
		return v, err
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
	if in.StartDate != nil && in.Deadline != nil && in.StartDate.After(*in.Deadline) {
		writeErr(w, 400, "start date must be on or before the due date")
		return
	}
	if err := checkServices(in.Services); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(in.Title) == "" {
		in.Title = titleFrom(in.Question)
	}
	from, to, chosen, agent, err := studyScope(in)
	validCLI := func(s *string) bool { return s == nil || *s == "claude" || *s == "codex" }
	if err == nil && (!validCLI(in.StudyCLI) || !validCLI(in.AuditorCLI)) {
		err = cardInputError("invalid CLI")
	}
	if err == nil && ((in.AuditorCLI == nil) != (in.StudyCLI == nil) || (in.AuditorCLI != nil && (agent != nil || *in.StudyCLI == *in.AuditorCLI))) {
		err = cardInputError("auditor CLI requires a Team and a different study CLI")
	}
	if err == nil {
		err = checkWorkflow(in, from, to, agent)
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
		if _, err := tx.Q.LockWorkspaceExecution(r.Context(), ws); err != nil {
			return err
		}
		if err := tx.Q.LockWorkspaceEvents(r.Context(), ws); err != nil {
			return err
		}
		services, err := pendingServices(r, tx, ws, user, in.Services)
		if err != nil {
			return err
		}
		if err := checkStudyFileLimits(append(slices.Clone(files), services...)); err != nil {
			return err
		}
		if in.ResearchID != nil {
			if _, err := tx.Q.GetResearch(r.Context(), db.GetResearchParams{WorkspaceID: ws, ID: *in.ResearchID}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return cardInputError("research is not in this workspace")
				}
				return err
			}
		}
		// Every column has a Team, or the study its agent, from the start, so
		// the study starts at once.
		assignee := "team"
		if agent != nil {
			assignee = "agent"
			if err := checkStudyAgent(r, tx, ws, user, *agent); err != nil {
				return err
			}
		} else {
			if teams, err = team.ColumnTeams(r.Context(), tx, from, to, chosen); err != nil {
				return err
			}
			for _, col := range board.Stages[board.StageIndex(from) : board.StageIndex(to)+1] {
				if err := ensureTeam(r, tx, user, teams[col]); err != nil {
					return err
				}
			}
		}
		params := db.CreateCardParams{
			WorkspaceID: ws, Title: in.Title, Question: in.Question, OwnerID: user, ResearchID: in.ResearchID,
			Deadline: in.Deadline, ScopeFrom: from, ScopeTo: to, BoardColumn: from,
			AssigneeType: &assignee, AssigneeID: agent, Round: 1, Workflow: in.Workflow,
			StudyCli: in.StudyCLI, AuditorCli: in.AuditorCLI,
		}
		if in.Workflow != nil {
			release := workflow.Release()
			params.WorkflowRelease = &release
		}
		if in.Machine != nil {
			params.Machine, err = h.machineFor(r.Context(), tx, user, *in.Machine)
			if err != nil {
				return err
			}
		}
		// No card may ask what an active card asks. The workspace lock makes two
		// identical cards created at once see each other.
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
		n, err := tx.Q.NextCardNumber(r.Context(), ws)
		if err != nil {
			return err
		}
		params.Number = n
		card, err := tx.Q.CreateCard(r.Context(), params)
		if err != nil {
			return err
		}
		if in.StartDate != nil {
			if err := tx.SetStudyStartDate(r.Context(), card.ID, in.StartDate); err != nil {
				return err
			}
		}
		for col, pack := range teams {
			if err := tx.Q.InsertCardTeam(r.Context(), db.InsertCardTeamParams{WorkspaceID: ws, CardID: card.ID, BoardColumn: col, PackID: pack}); err != nil {
				return err
			}
		}
		if err := linkStudyFiles(r, tx, h.Blobs, card, files); err != nil {
			return err
		}
		if err := linkStudyServices(r, tx, card, user, in.Services); err != nil {
			return err
		}
		if c, err = viewOf(r, tx, card); err != nil {
			return err
		}
		return tx.Emit(r.Context(), "card.created", &c.ID, store.User(user), map[string]any{"number": c.Number, "title": c.Title})
	})
	if errors.Is(err, errDuplicate) {
		writeJSON(w, 409, map[string]any{"error": fmt.Sprintf("an active study already asks this: #%d", dup.Number),
			"card_id": dup.ID, "number": dup.Number})
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

type cardListView struct {
	db.ListCardsRow
	Teams     map[string]cardTeamView `json:"teams"`
	StartDate *time.Time              `json:"start_date"`
	HoldText  string                  `json:"hold_text"`
}

func (h *H) listCards(w http.ResponseWriter, r *http.Request) {
	cards := []cardListView{}
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		rows, err := tx.Q.ListCards(r.Context(), ws)
		if err != nil {
			return err
		}
		starts, err := tx.StudyStartDates(r.Context())
		if err != nil {
			return err
		}
		assignments, err := tx.CardTeamAssignments(r.Context())
		if err != nil {
			return err
		}
		byCard := make(map[uuid.UUID]map[string]cardTeamView)
		for _, assignment := range assignments {
			if byCard[assignment.CardID] == nil {
				byCard[assignment.CardID] = make(map[string]cardTeamView)
			}
			byCard[assignment.CardID][assignment.Column] = cardTeamView{PackID: assignment.PackID, Name: assignment.Name, Stage: assignment.Stage}
		}
		for _, card := range rows {
			teams := byCard[card.ID]
			if teams == nil {
				teams = map[string]cardTeamView{}
			}
			var start *time.Time
			if date, ok := starts[card.ID]; ok {
				start = &date
			}
			var hold string
			if card.State == "blocked" && card.BlockKind == "" && card.Ending == "" {
				hold, err = tx.Q.CardHoldText(r.Context(), db.CardHoldTextParams{WorkspaceID: ws, CardID: &card.ID})
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
			}
			cards = append(cards, cardListView{ListCardsRow: card, Teams: teams, StartDate: start, HoldText: hold})
		}
		return nil
	})
	if err != nil {
		fail(w, r, err)
		return
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
		Body        string     `json:"body"`
		TargetRunID *uuid.UUID `json:"target_run_id"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Body) == "" {
		writeErr(w, http.StatusBadRequest, "body required")
		return
	}
	var comment db.Comment
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if _, err := tx.Q.LockCard(r.Context(), db.LockCardParams{WorkspaceID: ws, ID: id}); err != nil {
			return err
		}
		var attempt *int32
		if in.TargetRunID != nil {
			run, err := tx.Q.GetAgentRun(r.Context(), db.GetAgentRunParams{WorkspaceID: ws, ID: *in.TargetRunID})
			if err != nil {
				return err
			}
			if run.CardID == nil || *run.CardID != id {
				return pgx.ErrNoRows
			}
			if run.Status == "running" && run.Harness == "claude" && run.Kind == "role" {
				attempt = &run.Attempt
			}
		}
		var err error
		comment, err = tx.Q.CreateComment(r.Context(), db.CreateCommentParams{
			WorkspaceID: ws, CardID: id, AuthorID: user, Body: in.Body, ReplyRunID: in.TargetRunID, SteerAttempt: attempt,
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
	if h.Engine != nil && in.TargetRunID == nil {
		h.Engine.OnComment(comment.WorkspaceID, id, comment.AuthorID, comment.Body)
	}
	writeJSON(w, http.StatusCreated, comment)
}

func (h *H) cancelComment(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	comment, err := uuid.Parse(chi.URLParam(r, "comment"))
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	err = h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if _, err := tx.Q.LockCard(r.Context(), db.LockCardParams{WorkspaceID: ws, ID: id}); err != nil {
			return err
		}
		ok, err := tx.CancelStudyComment(r.Context(), id, comment, user)
		if err != nil {
			return err
		}
		if !ok {
			return pgx.ErrNoRows
		}
		return tx.Emit(r.Context(), "comment.cancelled", &id, store.User(user), map[string]string{"comment_id": comment.String()})
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listedComment is a comment with where it went: which run's work it
// reached and how, or nil while it waits for the next step.
type listedComment struct {
	db.Comment
	Delivery *commentDelivery `json:"delivery"`
}

type commentDelivery struct {
	How  string `json:"how"`
	Role string `json:"role"`
}

func (h *H) listComments(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	out := []listedComment{}
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		if _, err := tx.Q.GetCard(r.Context(), db.GetCardParams{WorkspaceID: ws, ID: id}); err != nil {
			return err
		}
		comments, err := tx.Q.ListComments(r.Context(), db.ListCommentsParams{WorkspaceID: ws, CardID: id})
		if err != nil {
			return err
		}
		receipts, err := tx.Q.CommentDeliveries(r.Context(), db.CommentDeliveriesParams{WorkspaceID: ws, CardID: id})
		if err != nil {
			return err
		}
		went := map[uuid.UUID]*commentDelivery{}
		for _, body := range receipts {
			var d service.CommentDelivery
			if json.Unmarshal(body, &d) == nil {
				went[d.CommentID] = &commentDelivery{How: d.How, Role: d.Role}
			}
		}
		for _, c := range comments {
			out = append(out, listedComment{Comment: c, Delivery: went[c.ID]})
		}
		return nil
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
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

// resolveInbox is the owner saying a machine's wait is dealt with: the
// machine is cleared, as a pass at daemon start would, which is the only
// other way a fence breach clears. Only an open wait of the caller's own
// does anything.
func (h *H) resolveInbox(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		item, err := tx.Q.ResolveOpenOwnerItem(r.Context(), db.ResolveOpenOwnerItemParams{ID: id, WorkspaceID: ws, UserID: user})
		if err != nil {
			return err
		}
		if item.RuntimeID != nil {
			// A machine that is no longer the caller's stays as it is.
			if err := service.ClearRuntime(r.Context(), tx, user, *item.RuntimeID); err != nil && !errors.Is(err, service.ErrNotYours) {
				return err
			}
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
		blobKeys, err := tx.Q.CardBlobKeys(r.Context(), db.CardBlobKeysParams{WorkspaceID: ws, CardID: id})
		if err != nil {
			return err
		}
		services, err := tx.Q.CardServices(r.Context(), db.CardServicesParams{WorkspaceID: ws, CardID: &id})
		if err != nil {
			return err
		}
		files = append(files, services...)
		if err := tx.Q.DeleteCard(r.Context(), db.DeleteCardParams{ID: id, WorkspaceID: ws}); err != nil {
			return err
		}
		h.deleteFilesAfterCommit(r, tx, files)
		h.deleteBlobsAfterCommit(r, tx, blobKeys)
		return tx.Emit(r.Context(), "card.deleted", nil, store.User(user), map[string]any{"card_id": id, "number": c.Number})
	})
	if answer(w, r, err) {
		w.WriteHeader(http.StatusNoContent)
	}
}

// machineFor resolves only runtimes the study owner may use in this workspace.
func (h *H) machineFor(ctx context.Context, tx *store.Tx, user uuid.UUID, ref string) (*string, error) {
	if ref == "" || ref == "mine" {
		return nil, nil
	}
	rows, err := tx.Q.ListRuntimes(ctx, tx.WS)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		label := row.DeviceName
		if row.CustomName != nil {
			label = *row.CustomName
		}
		if row.DaemonID != ref && label != ref {
			continue
		}
		rt, err := tx.Q.GetRuntime(ctx, db.GetRuntimeParams{WorkspaceID: tx.WS, ID: row.ID})
		if err != nil {
			return nil, err
		}
		allowed, err := service.RuntimeAllowsUser(ctx, tx, rt, user)
		if err != nil {
			return nil, err
		}
		if allowed {
			return &rt.DaemonID, nil
		}
	}
	return nil, cardInputError("No machine named " + ref + " is shared with you")
}

func machineView(ctx context.Context, tx *store.Tx, v *studyView) error {
	if v.Machine == nil {
		v.MachineLabel = "My machines"
		return nil
	}
	rows, err := tx.Q.ListRuntimes(ctx, tx.WS)
	if err != nil {
		return err
	}
	available := map[string]bool{}
	allowedAny := false
	for _, row := range rows {
		if row.DaemonID != *v.Machine {
			continue
		}
		rt, err := tx.Q.GetRuntime(ctx, db.GetRuntimeParams{WorkspaceID: tx.WS, ID: row.ID})
		if err != nil {
			return err
		}
		if v.MachineLabel == "" {
			label := rt.DeviceName
			if rt.CustomName != nil {
				label = *rt.CustomName
			}
			owner, err := tx.Q.GetUser(ctx, rt.OwnerID)
			if err != nil {
				return err
			}
			ownerName := owner.Name
			if ownerName == "" {
				ownerName = owner.Email
			}
			v.MachineLabel = label + " (" + ownerName + ")"
		}
		allowed, err := service.RuntimeAllowsUser(ctx, tx, rt, v.OwnerID)
		if err != nil {
			return err
		}
		if !allowed {
			continue
		}
		allowedAny = true
		available[rt.Harness] = true
	}
	if v.MachineLabel == "" {
		v.MachineLabel = *v.Machine
	}
	if !allowedAny {
		v.MachineNotice = "This study's machine is no longer shared with you; choose another."
		return nil
	}
	needed := map[string]bool{}
	if v.StudyCli != nil && v.AuditorCli != nil {
		runs, err := tx.Q.CardAgentRuns(ctx, db.CardAgentRunsParams{WorkspaceID: tx.WS, CardID: &v.ID})
		if err != nil {
			return err
		}
		for _, run := range runs {
			if run.Status == "queued" || run.Status == "dispatched" || run.Status == "running" {
				needed[run.Harness] = true
			}
		}
	} else {
		for _, t := range v.Teams {
			agents, err := tx.Q.TeamAgentsFor(ctx, db.TeamAgentsForParams{WorkspaceID: tx.WS, PackID: t.PackID, UserID: v.OwnerID})
			if err != nil {
				return err
			}
			for _, member := range agents {
				harness, err := tx.Q.AgentHarnessForHistory(ctx, db.AgentHarnessForHistoryParams{WorkspaceID: tx.WS, ID: member.AgentID})
				if err != nil {
					return err
				}
				needed[harness] = true
			}
		}
	}
	for _, harness := range []struct{ key, label string }{{"claude", "Claude Code"}, {"codex", "Codex"}} {
		if needed[harness.key] && !available[harness.key] {
			v.MachineNotice = "This study's machine has no " + harness.label + " shared with you; choose another."
			break
		}
	}
	return nil
}

func (h *H) setCardMachine(w http.ResponseWriter, r *http.Request) {
	id, ok := cardID(w, r)
	if !ok {
		return
	}
	var out studyView
	err := h.inWS(r, func(tx *store.Tx, ws, user uuid.UUID) error {
		if _, err := tx.Q.LockWorkspaceExecution(r.Context(), ws); err != nil {
			return err
		}
		if err := tx.Q.WaitLockCard(r.Context(), id); err != nil {
			return err
		}
		c, err := tx.Q.LockCard(r.Context(), db.LockCardParams{WorkspaceID: ws, ID: id})
		if err != nil {
			return err
		}
		if c.OwnerID != user {
			return forbidden("only the study's owner changes its machine")
		}
		var in struct {
			Machine *string `json:"machine"`
		}
		if err := readJSON(r, &in); err != nil {
			return cardInputError("invalid machine: " + err.Error())
		}
		var machine *string
		if in.Machine != nil {
			machine, err = h.machineFor(r.Context(), tx, user, *in.Machine)
			if err != nil {
				return err
			}
		}
		if _, err := tx.Q.SetCardMachine(r.Context(), db.SetCardMachineParams{WorkspaceID: ws, ID: id, Machine: machine}); err != nil {
			return err
		}
		c.Machine = machine
		if err := tx.Emit(r.Context(), "card.machine_changed", &id, store.User(user), map[string]any{"machine": machine}); err != nil {
			return err
		}
		if h.Engine != nil {
			if err := h.Engine.Resume(r.Context(), tx, c); err != nil {
				return err
			}
		}
		out, err = viewOf(r, tx, c)
		return err
	})
	var inputErr cardInputError
	if errors.As(err, &inputErr) {
		writeErr(w, 400, inputErr.Error())
		return
	}
	if answer(w, r, err) {
		writeJSON(w, 200, out)
	}
}
