package doc

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
	"strings"
)

type Hunk struct {
	Find    string `json:"find"`
	Replace string `json:"replace"`
}
type HunkResult struct {
	Index    int        `json:"index"`
	Applied  bool       `json:"applied"`
	ChangeID *uuid.UUID `json:"change_id,omitempty"`
	Current  string     `json:"current"`
}

var ErrEmptyFind = errors.New("find must not be empty")
var ErrDecided = errors.New("change is already decided or cannot be kept while stale")

func ApplyHunks(ctx context.Context, tx *store.Tx, f File, base int64, hunks []Hunk, run, proposal uuid.UUID) ([]HunkResult, error) {
	if len(hunks) == 0 || len(hunks) > 500 {
		return nil, ErrInvalid
	}
	for _, h := range hunks {
		if h.Find == "" {
			return nil, ErrEmptyFind
		}
	}
	current, e := Open(ctx, tx, f)
	if e != nil {
		return nil, e
	}
	old, e := AtVersion(ctx, tx, f, base)
	if e != nil {
		return nil, e
	}
	out := make([]HunkResult, len(hunks))
	e = tx.Savepoint(ctx, func(tx *store.Tx) error {
		for i, h := range hunks {
			out[i].Index = i
			at := strings.Index(old.Text, h.Find)
			if at < 0 {
				continue
			}
			if strings.Index(old.Text[at+1:], h.Find) >= 0 {
				out[i].Current = "ambiguous"
				continue
			}
			from, to := Length(old.Text[:at]), Length(old.Text[:at+len(h.Find)])
			for after := base; after < current.Version; {
				steps, e := Steps(ctx, tx, f, after, current.Version)
				if e != nil {
					return e
				}
				if len(steps) == 0 {
					return errors.New("missing anchor history")
				}
				for _, s := range steps {
					c, e := Parse(s.Changes)
					if e != nil {
						return e
					}
					from, to = c.MapPos(from, 1), c.MapPos(to, -1)
					if to < from {
						to = from
					}
					after = s.Version
				}
			}
			selected, e := Slice(current.Text, from, to)
			if e != nil {
				return e
			}
			out[i].Current = selected
			if selected != h.Find {
				continue
			}
			row, e := Propose(ctx, tx, f, current, from, to, h.Replace, proposal, Author{AgentRunID: &run})
			if e != nil {
				return e
			}
			out[i].Applied = true
			out[i].ChangeID = &row.ID
			current, e = Open(ctx, tx, f)
			if e != nil {
				return e
			}
		}
		return nil
	})
	if e == nil {
		e = tx.Emit(ctx, "coresearch.proposal.created", nil, store.Actor{Type: "role", ID: run}, map[string]any{"project_id": f.Record.ProjectID, "file_id": f.Record.ID, "proposal_id": proposal})
	}
	return out, e
}

// Propose applies text immediately and retains the removed span for a later decision.
func Propose(ctx context.Context, tx *store.Tx, f File, d Doc, from, to int, text string, proposal uuid.UUID, author Author) (db.CoresearchChange, error) {
	removed, e := Slice(d.Text, from, to)
	if e != nil {
		return db.CoresearchChange{}, e
	}
	raw, e := json.Marshal(Replace(from, to, text, Length(d.Text)))
	if e != nil {
		return db.CoresearchChange{}, e
	}
	_, e = Push(ctx, tx, f, d.Version, []Step{{Changes: raw, ClientID: "proposal:" + proposal.String()}}, author)
	if e != nil {
		return db.CoresearchChange{}, e
	}
	return tx.Q.InsertCoresearchChange(ctx, db.InsertCoresearchChangeParams{WorkspaceID: tx.WS, ProposalID: proposal, FileID: f.Record.ID, FromPos: int32(from), ToPos: int32(from + Length(text)), RemovedText: removed})
}
func Decide(ctx context.Context, tx *store.Tx, f File, id, who uuid.UUID, keep bool) error {
	d, e := Open(ctx, tx, f)
	if e != nil {
		return e
	}
	row, e := tx.Q.GetCoresearchChange(ctx, db.GetCoresearchChangeParams{WorkspaceID: tx.WS, ID: id, ProjectID: f.Record.ProjectID})
	if e != nil {
		return e
	}
	if row.FileID != f.Record.ID {
		return pgx.ErrNoRows
	}
	// A stale change has someone's typing in or beside it, so it cannot be kept,
	// and it can be undone only while undoing deletes nothing: restoring over a
	// non-empty range would erase that typing.
	undoable := row.Status == "pending" || row.Status == "stale" && row.ToPos == row.FromPos
	if keep && row.Status != "pending" || !keep && !undoable || keep && row.FileAction == "remove" && d.Text != "" {
		return ErrDecided
	}
	status := "kept"
	if !keep {
		status = "undone"
		raw, e := json.Marshal(Replace(int(row.FromPos), int(row.ToPos), row.RemovedText, Length(d.Text)))
		if e != nil {
			return e
		}
		if _, e = Push(ctx, tx, f, d.Version, []Step{{Changes: raw, ClientID: "undo:" + id.String()}}, Author{UserID: &who}); e != nil {
			return e
		}
	}
	hide := row.FileAction == "remove" && keep
	if row.FileAction == "restore" && !keep {
		current, e := Open(ctx, tx, f)
		if e != nil {
			return e
		}
		hide = current.Text == ""
		if hide {
			pending, e := tx.Q.ListCoresearchPendingRanges(ctx, db.ListCoresearchPendingRangesParams{WorkspaceID: tx.WS, FileID: f.Record.ID})
			if e != nil {
				return e
			}
			for _, other := range pending {
				if other.ID != id {
					hide = false
					break
				}
			}
		}
	}
	if hide {
		if e = SetDeleted(ctx, tx, f, true); e != nil {
			return e
		}
	}
	if e = tx.Q.DecideCoresearchChange(ctx, db.DecideCoresearchChangeParams{WorkspaceID: tx.WS, ID: id, Status: status, DecidedBy: &who}); e != nil {
		return e
	}
	return tx.Emit(ctx, "coresearch.change.decided", nil, store.User(who), map[string]any{"project_id": f.Record.ProjectID, "file_id": f.Record.ID, "change_id": id})
}

// SetDeleted hides or revives a file without dropping its identity, journal, or immutable blobs.
// Callers hold the project lock and have checked current session membership.
func SetDeleted(ctx context.Context, tx *store.Tx, f File, deleted bool) error {
	if e := tx.Q.SetCoresearchFileDeleted(ctx, db.SetCoresearchFileDeletedParams{WorkspaceID: tx.WS, ProjectID: f.Record.ProjectID, ID: f.Record.ID, Deleted: deleted}); e != nil {
		return e
	}
	return tx.Emit(ctx, "coresearch.file.lifecycle", nil, store.System, map[string]any{"project_id": f.Record.ProjectID, "file_id": f.Record.ID})
}
