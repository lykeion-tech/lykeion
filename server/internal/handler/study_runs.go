package handler

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/board"
	"lykeion/server/internal/store"
)

func studyRunGroup(w http.ResponseWriter, r *http.Request) (store.RunGroup, bool) {
	card, err := uuid.Parse(chi.URLParam(r, "card"))
	stage := chi.URLParam(r, "stage")
	round, roundErr := strconv.ParseInt(chi.URLParam(r, "round"), 10, 32)
	if err != nil || card == uuid.Nil || board.StageIndex(stage) < 0 || roundErr != nil || round < 1 {
		writeErr(w, 404, "study run not found")
		return store.RunGroup{}, false
	}
	return store.RunGroup{CardID: card, Stage: stage, Round: int32(round)}, true
}

type studyRunCursor struct {
	store.RunGroup
	Workspace uuid.UUID
	Project   *uuid.UUID
	Before    time.Time
}

type mobileStudy struct {
	ID     uuid.UUID          `json:"id"`
	Number int32              `json:"number"`
	Title  string             `json:"title"`
	Stage  string             `json:"stage"`
	State  string             `json:"state"`
	Latest *store.StudyRunRow `json:"latest_run"`
}

func (h *H) mobileStudies(w http.ResponseWriter, r *http.Request) {
	studies := []mobileStudy{}
	err := h.inWS(r, func(tx *store.Tx, ws, _ uuid.UUID) error {
		cards, err := tx.Q.ListCards(r.Context(), ws)
		if err != nil {
			return err
		}
		runs, err := tx.LatestStudyRuns(r.Context())
		if err != nil {
			return err
		}
		latest := make(map[uuid.UUID]*store.StudyRunRow, len(runs))
		for i := range runs {
			latest[runs[i].CardID] = &runs[i]
		}
		for _, card := range cards {
			studies = append(studies, mobileStudy{ID: card.ID, Number: card.Number, Title: card.Title,
				Stage: card.BoardColumn, State: card.State, Latest: latest[card.ID]})
		}
		return nil
	})
	if answer(w, r, err) {
		writeJSON(w, 200, studies)
	}
}

func (h *H) listStudyRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var project *uuid.UUID
	if q.Has("project_id") {
		p, err := uuid.Parse(q.Get("project_id"))
		if err != nil || p == uuid.Nil {
			writeErr(w, 400, "invalid project_id")
			return
		}
		project = &p
	}
	limit := 30
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > 100 {
			writeErr(w, 400, "limit must be between 1 and 100")
			return
		}
		limit = n
	}
	ws, _ := h.scope(r)
	var cursor studyRunCursor
	var before *time.Time
	if q.Has("cursor") {
		raw, err := base64.RawURLEncoding.DecodeString(q.Get("cursor"))
		if err != nil || len(raw) > 512 || json.Unmarshal(raw, &cursor) != nil || cursor.Workspace != ws || (cursor.Project == nil) != (project == nil) || project != nil && *cursor.Project != *project || cursor.Before.IsZero() || cursor.CardID == uuid.Nil || board.StageIndex(cursor.Stage) < 0 || cursor.Round < 1 {
			writeErr(w, 400, "invalid cursor")
			return
		}
		before = &cursor.Before
	}
	var items []store.StudyRunRow
	err := h.inWS(r, func(tx *store.Tx, _, _ uuid.UUID) error {
		var err error
		items, err = tx.StudyRunGroups(r.Context(), project, nil, before, cursor.RunGroup, limit+1)
		return err
	})
	if !answer(w, r, err) {
		return
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		last := items[limit-1]
		raw, _ := json.Marshal(studyRunCursor{RunGroup: last.RunGroup, Before: last.CreatedAt, Workspace: ws, Project: project})
		s := base64.RawURLEncoding.EncodeToString(raw)
		next = &s
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}

type studyRunDetail struct {
	store.StudyRunRow
	Outcome      string                   `json:"outcome"`
	Progress     string                   `json:"progress"`
	Participants []store.StudyParticipant `json:"participants"`
	Outputs      []store.StudyRunOutput   `json:"outputs"`
	Activity     []store.StudyActivity    `json:"activity"`
}

