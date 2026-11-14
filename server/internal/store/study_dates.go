package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// StudyStartDate is a planning date; execution starts independently of it.
func (t *Tx) StudyStartDate(ctx context.Context, id uuid.UUID) (*time.Time, error) {
	var date *time.Time
	err := t.raw.QueryRow(ctx, `SELECT start_date FROM card WHERE workspace_id=$1 AND id=$2`, t.WS, id).Scan(&date)
	return date, err
}

func (t *Tx) SetStudyStartDate(ctx context.Context, id uuid.UUID, date *time.Time) error {
	_, err := t.raw.Exec(ctx, `UPDATE card SET start_date=$3 WHERE workspace_id=$1 AND id=$2`, t.WS, id, date)
	return err
}

func (t *Tx) SetStudyPriority(ctx context.Context, id uuid.UUID, priority string) error {
	_, err := t.raw.Exec(ctx, `UPDATE card SET priority=$3 WHERE workspace_id=$1 AND id=$2`, t.WS, id, priority)
	return err
}

func (t *Tx) StudyStartDates(ctx context.Context) (map[uuid.UUID]time.Time, error) {
	rows, err := t.raw.Query(ctx, `SELECT id, start_date FROM card WHERE workspace_id=$1 AND start_date IS NOT NULL`, t.WS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	dates := make(map[uuid.UUID]time.Time)
	for rows.Next() {
		var id uuid.UUID
		var date time.Time
		if err := rows.Scan(&id, &date); err != nil {
			return nil, err
		}
		dates[id] = date
	}
	return dates, rows.Err()
}
