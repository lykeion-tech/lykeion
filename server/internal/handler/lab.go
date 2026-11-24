package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/handin"
	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/pkg/agent"
	db "lykeion/server/pkg/db/generated"
)

type labMessage struct {
	AgentRunID   *uuid.UUID                   `json:"agent_run_id,omitempty"`
	CurrentDraft *handin.ManualDraft          `json:"current_draft,omitempty"`
	Proposal     *labmemory.DraftProposal     `json:"proposal,omitempty"`
	ID           uuid.UUID                    `json:"id"`
	Kind         string                       `json:"kind"`
	Body         string                       `json:"body"`
	Statements   []labmemory.DisplayStatement `json:"statements"`
	Evidence     []labmemory.DisplayRef       `json:"evidence"`
	CreatedAt    time.Time                    `json:"created_at"`
	Changed      bool                         `json:"changed,omitempty"`
}

func labPlainMessage(m db.LabMessage) labMessage {
	return labMessage{ID: m.ID, Kind: m.Kind, Body: m.Body, Statements: []labmemory.DisplayStatement{}, Evidence: []labmemory.DisplayRef{}, CreatedAt: m.CreatedAt}
}

func labDraftMessage(m db.LabMessage) labMessage {
	out := labPlainMessage(m)
	out.AgentRunID = m.AgentRunID
	var turn labmemory.DraftTurn
	if m.Kind == "question" && json.Unmarshal([]byte(m.Body), &turn) == nil {
		out.Body = turn.UserRequest
		out.CurrentDraft = &turn.CurrentDraft
	}
	return out
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
	tx := middleware.TxFrom(r.Context())
	purpose := r.URL.Query().Get("purpose")
	if middleware.IdentityFrom(r.Context()).Mobile {
		purpose = labmemory.StudySuggestionPurpose
	} else if purpose == labmemory.StudySuggestionPurpose {
		writeErr(w, 400, "invalid purpose")
		return
	}
	rows := []db.LabChat{}
	before, id := p.before, p.id
	for len(rows) <= int(p.limit) {
		page, err := tx.Q.ListPrivateLabChats(r.Context(), db.ListPrivateLabChatsParams{BeforeTime: before, BeforeID: id, PageSize: p.limit + 1, Purpose: purpose, Archived: r.URL.Query().Get("archived") == "true"})
		if err != nil {
			fail(w, r, err)
			return
		}
		for _, chat := range page {
			if chat.Purpose == labmemory.ManualDraftPurpose {
				ok, e := labmemory.DraftReadable(r.Context(), tx, chat)
				if e != nil {
					fail(w, r, e)
					return
				}
				if !ok {
					continue
				}
			}
			rows = append(rows, chat)
		}
		if len(page) < int(p.limit+1) {
			break
		}
		last := page[len(page)-1]
		before = &last.UpdatedAt
		id = last.ID
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
	var in struct {
		Purpose   string     `json:"purpose"`
		RuntimeID *uuid.UUID `json:"runtime_id"`
	}
	if err := readJSON(r, &in); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, 400, "invalid chat")
		return
	}
	if middleware.IdentityFrom(r.Context()).Mobile {
		if in.Purpose != "" || in.RuntimeID != nil {
			writeErr(w, 403, "study suggestion chats only")
			return
		}
		row, err := middleware.TxFrom(r.Context()).Q.CreateStudySuggestionChat(r.Context())
		if err != nil {
			fail(w, r, err)
			return
		}
		writeJSON(w, 201, map[string]any{"chat": row})
		return
	}
	if in.RuntimeID != nil {
		if in.Purpose != labmemory.ManualDraftPurpose {
			writeErr(w, 400, "machine binding requires a Decision draft")
			return
		}
		tx := middleware.TxFrom(r.Context())
		runtime, err := tx.Q.GetRuntime(r.Context(), db.GetRuntimeParams{WorkspaceID: tx.WS, ID: *in.RuntimeID})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && runtime.OwnerID != user(r) {
			fail(w, r, pgx.ErrNoRows)
			return
		}
		if err != nil {
			fail(w, r, err)
			return
		}
		if runtime.Status != "online" || runtime.UnhealthyReason != nil || time.Since(runtime.LastSeenAt) > time.Minute {
			writeJSON(w, 409, map[string]string{"code": "no_machine", "error": "Your machine is offline"})
			return
		}
	}
	var row db.LabChat
	var err error
	switch in.Purpose {
	case "", "answer":
		row, err = middleware.TxFrom(r.Context()).Q.CreateLabChat(r.Context())
	case labmemory.ManualDraftPurpose:
		row, err = middleware.TxFrom(r.Context()).Q.CreateManualDraftChat(r.Context())
	default:
		writeErr(w, 400, "invalid purpose")
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	if in.RuntimeID != nil {
		if err := middleware.TxFrom(r.Context()).SetDecisionBuilderRuntime(r.Context(), row.ID, user(r), *in.RuntimeID); err != nil {
			fail(w, r, err)
			return
		}
	}
	writeJSON(w, 201, map[string]any{"chat": row})
}