func (h *H) studyRun(w http.ResponseWriter, r *http.Request) {
	g, ok := studyRunGroup(w, r)
	if !ok {
		return
	}
	var d studyRunDetail
	ctx := r.Context()
	err := h.inWS(r, func(tx *store.Tx, _, _ uuid.UUID) error {
		rows, err := tx.StudyRunGroups(ctx, nil, &g, nil, store.RunGroup{}, 1)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return pgx.ErrNoRows
		}
		d.StudyRunRow = rows[0]
		d.Participants, err = tx.StudyParticipantsForGroup(ctx, g)
		if err != nil {
			return err
		}
		d.Outputs, err = tx.StudyRunOutputs(ctx, g, nil)
		if err != nil {
			return err
		}
		d.Activity, err = tx.StudyRunActivity(ctx, g)
		return err
	})
	if !answer(w, r, err) {
		return
	}
	var at, reasonAt time.Time
	var reason, blocker, waiting string
	for _, p := range d.Participants {
		if p.Kind != "report" && p.Waiting != "" {
			waiting = p.Waiting
		}
		if p.Kind != "role" || (p.Role != "agent" && p.Role != "leader") || p.CompletedAt == nil {
			continue
		}
		if p.Status == d.Status && p.TerminalReason != "" && !p.CompletedAt.Before(reasonAt) {
			reasonAt = *p.CompletedAt
			reason = p.TerminalReason
		}
		var hand struct {
			Status, Answer, Reason, Report string
			Decision                       *struct{ Title string }
		}
		if p.Status != "completed" || json.Unmarshal(p.HandIn, &hand) != nil || p.CompletedAt.Before(at) {
			continue
		}
		result := hand.Answer
		if result == "" {
			result = hand.Reason
		}
		if result == "" {
			result = hand.Report
		}
		blocker = ""
		switch hand.Status {
		case "could_not_do":
			blocker = hand.Reason
		case "ask":
			if hand.Decision != nil {
				blocker = hand.Decision.Title
			}
		}
		d.Outcome = result
		at = *p.CompletedAt
	}
	switch d.Status {
	case "failed", "cancelled":
		d.Outcome = reason
		if d.StopReason != "" {
			d.Outcome = d.StopReason
		}
	case "completed":
	default:
		d.Outcome = ""
		switch d.Status {
		case "queued":
			d.Progress = waiting
			if d.Progress == "" {
				d.Progress = "Waiting for execution."
			}
		case "dispatched":
			d.Progress = "Dispatched to a machine."
		case "running":
			d.Progress = "Work is in progress."
		case "blocked":
			d.Progress = blocker
			if d.Progress == "" {
				d.Progress = "This round needs attention."
			}
		case "reviewing":
			d.Progress = "Waiting for the next stage decision."
		}
	}
	writeJSON(w, 200, d)
}

func (h *H) previewStudyRunOutput(w http.ResponseWriter, r *http.Request) {
	g, ok := studyRunGroup(w, r)
	if !ok {
		return
	}
	ws, wsErr := downloadWorkspace(r)
	id, err := uuid.Parse(chi.URLParam(r, "output"))
	if err != nil || wsErr != nil {
		writeErr(w, 404, "output not found")
		return
	}
	var file store.StudyRunOutput
	err = h.memberTx(r, ws, false, func(tx *store.Tx) error {
		files, err := tx.StudyRunOutputs(r.Context(), g, &id)
		if err != nil {
			return err
		}
		if len(files) == 0 || !files[0].Available {
			return pgx.ErrNoRows
		}
		file = files[0]
		return nil
	})
	if !answer(w, r, err) {
		return
	}
	var preview struct {
		Text      string `json:"text"`
		Truncated bool   `json:"truncated"`
		Supported bool   `json:"supported"`
	}
	switch strings.ToLower(path.Ext(file.Name)) {
	case ".txt", ".md", ".markdown", ".csv", ".tsv":
		rc, err := h.Blobs.Get(r.Context(), file.Blob)
		if err != nil {
			logoFail(w, r, err)
			return
		}
		defer rc.Close()
		const cap = 64 << 10
		data, err := io.ReadAll(io.LimitReader(rc, cap+1))
		if !answer(w, r, err) {
			return
		}
		preview.Truncated = len(data) > cap
		if preview.Truncated {
			data = data[:cap]
			for i := len(data) - 1; i >= 0 && i >= len(data)-utf8.UTFMax; i-- {
				if utf8.RuneStart(data[i]) {
					if !utf8.FullRune(data[i:]) {
						data = data[:i]
					}
					break
				}
			}
		}
		if !bytes.ContainsRune(data, 0) && utf8.Valid(data) {
			preview.Supported = true
			preview.Text = string(data)
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, 200, preview)
}
