package store

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// LabFactRefs enumerates only supported shared fact identities in the explicitly
// selected workspace. Source text is read separately through labmemory. Each
// kind is independently keyset-paged before merging at most six bounded pages.
func (t *Tx) LabFactRefs(ctx context.Context, after string, limit int) ([]string, error) {
	if limit <= 0 || limit > 33 {
		return nil, errors.New("invalid fact page size")
	}
	if !t.IsMaintenance() || t.WS == uuid.Nil {
		return nil, errors.New("workspace required")
	}
	afterKind, afterID := "", uuid.Nil
	if after != "" {
		kind, id, ok := strings.Cut(after, ":")
		if !ok {
			return nil, errors.New("invalid fact cursor")
		}
		afterKind = kind
		if kind != "workspace" {
			var err error
			afterID, err = uuid.Parse(id)
			if err != nil {
				return nil, err
			}
		}
	}
	rows, err := t.raw.Query(ctx, `SELECT ref FROM (
 SELECT 'workspace:context' AS ref WHERE 'workspace:context'>$2
 UNION ALL SELECT ref FROM (SELECT 'study:'||id AS ref FROM card WHERE workspace_id=$1 AND ('study'>$3 OR ('study'=$3 AND id>$4)) ORDER BY id LIMIT $5) s
 UNION ALL SELECT ref FROM (SELECT 'record:'||id AS ref FROM record WHERE workspace_id=$1 AND ('record'>$3 OR ('record'=$3 AND id>$4)) ORDER BY id LIMIT $5) r
 UNION ALL SELECT ref FROM (SELECT 'comment:'||id AS ref FROM comment WHERE workspace_id=$1 AND ('comment'>$3 OR ('comment'=$3 AND id>$4)) ORDER BY id LIMIT $5) c
 UNION ALL SELECT ref FROM (SELECT 'decision:'||id AS ref FROM inbox_item WHERE workspace_id=$1 AND (jsonb_array_length(options)>0 OR question_set IS NOT NULL) AND ('decision'>$3 OR ('decision'=$3 AND id>$4)) ORDER BY id LIMIT $5) d
 UNION ALL SELECT ref FROM (SELECT 'lesson:'||id AS ref FROM lesson WHERE workspace_id=$1 AND status='active' AND checked_at IS NOT NULL AND ('lesson'>$3 OR ('lesson'=$3 AND id>$4)) ORDER BY id LIMIT $5) l
 ) f ORDER BY ref LIMIT $5`, t.WS, after, afterKind, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}
