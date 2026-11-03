// Package lessons maintains the workspace-shared lesson writing queue.
package lessons

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"lykeion/server/internal/labindex"
	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/store"
)

type Triggers struct {
	Store *store.Store
	Now   func() time.Time

	mu                                         sync.Mutex
	lastWorkspace, sweepWorkspace, sweepLesson uuid.UUID
}

func (x *Triggers) Tick(ctx context.Context) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	now := time.Now()
	if x.Now != nil {
		now = x.Now()
	}
	var ws uuid.UUID
	err := x.Store.Maintenance(ctx, func(tx *store.Tx) error {
		work, err := tx.PendingLessonWork(ctx, x.lastWorkspace)
		if err != nil || len(work) == 0 {
			return err
		}
		ws = work[0].Workspace
		if err := tx.Workspace(ctx, ws); err != nil {
			return err
		}
		var events, dirty []int64
		for _, w := range work {
			// Dirty decisions invalidate citations; they are not new resolutions.
			// The separate sweep continually revisits every active lesson.
			if w.Source == "dirty" {
				dirty = append(dirty, w.ID)
				continue
			}
			events = append(events, w.ID)
			if w.Card == nil {
				continue
			}
			switch w.Type {
			case "card.steered":
				old, was, col, state, ok, err := tx.LessonSteer(ctx, w.ID, *w.Card)
				if err != nil {
					return err
				}
				if !ok {
					continue
				}
				// A reopening only invalidates previous evidence, including when it changes column.
				if was == "done" && state != "done" {
					continue
				}
				if old != "" && old != col {
					if err := tx.QueueLessonStudy(ctx, *w.Card, nil, "moved:"+old, now.Add(15*time.Minute)); err != nil {
						return err
					}
				}
				if state == "done" && was != "done" {
					if err := tx.QueueLessonStudy(ctx, *w.Card, nil, "finished", now.Add(15*time.Minute)); err != nil {
						return err
					}
				}
			case "inbox.resolved":
				id, err := tx.LessonDecision(ctx, w.ID, *w.Card)
				if err != nil {
					return err
				}
				if id != nil {
					if err := tx.QueueLessonStudy(ctx, *w.Card, id, "decision:"+id.String(), now.Add(15*time.Minute)); err != nil {
						return err
					}
				}
			}
		}
		return tx.AckLessonWork(ctx, events, dirty)
	})
	// Scheduling is independent of acknowledgement: a failing tenant must not
	// prevent another tenant from making progress. Its exact deliveries retry.
	if ws != uuid.Nil {
		x.lastWorkspace = ws
	}
	return err
}

func SimilarStudies(ctx context.Context, tx *store.Tx, idx *labindex.Index, card uuid.UUID, k int) ([]uuid.UUID, error) {
	if idx == nil || k <= 0 {
		return nil, nil
	}
	if tx == nil || tx.WS == uuid.Nil || !tx.IsMaintenance() {
		return nil, labmemory.ErrNotFound
	}
	query, err := tx.LessonQuestion(ctx, card)
	if err != nil {
		return nil, err
	}
	k = min(k, 5)
	var result []uuid.UUID
	seen := map[uuid.UUID]bool{}
	err = idx.VisitStudyCandidates(ctx, tx.WS, query, func(refs []labmemory.Ref) (bool, error) {
		var ids []uuid.UUID
		for _, ref := range refs {
			if ref.Kind == "study" && ref.ID != card && !seen[ref.ID] {
				ids = append(ids, ref.ID)
			}
		}
		if len(ids) == 0 {
			return false, nil
		}
		finished, err := tx.FinishedLessonStudies(ctx, card, ids)
		if err != nil {
			return false, err
		}
		for _, id := range finished {
			if !seen[id] && len(result) < k {
				result = append(result, id)
				seen[id] = true
			}
		}
		return len(result) == k, nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
