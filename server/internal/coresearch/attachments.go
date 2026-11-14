package coresearch

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type ErrAttachmentOverText struct{ Path string }

func (e ErrAttachmentOverText) Error() string {
	return fmt.Sprintf("attachment would overwrite text file %s", e.Path)
}

var ErrAttachmentHeadChanged = errors.New("attachment destination changed; retry the send")

func AttachPath(mode, name string) (string, error) {
	clean, err := CleanPath(name)
	if err != nil {
		return "", err
	}
	prefix := "attachments/"
	if mode == "cowrite" {
		prefix = "figures/"
	} else if mode != "experiment" {
		return "", errors.New("invalid session mode")
	}
	return CleanPath(prefix + clean)
}

// SaveAttachment checks a SHA classification read before the transaction. A head
// absent from that snapshot fails closed so concurrent text edits are protected.
func SaveAttachment(ctx context.Context, tx *store.Tx, in SaveIn, textHeads map[string]bool) (db.CoresearchFileVersion, error) {
	var zero db.CoresearchFileVersion
	if _, err := tx.Q.LockCoresearchProject(ctx, db.LockCoresearchProjectParams{WorkspaceID: in.Workspace, ID: in.Project}); err != nil {
		return zero, err
	}
	f, err := tx.Q.LockCoresearchFile(ctx, db.LockCoresearchFileParams{WorkspaceID: in.Workspace, ProjectID: in.Project, Path: in.Path})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return zero, err
	}
	if err == nil {
		head, e := tx.Q.CoresearchFileHead(ctx, db.CoresearchFileHeadParams{WorkspaceID: in.Workspace, FileID: f.ID})
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return zero, e
		}
		if e == nil {
			isText, known := textHeads[head.Sha256]
			if !known {
				return zero, fmt.Errorf("%w: %s", ErrAttachmentHeadChanged, in.Path)
			}
			if isText {
				return zero, ErrAttachmentOverText{in.Path}
			}
			in.Base = int(head.N)
		}
	}
	return SaveVersion(ctx, tx, in)
}
