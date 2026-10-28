package doc

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// PreparedSnapshot is staged with coresearch.WithBlobs before Snapshot is called.
// Version binds the staged bytes to the live document observed by the caller.
type PreparedSnapshot struct {
	Version         int64
	Text, SHA256    string
	MaxProjectBytes int64
}

func PrepareSnapshot(d Doc) PreparedSnapshot {
	return PreparedSnapshot{Version: d.Version, Text: d.Text, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(d.Text))), MaxProjectBytes: 2 << 30}
}

// Snapshot records an already-staged content-addressed blob. It never accesses
// storage. Callers hold the workspace mutation lock before authorization locks.
func Snapshot(ctx context.Context, tx *store.Tx, f File, p PreparedSnapshot, author Author) (int, error) {
	if (author.UserID == nil) == (author.AgentRunID == nil) || !utf8.ValidString(p.Text) || strings.ContainsRune(p.Text, 0) || len(p.Text) > MaxTextBytes || p.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(p.Text))) {
		return 0, ErrInvalid
	}
	d, e := Open(ctx, tx, f)
	if e != nil {
		return 0, e
	}
	if d.Version != p.Version {
		return 0, ErrBehind{d.Version}
	}
	if d.Text != p.Text {
		return 0, ErrInvalid
	}
	project, e := tx.Q.GetCoresearchProject(ctx, db.GetCoresearchProjectParams{WorkspaceID: tx.WS, ID: f.Record.ProjectID})
	if e != nil {
		return 0, e
	}
	if project.ArchivedAt != nil {
		return 0, errors.New("project is archived")
	}
	head, e := tx.Q.CoresearchFileHead(ctx, db.CoresearchFileHeadParams{WorkspaceID: tx.WS, FileID: f.Record.ID})
	if e != nil {
		return 0, e
	}
	total, e := tx.Q.CoresearchProjectBytes(ctx, db.CoresearchProjectBytesParams{WorkspaceID: tx.WS, ProjectID: f.Record.ProjectID, Sha256: p.SHA256})
	if e != nil {
		return 0, e
	}
	limit := p.MaxProjectBytes
	if limit <= 0 {
		limit = 2 << 30
	}
	if int64(len(p.Text)) > limit-total {
		return 0, errors.New("project files exceed the storage limit")
	}
	row, e := tx.Q.InsertCoresearchVersion(ctx, db.InsertCoresearchVersionParams{WorkspaceID: tx.WS, FileID: f.Record.ID, N: head.N + 1, Sha256: p.SHA256, Size: int64(len(p.Text)), BlobKey: tx.WS.String() + "/coresearch/" + p.SHA256, AuthorUserID: author.UserID, AgentRunID: author.AgentRunID})
	if e != nil {
		return 0, e
	}
	if e = tx.Q.TouchCoresearchProject(ctx, db.TouchCoresearchProjectParams{WorkspaceID: tx.WS, ID: f.Record.ProjectID}); e != nil {
		return 0, e
	}
	actor := store.System
	if author.UserID != nil {
		actor = store.User(*author.UserID)
	} else {
		actor = store.Actor{Type: "role", ID: *author.AgentRunID}
	}
	if e = tx.Emit(ctx, "coresearch.file.saved", nil, actor, map[string]any{"project_id": f.Record.ProjectID, "file_id": f.Record.ID, "n": row.N}); e != nil {
		return 0, e
	}
	return int(row.N), nil
}