// deleteLabChat retains only the run identities needed to stop daemons and
// settle usage. All private conversation content is purged in this transaction.
func (h *H) deleteLabChat(w http.ResponseWriter, r *http.Request) {
	tx := middleware.TxFrom(r.Context())
	ctx := r.Context()
	if _, err := tx.Q.LockWorkspaceExecution(ctx, tx.WS); err != nil {
		fail(w, r, err)
		return
	}
	chat, err := privateLabChat(r, tx)
	if err != nil {
		fail(w, r, err)
		return
	}
	if err = DeleteLabChatInTx(ctx, tx, h.AgentRuns, user(r), chat); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteLabChatInTx deletes a private lab chat, cancels its live runs and purges
// its data. The caller holds LockWorkspaceExecution and has read the chat.
func DeleteLabChatInTx(ctx context.Context, tx *store.Tx, runs *service.AgentRuns, user uuid.UUID, chat db.LabChat) error {
	locked, err := tx.Q.LockPrivateLabChatRuns(ctx, chat.ID)
	if err != nil {
		return err
	}
	if _, err = tx.Q.DeletePrivateLabChat(ctx, chat.ID); err != nil {
		return err
	}
	if err = tx.PurgeDeletedLabChat(ctx, chat.ID); err != nil {
		return err
	}
	for _, run := range locked {
		if run.Status == "queued" || run.Status == "dispatched" || run.Status == "running" {
			if err = runs.CancelInTx(ctx, tx, user, run); err != nil {
				return err
			}
		}
		if err = tx.Q.DeleteAgentRunTokens(ctx, db.DeleteAgentRunTokensParams{WorkspaceID: tx.WS, AgentRunID: run.ID}); err != nil {
			return err
		}
	}
	return tx.Q.DeletePrivateLabMessages(ctx, chat.ID)
}

func privateLabChat(r *http.Request, tx *store.Tx) (db.LabChat, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return db.LabChat{}, pgx.ErrNoRows
	}
	chat, err := tx.Q.GetPrivateLabChat(r.Context(), id)
	// Study suggestions live on the phone; every other chat lives off it.
	if err == nil && middleware.IdentityFrom(r.Context()).Mobile != (chat.Purpose == labmemory.StudySuggestionPurpose) {
		return db.LabChat{}, pgx.ErrNoRows
	}
	return chat, err
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
	var pending bool
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
		if chat.Purpose == labmemory.ManualDraftPurpose {
			ok, e := labmemory.DraftReadable(ctx, tx, chat)
			if e != nil {
				return e
			}
			if !ok {
				return pgx.ErrNoRows
			}
		}
		pending, e = tx.Q.LabChatHasPendingRuns(ctx, chat.ID)
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
			if chat.Purpose == labmemory.ManualDraftPurpose {
				if row.Kind == "question" {
					if _, e := labmemory.DraftQuestionStudy(ctx, tx, who, row.Body, row.ContextCardID); e != nil {
						if errors.Is(e, labmemory.ErrNotFound) {
							continue
						}
						return e
					}
				}
				m = labDraftMessage(row)
			}
			if row.Kind == "answer" {
				ss, ee, safe, e := reader.Answer(ctx, row)
				if e != nil {
					return e
				}
				m.Body = ""
				m.Changed = safe && reader.Changed(row.ID)
				if study := labmemory.SuggestedStudy(row); safe && study != nil {
					// The proposed study is the whole reply; the lookup behind it stays out.
					m.Body = study.Description
				} else if safe {
					m.Statements = ss
					m.Evidence = ee
					if chat.Purpose == labmemory.ManualDraftPurpose && len(row.Proposal) > 0 {
						var p labmemory.DraftProposal
						if json.Unmarshal(row.Proposal, &p) == nil {
							m.Proposal = &p
						}
					}
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
	writeJSON(w, 200, map[string]any{"messages": messages, "next_cursor": next, "pending": pending})
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
	if chat.Purpose == labmemory.ManualDraftPurpose {
		h.askManualDraft(w, r, chat)
		return
	}
	var in struct {
		OperationID *uuid.UUID `json:"operation_id"`
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
			if in.ContextCard != nil || err != nil || (ref.Kind != "study" && ref.Kind != "record" && ref.Kind != "report") || ref.Node != "" {
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
	if in.OperationID != nil {
		if *in.OperationID == uuid.Nil {
			writeErr(w, 400, "invalid operation ID")
			return
		}
		prior, harness, model, e := tx.QuestionRequest(ctx, chat.ID, who, *in.OperationID)
		if e == nil {
			if prior.Body != in.Text || harness != in.Harness || model != in.Model || !reflect.DeepEqual(prior.ContextCardID, in.ContextCard) || !reflect.DeepEqual(prior.ContextRef, in.ContextRef) {
				writeErr(w, 409, "question already sent with different inputs")
				return
			}
			writeJSON(w, 202, map[string]any{"message": labPlainMessage(prior)})
			return
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			fail(w, r, e)
			return
		}
	}
	if in.Harness != "claude" && in.Harness != "codex" {
		writeErr(w, 400, "select a supported CLI")
		return
	}
	if err := agent.LabAvailability(in.Harness); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": "lab_unavailable", "error": err.Error()})
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
	if in.OperationID != nil {
		if err := tx.Q.SetManualDraftRequest(ctx, db.SetManualDraftRequestParams{RunID: run.ID, RequestID: *in.OperationID}); err != nil {
			fail(w, r, err)
			return
		}
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
	w.WriteHeader(204)
}
