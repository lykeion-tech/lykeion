package coresearch

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var MaxProjectBytes int64 = 2 << 30

const MaxFileBytes int64 = 50 << 20
const MaxTextBytes int64 = 1 << 20

var ErrLiveDocument = errors.New("live document")

var ErrArchived = errors.New("project is archived")
var ErrQuota = errors.New("project files exceed the storage limit")
var shaPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type ErrStale struct{ Base, Current int }

func (e ErrStale) Error() string {
	return fmt.Sprintf("version %d is not the latest (%d is); save with force to override", e.Base, e.Current)
}

type SaveIn struct {
	Workspace, Project    uuid.UUID
	Path, Sha256          string
	Size                  int64
	Base                  int
	Force                 bool
	Author, AgentRun, Run *uuid.UUID
	RestoredFrom          *int32
}

// SaveVersion records a blob already stored by WithBlob. Callers must acquire
// the workspace mutation lock before their authorization locks and this call.
// The project lock serializes quota checks for saves to different files and
// project archiving.
func SaveVersion(ctx context.Context, tx *store.Tx, in SaveIn) (db.CoresearchFileVersion, error) {
	var zero db.CoresearchFileVersion
	project, err := tx.Q.LockCoresearchProject(ctx, db.LockCoresearchProjectParams{WorkspaceID: in.Workspace, ID: in.Project})
	if err != nil {
		return zero, err
	}
	if project.ArchivedAt != nil {
		return zero, ErrArchived
	}
	clean, err := CleanPath(in.Path)
	if err != nil {
		return zero, err
	}
	if in.Base < 0 || in.Size < 0 || in.Size > MaxFileBytes || !shaPattern.MatchString(in.Sha256) {
		return zero, errors.New("invalid file version")
	}
	if err = tx.Q.EnsureCoresearchFile(ctx, db.EnsureCoresearchFileParams{WorkspaceID: in.Workspace, ProjectID: in.Project, Path: clean}); err != nil {
		return zero, err
	}
	file, err := tx.Q.LockCoresearchFile(ctx, db.LockCoresearchFileParams{WorkspaceID: in.Workspace, ProjectID: in.Project, Path: clean})
	if err != nil {
		return zero, err
	}
	if _, err := tx.Q.GetCoresearchDoc(ctx, db.GetCoresearchDocParams{WorkspaceID: in.Workspace, FileID: file.ID}); err == nil {
		return zero, ErrLiveDocument
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return zero, err
	}
	head, err := tx.Q.CoresearchFileHead(ctx, db.CoresearchFileHeadParams{WorkspaceID: in.Workspace, FileID: file.ID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return zero, err
	}
	current := head.N
	if in.Base != int(current) && !in.Force {
		return zero, ErrStale{in.Base, int(current)}
	}
	total, err := tx.Q.CoresearchProjectBytes(ctx, db.CoresearchProjectBytesParams{WorkspaceID: in.Workspace, ProjectID: in.Project, Sha256: in.Sha256})
	if err != nil {
		return zero, err
	}
	if in.Size > MaxProjectBytes-total {
		return zero, ErrQuota
	}
	var overrode *int32
	if in.Force && current > 0 {
		overrode = &current
	}
	row, err := tx.Q.InsertCoresearchVersion(ctx, db.InsertCoresearchVersionParams{WorkspaceID: in.Workspace, FileID: file.ID, N: current + 1, Sha256: in.Sha256, Size: in.Size, BlobKey: BlobKey(in.Workspace, in.Sha256), AuthorUserID: in.Author, AgentRunID: in.AgentRun, RunID: in.Run, Overrode: overrode, RestoredFrom: in.RestoredFrom})
	if err != nil {
		return zero, err
	}
	if err = tx.Q.TouchCoresearchProject(ctx, db.TouchCoresearchProjectParams{WorkspaceID: in.Workspace, ID: in.Project}); err != nil {
		return zero, err
	}
	actor := store.System
	if in.Author != nil {
		actor = store.User(*in.Author)
	} else if in.AgentRun != nil {
		actor = store.Actor{Type: "role", ID: *in.AgentRun}
	}
	if err = tx.Emit(ctx, "coresearch.file.saved", nil, actor, map[string]any{"project_id": in.Project, "file_id": file.ID, "n": row.N}); err != nil {
		return zero, err
	}
	return row, nil
}
