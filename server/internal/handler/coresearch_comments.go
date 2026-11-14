package handler

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/coresearch/doc"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var errCommentAuthor = errors.New("only the comment author may delete it")

func (h *H) coresearchComments(w http.ResponseWriter, r *http.Request) {
	var out any
	status := http.StatusOK
	e := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		if r.Method == http.MethodGet || r.Method == http.MethodPost && !strings.HasSuffix(r.URL.Path, "/resolve") {
			f, e := docFile(r, tx, ws, who, r.Method == http.MethodPost)
			if e != nil {
				return e
			}
			if r.Method == http.MethodGet {
				rows, e := tx.Q.ListCoresearchComments(r.Context(), db.ListCoresearchCommentsParams{WorkspaceID: ws, FileID: f.Record.ID})
				if rows == nil {
					rows = []db.CoresearchComment{}
				}
				out = rows
				return e
			}
			var in struct {
				Base     *int64      `json:"base"`
				From     *int32      `json:"from"`
				To       *int32      `json:"to"`
				Quote    string      `json:"quote"`
				Line     *int32      `json:"line"`
				Body     string      `json:"body"`
				Mentions []uuid.UUID `json:"mentions"`
			}
			if readJSON(r, &in) != nil || in.Base == nil || in.From == nil || in.To == nil || in.Line == nil || *in.Line < 1 || *in.Base < 0 || !utf8.ValidString(in.Body) || !utf8.ValidString(in.Quote) || strings.ContainsRune(in.Body, 0) || strings.ContainsRune(in.Quote, 0) || strings.TrimSpace(in.Body) == "" || len(in.Body) > 16384 || len(in.Quote) > 4096 {
				return coresearchInputError("invalid comment")
			}
			if _, e = tx.Q.GetCoresearchDoc(r.Context(), db.GetCoresearchDocParams{WorkspaceID: ws, FileID: f.Record.ID}); e != nil {
				return e
			}
			current, e := doc.Open(r.Context(), tx, f)
			if e != nil {
				return e
			}
			historical, e := doc.AtVersion(r.Context(), tx, f, *in.Base)
			if errors.Is(e, doc.ErrInvalid) {
				return coresearchInputError("invalid comment base")
			}
			if e != nil {
				return e
			}
			from, to := int(*in.From), int(*in.To)
			if _, e = doc.Slice(historical.Text, from, to); e != nil {
				return coresearchInputError("invalid comment anchor")
			}
			members, e := tx.Q.CoresearchPresenceMembers(r.Context(), db.CoresearchPresenceMembersParams{WorkspaceID: ws, ProjectID: f.Record.ProjectID})
			if e != nil {
				return e
			}
			allowed := make(map[uuid.UUID]bool, len(members))
			for _, m := range members {
				allowed[m.UserID] = true
			}
			mentions := make([]uuid.UUID, 0, len(in.Mentions))
			seen := make(map[uuid.UUID]bool, len(in.Mentions))
			for _, id := range in.Mentions {
				if !allowed[id] {
					return coresearchInputError("mentions must be session members")
				}
				if !seen[id] {
					mentions = append(mentions, id)
					seen[id] = true
				}
			}
			after := *in.Base
			for after < current.Version {
				steps, e := doc.Steps(r.Context(), tx, f, after, current.Version)
				if e != nil {
					return e
				}
				if len(steps) == 0 {
					return errors.New("missing comment anchor history")
				}
				for _, step := range steps {
					if step.Version != after+1 {
						return errors.New("noncontiguous comment anchor history")
					}
					change, e := doc.Parse(step.Changes)
					if e != nil {
						return e
					}
					from, to = change.MapPos(from, 1), change.MapPos(to, -1)
					if to < from {
						to = from
					}
					after = step.Version
				}
			}
			out, e = tx.Q.InsertCoresearchComment(r.Context(), db.InsertCoresearchCommentParams{WorkspaceID: ws, FileID: f.Record.ID, FromPos: int32(from), ToPos: int32(to), Quote: in.Quote, Line: *in.Line, AuthorUserID: who, Body: in.Body, Mentions: mentions})
			if e != nil {
				return e
			}
			status = http.StatusCreated
			return tx.Emit(r.Context(), "coresearch.comment.changed", nil, store.User(who), map[string]any{"project_id": f.Record.ProjectID, "file_id": f.Record.ID})
		}
		p, e := coresearchProject(r, tx, ws, who, true, false)
		if e != nil {
			return e
		}
		if p.Mode != "cowrite" {
			return pgx.ErrNoRows
		}
		id, e := coresearchID(r, "comment")
		if e != nil {
			return e
		}
		comment, e := tx.Q.GetCoresearchComment(r.Context(), db.GetCoresearchCommentParams{WorkspaceID: ws, ID: id, ProjectID: p.ID})
		if e != nil {
			return e
		}
		if r.Method == http.MethodDelete {
			if comment.AuthorUserID != who {
				return errCommentAuthor
			}
			if e = tx.Q.DeleteCoresearchComment(r.Context(), db.DeleteCoresearchCommentParams{WorkspaceID: ws, ID: id, AuthorUserID: who}); e != nil {
				return e
			}
			status = http.StatusNoContent
		} else {
			var in struct {
				Resolved *bool `json:"resolved"`
			}
			if e = readJSON(r, &in); e != nil && !errors.Is(e, io.EOF) {
				return coresearchInputError("invalid comment resolution")
			}
			resolved := in.Resolved == nil || *in.Resolved
			if resolved == (comment.ResolvedAt != nil) {
				out = comment
				return nil
			}
			out, e = tx.Q.ResolveCoresearchComment(r.Context(), db.ResolveCoresearchCommentParams{WorkspaceID: ws, ID: id, Resolved: resolved, ResolvedBy: who})
			if e != nil {
				return e
			}
		}
		return tx.Emit(r.Context(), "coresearch.comment.changed", nil, store.User(who), map[string]any{"project_id": p.ID, "file_id": comment.FileID})
	})
	if errors.Is(e, errCommentAuthor) {
		writeErr(w, http.StatusForbidden, e.Error())
		return
	}
	if docAnswer(w, r, e) {
		if status == http.StatusNoContent {
			w.WriteHeader(status)
		} else {
			writeJSON(w, status, out)
		}
	}
}
