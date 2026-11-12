package store

import (
	"context"
	"time"
)

// CoresearchClock returns the database wall clock after the caller's locks.
func (t *Tx) CoresearchClock(ctx context.Context) (time.Time, error) {
	var now time.Time
	err := t.raw.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now)
	return now, err
}
