package doc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type Doc struct {
	Version int64  `json:"version"`
	Text    string `json:"text"`
}

// File carries an immutable blob hydrated before entering a transaction. InitialHead
// is required only for the first Open; already-open documents use their journal.
type File struct {
	Record      db.CoresearchFile
	InitialText string
	InitialHead int32
}
type Author struct{ UserID, AgentRunID *uuid.UUID }
type Step struct {
	Version  int64           `json:"version"`
	Changes  json.RawMessage `json:"changes"`
	ClientID string          `json:"client_id"`
}
type ErrBehind struct{ Current int64 }

func (e ErrBehind) Error() string { return fmt.Sprintf("document is at version %d", e.Current) }

var ErrHeadChanged = errors.New("document source changed; hydrate again")

func Eligible(f db.CoresearchFile) bool {
	ext := strings.ToLower(path.Ext(f.Path))
	return ext == ".tex" || ext == ".bib"
}
func lock(ctx context.Context, tx *store.Tx, f File) error {
	if f.Record.WorkspaceID != tx.WS || !Eligible(f.Record) {
		return pgx.ErrNoRows
	}
	p, e := tx.Q.LockCoresearchProject(ctx, db.LockCoresearchProjectParams{WorkspaceID: tx.WS, ID: f.Record.ProjectID})
	if e != nil {
		return e
	}
	if p.Mode != "cowrite" {
		return pgx.ErrNoRows
	}
	_, e = tx.Q.LockCoresearchFile(ctx, db.LockCoresearchFileParams{WorkspaceID: tx.WS, ProjectID: f.Record.ProjectID, Path: f.Record.Path})
	return e
}
func Open(ctx context.Context, tx *store.Tx, f File) (Doc, error) {
	if e := lock(ctx, tx, f); e != nil {
		return Doc{}, e
	}
	d, e := tx.Q.LockCoresearchDoc(ctx, db.LockCoresearchDocParams{WorkspaceID: tx.WS, FileID: f.Record.ID})
	if errors.Is(e, pgx.ErrNoRows) {
		if f.InitialHead <= 0 {
			return Doc{}, ErrHeadChanged
		}
		head, e := tx.Q.CoresearchFileHead(ctx, db.CoresearchFileHeadParams{WorkspaceID: tx.WS, FileID: f.Record.ID})
		if e != nil {
			return Doc{}, e
		}
		if head.N != f.InitialHead {
			return Doc{}, ErrHeadChanged
		}
		if !utf8.ValidString(f.InitialText) || len(f.InitialText) > MaxTextBytes || strings.ContainsRune(f.InitialText, 0) {
			return Doc{}, ErrInvalid
		}
		if e = tx.Q.InsertCoresearchDoc(ctx, db.InsertCoresearchDocParams{WorkspaceID: tx.WS, FileID: f.Record.ID, InitialText: f.InitialText}); e != nil {
			return Doc{}, e
		}
		return Doc{Text: f.InitialText}, nil
	}
	if e != nil {
		return Doc{}, e
	}
	return reconstruct(ctx, tx, f, d.CheckpointText, d.CheckpointVersion, d.Version)
}
func reconstruct(ctx context.Context, tx *store.Tx, f File, text string, after, through int64) (Doc, error) {
	for after < through {
		steps, e := Steps(ctx, tx, f, after, through)
		if e != nil {
			return Doc{}, e
		}
		if len(steps) == 0 {
			return Doc{}, errors.New("missing document steps")
		}
		for _, s := range steps {
			if s.Version != after+1 {
				return Doc{}, errors.New("noncontiguous document history")
			}
			c, e := Parse(s.Changes)
			if e != nil {
				return Doc{}, e
			}
			text, e = c.Apply(text)
			if e != nil {
				return Doc{}, e
			}
			after = s.Version
		}
	}
	return Doc{Version: through, Text: text}, nil
}
func AtVersion(ctx context.Context, tx *store.Tx, f File, version int64) (Doc, error) {
	d, e := tx.Q.GetCoresearchDoc(ctx, db.GetCoresearchDocParams{WorkspaceID: tx.WS, FileID: f.Record.ID})
	if e != nil {
		return Doc{}, e
	}
	if version < 0 || version > d.Version {
		return Doc{}, ErrInvalid
	}
	if version >= d.CheckpointVersion {
		return reconstruct(ctx, tx, f, d.CheckpointText, d.CheckpointVersion, version)
	}
	return reconstruct(ctx, tx, f, d.InitialText, 0, version)
}
func Steps(ctx context.Context, tx *store.Tx, f File, after, through int64) ([]Step, error) {
	if after < 0 || through < after {
		return nil, ErrInvalid
	}
	rows, e := tx.Q.ListCoresearchDocSteps(ctx, db.ListCoresearchDocStepsParams{WorkspaceID: tx.WS, FileID: f.Record.ID, AfterVersion: after, ThroughVersion: through})
	if e != nil {
		return nil, e
	}
	out := make([]Step, 0, len(rows))
	for _, r := range rows {
		out = append(out, Step{r.Version, r.Changes, r.ClientID})
	}
	return out, nil
}
func Push(ctx context.Context, tx *store.Tx, f File, base int64, steps []Step, author Author) (int64, error) {
	d, e := Open(ctx, tx, f)
	if e != nil {
		return 0, e
	}
	if base != d.Version {
		return 0, ErrBehind{d.Version}
	}
	if len(steps) == 0 || len(steps) > 500 || (author.UserID == nil) == (author.AgentRunID == nil) {
		return 0, ErrInvalid
	}
	parsed := make([]ChangeSet, len(steps))
	texts := make([]string, len(steps))
	text := d.Text
	for i, s := range steps {
		if len(s.ClientID) == 0 || len(s.ClientID) > 128 || !utf8.ValidString(s.ClientID) || strings.ContainsRune(s.ClientID, 0) {
			return 0, ErrInvalid
		}
		parsed[i], e = Parse(s.Changes)
		if e != nil {
			return 0, e
		}
		text, e = parsed[i].Apply(text)
		if e != nil {
			return 0, e
		}
		texts[i] = text
	}
	e = tx.Savepoint(ctx, func(inner *store.Tx) error {
		for i, c := range parsed {
			v := base + int64(i) + 1
			s := steps[i]
			if e := inner.Q.InsertCoresearchDocStep(ctx, db.InsertCoresearchDocStepParams{WorkspaceID: tx.WS, FileID: f.Record.ID, Version: v, AuthorUserID: author.UserID, AgentRunID: author.AgentRunID, ClientID: s.ClientID, Changes: s.Changes}); e != nil {
				return e
			}
			if e := mapRanges(ctx, inner, f, c); e != nil {
				return e
			}
			if e := inner.Q.AdvanceCoresearchDoc(ctx, db.AdvanceCoresearchDocParams{WorkspaceID: tx.WS, FileID: f.Record.ID, Version: v}); e != nil {
				return e
			}
			if v%200 == 0 {
				if e := Fold(ctx, inner, f, Doc{v, texts[i]}); e != nil {
					return e
				}
			}
		}
		return nil
	})
	if e != nil {
		return 0, e
	}
	version := base + int64(len(steps))
	actor := store.System
	if author.UserID != nil {
		actor = store.User(*author.UserID)
	} else {
		actor = store.Actor{Type: "role", ID: *author.AgentRunID}
	}
	if e = tx.Emit(ctx, "coresearch.doc.stepped", nil, actor, map[string]any{"project_id": f.Record.ProjectID, "file_id": f.Record.ID, "version": version}); e != nil {
		return 0, e
	}
	return version, nil
}
func mapRanges(ctx context.Context, tx *store.Tx, f File, c ChangeSet) error {
	changes, e := tx.Q.ListCoresearchPendingRanges(ctx, db.ListCoresearchPendingRangesParams{WorkspaceID: tx.WS, FileID: f.Record.ID})
	if e != nil {
		return e
	}
	for _, r := range changes {
		status := r.Status
		if c.Touches(int(r.FromPos), int(r.ToPos)) {
			status = "stale"
		}
		from, to := c.MapPos(int(r.FromPos), 1), c.MapPos(int(r.ToPos), -1)
		if to < from {
			to = from
		}
		if e = tx.Q.MapCoresearchChange(ctx, db.MapCoresearchChangeParams{WorkspaceID: tx.WS, ID: r.ID, FromPos: int32(from), ToPos: int32(to), Status: status}); e != nil {
			return e
		}
	}
	comments, e := tx.Q.ListCoresearchCommentRanges(ctx, db.ListCoresearchCommentRangesParams{WorkspaceID: tx.WS, FileID: f.Record.ID})
	if e != nil {
		return e
	}
	for _, r := range comments {
		from, to := c.MapPos(int(r.FromPos), 1), c.MapPos(int(r.ToPos), -1)
		if to < from {
			to = from
		}
		if e = tx.Q.MapCoresearchComment(ctx, db.MapCoresearchCommentParams{WorkspaceID: tx.WS, ID: r.ID, FromPos: int32(from), ToPos: int32(to)}); e != nil {
			return e
		}
	}
	return nil
}

// Fold advances only the fast checkpoint. The initial text and journal remain intact.
func Fold(ctx context.Context, tx *store.Tx, f File, d Doc) error {
	return tx.Q.CheckpointCoresearchDoc(ctx, db.CheckpointCoresearchDocParams{WorkspaceID: tx.WS, FileID: f.Record.ID, CheckpointText: d.Text, CheckpointVersion: d.Version})
}
