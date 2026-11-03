package store

import (
	"context"
	"errors"
	"github.com/google/uuid"
)

// LabFactRefs enumerates only supported shared fact identities in the explicitly
// selected workspace. Source text is read separately through labmemory.
func (t *Tx) LabFactRefs(ctx context.Context) ([]string, error) {
	if !t.IsMaintenance() || t.WS == uuid.Nil {
		return nil, errors.New("workspace required")
	}
	rows, err := t.raw.Query(ctx, `SELECT ref FROM (
 SELECT 'workspace:context' AS ref
 UNION ALL SELECT 'study:'||id FROM card WHERE workspace_id=$1
 UNION ALL SELECT 'record:'||id FROM record WHERE workspace_id=$1
 UNION ALL SELECT 'comment:'||id FROM comment WHERE workspace_id=$1
 UNION ALL SELECT 'decision:'||id FROM inbox_item WHERE workspace_id=$1 AND (jsonb_array_length(options)>0 OR question_set IS NOT NULL)
 UNION ALL SELECT 'lesson:'||id FROM lesson WHERE workspace_id=$1 AND status='active'
 ) f ORDER BY ref`, t.WS)
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
