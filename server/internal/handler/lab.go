package handler

import (
	"encoding/base64"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

type labMessage struct {
	ID         uuid.UUID                    `json:"id"`
	Kind       string                       `json:"kind"`
	Body       string                       `json:"body"`
	Statements []labmemory.DisplayStatement `json:"statements"`
	Evidence   []labmemory.DisplayRef       `json:"evidence"`
	CreatedAt  time.Time                    `json:"created_at"`
}

func labPlainMessage(m db.LabMessage) labMessage {
	return labMessage{m.ID, m.Kind, m.Body, []labmemory.DisplayStatement{}, []labmemory.DisplayRef{}, m.CreatedAt}
}

func labCursor(t time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(t.Format(time.RFC3339Nano) + "|" + id.String()))
}

type labPage struct {
	limit  int32
	before *time.Time
	id     uuid.UUID
}

func labPagination(w http.ResponseWriter, r *http.Request) (labPage, bool) {
	page := labPage{limit: 50}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			writeErr(w, 400, "limit must be 1 to 100")
			return page, false
		}
		page.limit = int32(n)
	}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		data, err := base64.RawURLEncoding.DecodeString(raw)
		ts, id, ok := strings.Cut(string(data), "|")
		t, e := time.Parse(time.RFC3339Nano, ts)
		u, f := uuid.Parse(id)
		if err != nil || !ok || e != nil || f != nil {
			writeErr(w, 400, "invalid cursor")
			return page, false
		}
		page.before = &t
		page.id = u
	}
	return page, true
}

func (h *H) listLabChats(w http.ResponseWriter, r *http.Request) {
	p, ok := labPagination(w, r)
	if !ok {
		return
	}
	rows, err := middleware.TxFrom(r.Context()).Q.ListPrivateLabChats(r.Context(), db.ListPrivateLabChatsParams{BeforeTime: p.before, BeforeID: p.id, PageSize: p.limit + 1})
	if err != nil {
		fail(w, r, err)
		return
	}
	next := ""
	if len(rows) > int(p.limit) {
		rows = rows[:p.limit]
		last := rows[len(rows)-1]
		next = labCursor(last.UpdatedAt, last.ID)
	}
	if rows == nil {
		rows = []db.LabChat{}
	}
	writeJSON(w, 200, map[string]any{"chats": rows, "next_cursor": next})
}

func (h *H) createLabChat(w http.ResponseWriter, r *http.Request) {
	row, err := middleware.TxFrom(r.Context()).Q.CreateLabChat(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 201, map[string]any{"chat": row})
}

func privateLabChat(r *http.Request, tx *store.Tx) (db.LabChat, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return db.LabChat{}, pgx.ErrNoRows
	}
	return tx.Q.GetPrivateLabChat(r.Context(), id)
}

func (h *H) listLabMessages(w http.ResponseWriter, r *http.Request) {
	ws, err := uuid.Parse(r.Header.Get("X-Workspace-ID"))
	if err != nil {
		writeErr(w, 404, "workspace not found")
		return
	}
	p, ok := labPagination(w, r)
	if !ok {
		return
	}
	var messages []labMessage
	var next string
	who := user(r)
	ctx := r.Context()
	err = h.Store.InWorkspaceSnapshot(ctx, ws, who, func(tx *store.Tx) error {
		if _, e := tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: ws, UserID: who}); e != nil {
			return e
		}
		chat, e := privateLabChat(r, tx)
		if e != nil {
			return e
		}
		rows, e := tx.Q.ListPrivateLabMessages(ctx, db.ListPrivateLabMessagesParams{ChatID: chat.ID, BeforeTime: p.before, BeforeID: p.id, PageSize: p.limit + 1})
		if e != nil {
			return e
		}
		next = ""
		if len(rows) > int(p.limit) {
			rows = rows[:p.limit]
			last := rows[len(rows)-1]
			next = labCursor(last.CreatedAt, last.ID)
		}
		slices.Reverse(rows)
		messages = make([]labMessage, 0, len(rows))
		reader := labmemory.NewDisplayReader(tx, who, chat.ID)
		for _, row := range rows {
			m := labPlainMessage(row)
			if row.Kind == "answer" {
				ss, ee, safe, e := reader.Answer(ctx, row)
				if e != nil {
					return e
				}
				m.Body = ""
				if safe {
					m.Statements = ss
					m.Evidence = ee
				} else {
					m.Kind = "failure"
					m.Body = "This answer is unavailable because its evidence has changed."
				}
			}
			messages = append(messages, m)
		}
		return nil
	})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"messages": messages, "next_cursor": next})
}

