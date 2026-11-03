package lessons

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/store"
)

// Sweep rechecks a bounded page, even without a corresponding event. Version
// uses the same shared projection as the immediate lesson read guard.
func (x *Triggers) Sweep(ctx context.Context) error {
	x.sweepMu.Lock()
	defer x.sweepMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var ws uuid.UUID
	err := x.Store.Maintenance(ctx, func(tx *store.Tx) error {
		var err error
		ws, err = tx.NextLessonWorkspace(ctx, x.sweepWorkspace)
		if err != nil || ws == uuid.Nil {
			return err
		}
		if err := tx.Workspace(ctx, ws); err != nil {
			return err
		}
		page, err := tx.ActiveLessonPage(ctx)
		if err != nil {
			return err
		}
		fresh := make([]uuid.UUID, 0, len(page))
		for _, l := range page {
			var cites []labmemory.Citation
			stale := json.Unmarshal(l.Cites, &cites) != nil || len(cites) == 0
			for _, cite := range cites {
				r, err := labmemory.ParseRef(cite.Ref)
				if err != nil || r.Kind == "lesson" || cite.Version == "" {
					stale = true
					break
				}
				version, err := labmemory.Version(ctx, tx, r)
				if err != nil && !errors.Is(err, labmemory.ErrNotFound) {
					return err
				}
				if err != nil || version != cite.Version {
					stale = true
					break
				}
			}
			if stale {
				if err := tx.SetAsideChangedLesson(ctx, l.ID); err != nil {
					return err
				}
			} else {
				fresh = append(fresh, l.ID)
			}
		}
		return tx.MarkLessonsSwept(ctx, fresh)
	})
	// Only schedule by workspace. Failed checks roll back, so that page
	// remains the oldest in its workspace and is retried on the next visit.
	if ws != uuid.Nil {
		x.sweepWorkspace = ws
	}
	return err
}
