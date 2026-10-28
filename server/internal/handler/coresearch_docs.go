package handler

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/coresearch/doc"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

func docAnswer(w http.ResponseWriter, r *http.Request, e error) bool {
	var behind doc.ErrBehind
	if errors.As(e, &behind) {
		writeJSON(w, 409, map[string]any{"current": behind.Current})
		return false
	}
	if errors.Is(e, doc.ErrInvalid) {
		writeErr(w, 400, e.Error())
		return false
	}
	if errors.Is(e, doc.ErrHeadChanged) {
		writeErr(w, 409, e.Error())
		return false
	}
	return coresearchAnswer(w, r, e)
}
func docFile(r *http.Request, tx *store.Tx, ws, who uuid.UUID, write bool) (doc.File, error) {
	p, e := coresearchProject(r, tx, ws, who, write, false)
	if e != nil {
		return doc.File{}, e
	}
	if p.Mode != "cowrite" {
		return doc.File{}, pgx.ErrNoRows
	}
	f, e := coresearchFile(r, tx, ws, p.ID)
	if e != nil {
		return doc.File{}, e
	}
	if !doc.Eligible(f) {
		return doc.File{}, pgx.ErrNoRows
	}
	return doc.File{Record: f}, nil
}

// Opening may hydrate a blob, so authorization transactions surround storage I/O.
func (h *H) openCoresearchDoc(w http.ResponseWriter, r *http.Request) {
	ws, e := uuid.Parse(r.Header.Get("X-Workspace-ID"))
	if e != nil {
		coresearchAnswer(w, r, pgx.ErrNoRows)
		return
	}
	var out doc.Doc
	for attempt := 0; attempt < 4; attempt++ {
		var f doc.File
		var head db.CoresearchFileVersion
		exists := false
		e = h.memberTx(r, ws, false, func(tx *store.Tx) error {
			var e error
			f, e = docFile(r, tx, ws, user(r), false)
			if e != nil {
				return e
			}
			_, e = tx.Q.GetCoresearchDoc(r.Context(), db.GetCoresearchDocParams{WorkspaceID: ws, FileID: f.Record.ID})
			if e == nil {
				exists = true
				return nil
			}
			if !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
			head, e = tx.Q.CoresearchFileHead(r.Context(), db.CoresearchFileHeadParams{WorkspaceID: ws, FileID: f.Record.ID})
			return e
		})
		if e != nil {
			break
		}
		if !exists {
			if head.Size > coresearch.MaxTextBytes {
				e = doc.ErrInvalid
				break
			}
			var rc io.ReadCloser
			rc, e = h.Blobs.Get(r.Context(), head.BlobKey)
			if e != nil {
				break
			}
			var b []byte
			b, e = io.ReadAll(io.LimitReader(rc, coresearch.MaxTextBytes+1))
			closeErr := rc.Close()
			if e == nil {
				e = closeErr
			}
			if e != nil {
				break
			}
			if int64(len(b)) != head.Size || !utf8.Valid(b) {
				e = doc.ErrInvalid
				break
			}
			f.InitialText = string(b)
			f.InitialHead = head.N
		}
		e = h.coresearchSaveTx(r, ws, func(tx *store.Tx) error {
			fresh, e := docFile(r, tx, ws, user(r), false)
			if e != nil {
				return e
			}
			fresh.InitialText, fresh.InitialHead = f.InitialText, f.InitialHead
			out, e = doc.Open(r.Context(), tx, fresh)
			return e
		})
		if !errors.Is(e, doc.ErrHeadChanged) {
			break
		}
	}
	if docAnswer(w, r, e) {
		writeJSON(w, 200, out)
	}
}
func (h *H) coresearchDocSteps(w http.ResponseWriter, r *http.Request) {
	var out any
	e := h.inWS(r, func(tx *store.Tx, ws, who uuid.UUID) error {
		f, e := docFile(r, tx, ws, who, r.Method == "POST")
		if e != nil {
			return e
		}
		if r.Method == "GET" {
			after, e := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
			if e != nil || after < 0 {
				return doc.ErrInvalid
			}
			d, e := tx.Q.GetCoresearchDoc(r.Context(), db.GetCoresearchDocParams{WorkspaceID: ws, FileID: f.Record.ID})
			if e != nil {
				return e
			}
			if after > d.Version {
				return doc.ErrBehind{Current: d.Version}
			}
			out, e = doc.Steps(r.Context(), tx, f, after, math.MaxInt64)
			return e
		}
		var in struct {
			Base     *int64            `json:"base"`
			ClientID string            `json:"client_id"`
			Steps    []json.RawMessage `json:"steps"`
		}
		if e = readJSON(r, &in); e != nil || in.Base == nil {
			return doc.ErrInvalid
		}
		steps := make([]doc.Step, len(in.Steps))
		for i, raw := range in.Steps {
			steps[i] = doc.Step{Changes: raw, ClientID: in.ClientID}
		}
		version, e := doc.Push(r.Context(), tx, f, *in.Base, steps, doc.Author{UserID: &who})
		out = map[string]int64{"version": version}
		return e
	})
	if docAnswer(w, r, e) {
		writeJSON(w, 200, out)
	}
}
func (h *H) coresearchDocPresence(w http.ResponseWriter, r *http.Request) {
	ws, e := uuid.Parse(r.Header.Get("X-Workspace-ID"))
	if e != nil {
		coresearchAnswer(w, r, pgx.ErrNoRows)
		return
	}
	var f doc.File
	var members []db.CoresearchPresenceMembersRow
	var length int
	authorize := func() error {
		return h.memberTx(r, ws, false, func(tx *store.Tx) error {
			var e error
			f, e = docFile(r, tx, ws, user(r), r.Method == "POST")
			if e != nil {
				return e
			}
			d, e := tx.Q.GetCoresearchDoc(r.Context(), db.GetCoresearchDocParams{WorkspaceID: ws, FileID: f.Record.ID})
			if e != nil {
				return e
			}
			if r.Method == "POST" {
				current, e := doc.AtVersion(r.Context(), tx, f, d.Version)
				if e != nil {
					return e
				}
				length = doc.Length(current.Text)
			}
			members, e = tx.Q.CoresearchPresenceMembers(r.Context(), db.CoresearchPresenceMembersParams{WorkspaceID: ws, ProjectID: f.Record.ProjectID})
			return e
		})
	}
	if e = authorize(); e != nil {
		docAnswer(w, r, e)
		return
	}
	var presence coresearch.Presence
	if h.Hub != nil {
		presence.Redis = h.Hub.Redis
	}
	if r.Method == "POST" {
		var in struct{ Anchor, Head *int }
		if e = readJSON(r, &in); e != nil || in.Anchor == nil || in.Head == nil || *in.Anchor < 0 || *in.Head < 0 || *in.Anchor > length || *in.Head > length {
			docAnswer(w, r, doc.ErrInvalid)
			return
		}
		name := ""
		for _, m := range members {
			if m.UserID == user(r) {
				name = m.Name
			}
		}
		e = presence.Put(r.Context(), ws, f.Record.ID, coresearch.Cursor{UserID: user(r), Name: name, Anchor: *in.Anchor, Head: *in.Head})
		if e == nil {
			e = authorize()
		}
		if docAnswer(w, r, e) {
			w.WriteHeader(204)
		}
		return
	}
	out, e := presence.List(r.Context(), f.Record.ID, members)
	if e == nil {
		e = authorize()
	}
	if e == nil {
		allowed := map[uuid.UUID]bool{}
		for _, m := range members {
			allowed[m.UserID] = true
		}
		filtered := out[:0]
		for _, p := range out {
			if allowed[p.UserID] {
				filtered = append(filtered, p)
			}
		}
		out = filtered
	}
	if docAnswer(w, r, e) {
		writeJSON(w, 200, out)
	}
}
