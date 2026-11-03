package labmemory

import (
	"context"
	"errors"
	"strconv"

	"github.com/google/uuid"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

var errPage = errors.New("invalid links page; use the returned next_after and a limit from 1 to 20")

const maxLinks = 20

func linkPage(ctx context.Context, tx *store.Tx, asker uuid.UUID, ref Ref, in refInput) ([]Ref, string, error) {
	limit := in.Limit
	if limit == 0 {
		limit = maxLinks
	}
	if limit < 1 || limit > maxLinks || ref.Kind != "workspace" && in.Directory != "" {
		return nil, "", errPage
	}
	if ref.Kind == "study" {
		return studyLinks(ctx, tx, ref, in.After, limit)
	}
	f, err := Read(ctx, tx, asker, ref)
	if err != nil {
		return nil, "", err
	}
	if ref.Kind == "workspace" {
		return directoryLinks(ctx, tx, in, limit)
	}
	offset, err := linkOffset(in.After)
	if err != nil {
		return nil, "", err
	}
	if offset > len(f.Links) {
		return nil, "", errPage
	}
	return slicePage(f.Links[offset:], limit, offset)
}

func studyLinks(ctx context.Context, tx *store.Tx, ref Ref, after string, limit int) ([]Ref, string, error) {
	// Membership was locked under the asker. The study visibility rule is the
	// same card membership rule used by Read, without loading its entire tree.
	visible, err := tx.Q.LabStudyOpen(ctx, db.LabStudyOpenParams{WorkspaceID: tx.WS, CardID: ref.ID})
	if err != nil {
		return nil, "", err
	}
	if !visible {
		return nil, "", ErrNotFound
	}
	cursor := Ref{}
	if after != "" {
		cursor, err = ParseRef(after)
		if err != nil {
			return nil, "", errPage
		}
	}
	rows, err := tx.Q.LabStudyLinks(ctx, db.LabStudyLinksParams{WorkspaceID: tx.WS, CardID: ref.ID, AfterID: cursor.ID, AfterKind: cursor.Kind, PageSize: int32(limit + 1)})
	if err != nil {
		return nil, "", err
	}
	refs := make([]Ref, 0, len(rows))
	for _, row := range rows {
		refs = append(refs, Ref{row.Kind, row.ID})
	}
	if len(refs) > limit {
		return refs[:limit], refs[limit-1].String(), nil
	}
	return refs, "", nil
}

func directoryLinks(ctx context.Context, tx *store.Tx, in refInput, limit int) ([]Ref, string, error) {
	switch in.Directory {
	case "", "studies":
		after := uuid.Nil
		var err error
		if in.After != "" {
			after, err = uuid.Parse(in.After)
			if err != nil {
				return nil, "", errPage
			}
		}
		ids, err := tx.Q.LabStudyDirectory(ctx, db.LabStudyDirectoryParams{WorkspaceID: tx.WS, AfterID: after, PageSize: int32(limit + 1)})
		if err != nil {
			return nil, "", err
		}
		refs := kindRefs("study", ids)
		if len(refs) > limit {
			return refs[:limit], ids[limit-1].String(), nil
		}
		return refs, "", nil
	case "lessons":
		// Sequence cursors reveal no reference for a stale candidate and
		// permit a bounded index seek even at the end of a large directory.
		var seq int64
		if in.After != "" {
			var err error
			seq, err = strconv.ParseInt(in.After, 10, 64)
			if err != nil || seq < 0 {
				return nil, "", errPage
			}
		}
		rows, err := tx.Q.LabLessonDirectory(ctx, db.LabLessonDirectoryParams{WorkspaceID: tx.WS, AfterSeq: seq, PageSize: int32(limit + 1)})
		if err != nil {
			return nil, "", err
		}
		refs := make([]Ref, 0, len(rows))
		for _, row := range rows {
			refs = append(refs, Ref{"lesson", row.ID})
		}
		if len(refs) > limit {
			return refs[:limit], strconv.FormatInt(rows[limit-1].DirectorySeq, 10), nil
		}
		return refs, "", nil
	default:
		return nil, "", errPage
	}
}

func kindRefs(kind string, ids []uuid.UUID) []Ref {
	refs := make([]Ref, 0, len(ids))
	for _, id := range ids {
		refs = append(refs, Ref{kind, id})
	}
	return refs
}
func linkOffset(after string) (int, error) {
	if after == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(after, 10, 31)
	if err != nil {
		return 0, errPage
	}
	return int(n), nil
}
func slicePage(refs []Ref, limit, offset int) ([]Ref, string, error) {
	if len(refs) > limit {
		return refs[:limit], strconv.Itoa(offset + limit), nil
	}
	return refs, "", nil
}
