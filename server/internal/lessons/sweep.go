package lessons

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/store"
)

// Sweep rechecks a bounded page, even without a corresponding event. Version
// uses the same shared projection as the immediate lesson read guard.
func (x *Triggers) Sweep(ctx context.Context) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	ws, after := x.sweepWorkspace, x.sweepLesson
	err := x.Store.Maintenance(ctx, func(tx *store.Tx) error {
		if after == uuid.Nil {
			var err error
			ws, err = tx.NextLessonWorkspace(ctx, ws)
			if err != nil || ws == uuid.Nil {
				return err
			}
		}
		if err := tx.Workspace(ctx, ws); err != nil {
			return err
		}
		page, err := tx.ActiveLessonPage(ctx, after)
		if err != nil {
			return err
		}
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
			}
			after = l.ID
		}
		if len(page) < 16 {
			after = uuid.Nil
		}
		return nil
	})
	if err == nil {
		x.sweepWorkspace = ws
		x.sweepLesson = after
	}
	return err
}