func (h *H) askLabQuestion(w http.ResponseWriter, r *http.Request) {
	tx := middleware.TxFrom(r.Context())
	ctx := r.Context()
	who := user(r)
	chat, err := privateLabChat(r, tx)
	if err != nil {
		fail(w, r, err)
		return
	}
	var in struct {
		Text        string     `json:"text"`
		ContextCard *uuid.UUID `json:"context_card_id"`
		ContextRef  *string    `json:"context_ref"`
		Harness     string     `json:"harness"`
		Model       string     `json:"model"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid question")
		return
	}
	if strings.TrimSpace(in.Text) == "" || utf8.RuneCountInString(in.Text) > 8000 {
		writeErr(w, 400, "question must contain 1 to 8000 characters")
		return
	}
	in.Text = strings.TrimSpace(in.Text)
	if in.ContextRef != nil || in.ContextCard != nil {
		var ref labmemory.Ref
		if in.ContextRef != nil {
			ref, err = labmemory.ParseRef(*in.ContextRef)
			if in.ContextCard != nil || err != nil || (ref.Kind != "study" && ref.Kind != "record") {
				writeErr(w, 400, "invalid context reference")
				return
			}
			if ref.Kind == "study" {
				in.ContextCard = &ref.ID
				in.ContextRef = nil
			} else {
				s := ref.String()
				in.ContextRef = &s
			}
		} else {
			ref = labmemory.Ref{Kind: "study", ID: *in.ContextCard}
		}
		open, e := labmemory.CanOpen(ctx, tx, who, ref)
		if e != nil {
			fail(w, r, e)
			return
		}
		if !open {
			writeErr(w, 400, "context unavailable")
			return
		}
	}
	if in.Harness == "codex" {
		writeJSON(w, 422, map[string]string{"code": "unsupported_harness", "error": agent.ErrLabHarness.Error()})
		return
	}
	if in.Harness != "claude" {
		writeErr(w, 400, "select a supported CLI")
		return
	}
	available, err := tx.Q.AristotleRuntimeAvailable(ctx, db.AristotleRuntimeAvailableParams{Harness: in.Harness, RequireHealthy: true})
	if err != nil {
		fail(w, r, err)
		return
	}
	if !available {
		writeJSON(w, 409, map[string]string{"code": "no_machine", "error": "Your machine is offline"})
		return
	}
	if in.Model != "" {
		ok, e := tx.Q.AristotleRuntimeModelAvailable(ctx, db.AristotleRuntimeModelAvailableParams{Harness: in.Harness, Model: in.Model})
		if e != nil {
			fail(w, r, e)
			return
		}
		if !ok {
			writeErr(w, 400, "model is not offered by an own runtime")
			return
		}
	}
	run, err := labmemory.EnqueueWithContext(ctx, tx, h.AgentRuns, chat, who, in.Text, in.ContextCard, in.ContextRef, in.Harness, in.Model)
	if err != nil {
		fail(w, r, err)
		return
	}
	msg, err := tx.Q.GetLabQuestion(ctx, run.ID)
	if err != nil {
		fail(w, r, err)
		return
	}
	title := []rune(in.Text)
	if len(title) > 40 {
		title = append(title[:40], '…')
	}
	if err := tx.Q.TouchLabChat(ctx, db.TouchLabChatParams{ID: chat.ID, Title: string(title)}); err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 202, map[string]any{"message": labPlainMessage(msg)})
}

func (h *H) flagLabLesson(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		fail(w, r, pgx.ErrNoRows)
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if readJSON(r, &in) != nil || utf8.RuneCountInString(in.Reason) > 500 {
		writeErr(w, 400, "reason must be at most 500 characters")
		return
	}
	ctx := r.Context()
	q := middleware.TxFrom(ctx).Q
	if _, err = q.FlagLabLesson(ctx, id); err != nil {
		fail(w, r, err)
		return
	}
	if err = q.RecordLabFlag(ctx, db.RecordLabFlagParams{LessonID: id, Reason: in.Reason}); err != nil {
		fail(w, r, err)
		return
	}
	if h.LessonWritesEnabled {
		if err = q.QueueLabFlagRewrite(ctx, id); err != nil {
			fail(w, r, err)
			return
		}
	}
	w.WriteHeader(204)
}
