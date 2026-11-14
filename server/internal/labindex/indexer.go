package labindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/store"
)

const factBudget = 32

// Indexer consumes transactional delivery rows, never an event-ID high-water
// mark. A lower event ID committed later remains pending. One Tick processes at
// most one workspace source/pruning page and one orphan-key page. Restarting
// repeats backfill; pending delivery is durable until complete reconciliation.
type Indexer struct {
	Store          *store.Store
	Index          *Index
	mu             sync.Mutex
	epoch, model   string
	initialized    bool
	preferEvents   bool
	afterWorkspace uuid.UUID
	work           *indexWork
	orphanCursor   uint64
	orphanKeys     []string
}
type indexWork struct {
	WS         uuid.UUID
	Model      string
	Events     []int64 // exact observed IDs; later events are never added to this snapshot
	Dirty      []int64
	After      string
	SourceDone bool
	Scan       uint64
	Keys       []string
	ScanDone   bool
}
type indexEvent struct {
	id    int64
	ws    uuid.UUID
	typ   string
	card  *uuid.UUID
	dirty bool
}

func relevantEvent(typ string) bool {
	for _, prefix := range []string{"record.", "card.", "comment.", "inbox.", "decision.", "workspace.", "team.", "pack.", "attachment.", "study.", "stage_addon.", "agent_run.", "lesson."} {
		if strings.HasPrefix(typ, prefix) {
			return true
		}
	}
	return false
}
func relevantDelivery(e indexEvent) bool {
	// Coresearch and agent-only conversations have no study CardID. Their run
	// events cannot affect a shared study fact and must not trigger reconciliation.
	return relevantEvent(e.typ) && !(strings.HasPrefix(e.typ, "agent_run.") && e.card == nil)
}

func (x *Indexer) Tick(ctx context.Context) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.Store == nil || x.Index == nil {
		return errors.New("indexer dependencies required")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// No PostgreSQL connection is held while another is acquired or TEI runs.
	// A short Redis lease serializes replica ticks; the operation deadline is
	// shorter than the lease, and interrupted work remains unacknowledged.
	token := uuid.NewString()
	locked, err := x.Index.rdb.SetNX(ctx, "labindex:lock", token, 30*time.Second).Result()
	if err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer func() {
		c, done := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer done()
		x.Index.rdb.Eval(c, `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`, []string{"labindex:lock"}, token)
	}()
	if err := x.Index.Ensure(ctx); err != nil {
		return err
	}
	if err := x.Index.rdb.SetNX(ctx, "labindex:lastworkspace", uuid.Nil.String(), 0).Err(); err != nil {
		return err
	}
	if err := x.Index.rdb.SetNX(ctx, "labindex:epoch", uuid.NewString(), 0).Err(); err != nil {
		return err
	}
	epoch, err := x.Index.rdb.Get(ctx, "labindex:epoch").Result()
	if err != nil {
		return err
	}
	if epoch != x.epoch || x.model != x.Index.model {
		x.epoch = epoch
		x.model = x.Index.model
		x.initialized = false
		x.preferEvents = false
		x.afterWorkspace = uuid.Nil
		x.work = nil
		x.orphanCursor = 0
		x.orphanKeys = nil
	}
	// Alternate recovery/backfill and event pages. Event selection round-robins
	// workspace UUIDs and keeps progress per workspace, so a large lab cannot
	// monopolize maintenance while another lab waits.
	var workErr error
	if !x.initialized && !x.preferEvents {
		workErr = x.backfill(ctx)
		x.preferEvents = true
	} else {
		workErr = x.deliver(ctx)
		x.preferEvents = false
	}
	// Global legacy/orphan discovery never gates event completion and never scans
	// to completion for a single event. New entries use workspace-local manifests.
	cleanupErr := x.sweepOrphans(ctx)
	if workErr != nil {
		return workErr
	}
	return cleanupErr
}
func (x *Indexer) backfill(ctx context.Context) error {
	if x.work == nil {
		var ws uuid.UUID
		rows, err := x.Store.Pool.Query(ctx, `SELECT * FROM lab_index_workspaces_after($1,1)`, x.afterWorkspace)
		if err != nil {
			return err
		}
		found := rows.Next()
		if found {
			err = rows.Scan(&ws)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
		if !found {
			x.initialized = true
			return x.deliver(ctx)
		}
		x.work = &indexWork{WS: ws}
	}
	done, err := x.step(ctx, x.work)
	if err != nil {
		return err
	}
	if done {
		x.afterWorkspace = x.work.WS
		x.work = nil
	}
	return nil
}
func (x *Indexer) deliver(ctx context.Context) error {
	after := uuid.Nil
	if s, err := x.Index.rdb.Get(ctx, "labindex:lastworkspace").Result(); err == nil {
		after, _ = uuid.Parse(s)
	} else if err != redis.Nil {
		return err
	}
	events, err := x.pendingWorkspace(ctx, after)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	ws := events[0].ws
	if err := x.Index.rdb.Set(ctx, "labindex:lastworkspace", ws.String(), 0).Err(); err != nil {
		return err
	}
	jobKey := "labindex:job:" + ws.String()
	var w indexWork
	raw, err := x.Index.rdb.Get(ctx, jobKey).Bytes()
	if err == nil {
		if err := json.Unmarshal(raw, &w); err != nil {
			return err
		}
		if w.Model != x.Index.model {
			err = redis.Nil
		}
	} else if err != redis.Nil {
		return err
	}
	if err == redis.Nil {
		w = indexWork{WS: ws, Model: x.Index.model}
		var ignoredEvents, ignoredDirty []int64
		for _, e := range events {
			if relevantDelivery(e) {
				if e.dirty {
					w.Dirty = append(w.Dirty, e.id)
				} else {
					w.Events = append(w.Events, e.id)
				}
			} else {
				if e.dirty {
					ignoredDirty = append(ignoredDirty, e.id)
				} else {
					ignoredEvents = append(ignoredEvents, e.id)
				}
			}
		}
		if len(ignoredEvents)+len(ignoredDirty) > 0 {
			if err := x.ack(ctx, ignoredEvents, ignoredDirty); err != nil {
				return err
			}
		}
		if len(w.Events)+len(w.Dirty) == 0 {
			return nil
		}
	}
	done, err := x.step(ctx, &w)
	// Persist successful partial progress even after TEI failure. Only IDs captured
	// before this job began are acknowledged after its complete reconciliation.
	encoded, encodeErr := json.Marshal(w)
	if encodeErr != nil {
		return encodeErr
	}
	if saveErr := x.Index.rdb.Set(ctx, jobKey, encoded, 24*time.Hour).Err(); saveErr != nil {
		return saveErr
	}
	if err != nil {
		return err
	}
	if !done {
		return nil
	}
	if err := x.ack(ctx, w.Events, w.Dirty); err != nil {
		return err
	}
	return x.Index.rdb.Del(ctx, jobKey).Err()
}
func (x *Indexer) pendingWorkspace(ctx context.Context, after uuid.UUID) ([]indexEvent, error) {
	rows, err := x.Store.Pool.Query(ctx, `SELECT source,id,workspace_id,type,card_id FROM lab_pending_workspace_work('indexer',$1,200)`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []indexEvent
	for rows.Next() {
		var e indexEvent
		var source string
		if err := rows.Scan(&source, &e.id, &e.ws, &e.typ, &e.card); err != nil {
			return nil, err
		}
		e.dirty = source == "dirty"
		events = append(events, e)
	}
	return events, rows.Err()
}

// Run owns an independent cancellable maintenance loop; callers never put its
// embedding work on the flow-engine/autoresearch ticker.
func (x *Indexer) Run(ctx context.Context, interval time.Duration, onError func(error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := x.Tick(ctx); err != nil && ctx.Err() == nil && onError != nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (x *Indexer) ack(ctx context.Context, events, dirty []int64) error {
	_, err := x.Store.Pool.Exec(ctx, `SELECT lab_ack_work('indexer',$1,$2)`, events, dirty)
	return err
}

func (x *Indexer) read(ctx context.Context, ws uuid.UUID, r labmemory.Ref) (labmemory.Fact, error) {
	var f labmemory.Fact
	err := x.Store.Maintenance(ctx, func(tx *store.Tx) error {
		if err := tx.Workspace(ctx, ws); err != nil {
			return err
		}
		var err error
		f, err = labmemory.ReadMaintenance(ctx, tx, r)
		return err
	})
	return f, err
}
func (x *Indexer) step(ctx context.Context, w *indexWork) (bool, error) {
	budget := factBudget
	if !w.SourceDone {
		var refs []string
		err := x.Store.Maintenance(ctx, func(tx *store.Tx) error {
			if err := tx.Workspace(ctx, w.WS); err != nil {
				return err
			}
			var err error
			refs, err = tx.LabFactRefs(ctx, w.After, budget+1)
			return err
		})
		if err != nil {
			return false, err
		}
		n := len(refs)
		if n > budget {
			n = budget
		}
		for _, s := range refs[:n] {
			r, err := labmemory.ParseRef(s)
			if err != nil {
				return false, err
			}
			f, err := x.read(ctx, w.WS, r)
			if errors.Is(err, labmemory.ErrNotFound) || (err == nil && r.Kind == "comment" && f.Superseded) {
				if err := x.Index.Delete(ctx, w.WS, r); err != nil {
					return false, err
				}
				w.After = s
				continue
			}
			if err != nil {
				return false, err
			}
			fields, err := x.Index.rdb.HMGet(ctx, key(w.WS, r), "model", "version").Result()
			if err != nil {
				return false, err
			}
			if len(fields) != 2 || fields[0] != x.Index.model || fields[1] != f.Version {
				if err := x.Index.Put(ctx, w.WS, r, f.Version, f.Title+"\n"+f.Text); err != nil {
					return false, err
				}
			} else if err := x.Index.rdb.SAdd(ctx, membersKey(w.WS), r.String()).Err(); err != nil {
				return false, err
			}
			if r.Kind == "study" {
				var question string
				err := x.Store.Maintenance(ctx, func(tx *store.Tx) error {
					if err := tx.Workspace(ctx, w.WS); err != nil {
						return err
					}
					var err error
					question, err = tx.LessonQuestion(ctx, r.ID)
					return err
				})
				if err != nil {
					return false, err
				}
				// The maintenance transaction has released its connection before TEI.
				if err := x.Index.updateStudyVector(ctx, w.WS, r, question); err != nil {
					return false, err
				}
			}

			w.After = s
		}
		budget -= n
		w.SourceDone = len(refs) <= n
		if !w.SourceDone || budget == 0 {
			return false, nil
		}
	}
	if len(w.Keys) == 0 && !w.ScanDone {
		refs, next, err := x.Index.rdb.SScan(ctx, membersKey(w.WS), w.Scan, "*", int64(budget)).Result()
		if err != nil {
			return false, err
		}
		w.Keys = refs
		w.Scan = next
		w.ScanDone = next == 0
	}
	n := len(w.Keys)
	if n > budget {
		n = budget
	}
	for _, s := range w.Keys[:n] {
		r, err := labmemory.ParseRef(s)
		if err != nil {
			return false, err
		}
		f, err := x.read(ctx, w.WS, r)
		if errors.Is(err, labmemory.ErrNotFound) || (err == nil && r.Kind == "comment" && f.Superseded) {
			if err := x.Index.Delete(ctx, w.WS, r); err != nil {
				return false, err
			}
		} else if err != nil {
			return false, err
		}
	}
	w.Keys = w.Keys[n:]
	return w.ScanDone && len(w.Keys) == 0, nil
}

// Workspace deletion cascades its event rows. Redis namespace cleanup therefore
// uses bounded key discovery and a narrow global existence check, independent of
// event delivery. No source content is read globally.
func (x *Indexer) sweepOrphans(ctx context.Context) error {
	if len(x.orphanKeys) == 0 {
		keys, next, err := x.Index.rdb.Scan(ctx, x.orphanCursor, "lab:*", factBudget).Result()
		if err != nil {
			return err
		}
		x.orphanKeys = keys
		x.orphanCursor = next
	}
	n := len(x.orphanKeys)
	if n > factBudget {
		n = factBudget
	}
	exists := map[uuid.UUID]bool{}
	for _, k := range x.orphanKeys[:n] {
		parts := strings.SplitN(k, ":", 3)
		if len(parts) != 3 {
			return fmt.Errorf("invalid lab index key")
		}
		ws, err := uuid.Parse(parts[1])
		if err != nil {
			return err
		}
		live, ok := exists[ws]
		if !ok {
			if err := x.Store.Pool.QueryRow(ctx, `SELECT lab_index_workspace_exists($1)`, ws).Scan(&live); err != nil {
				return err
			}
			exists[ws] = live
		}
		r, err := labmemory.ParseRef(parts[2])
		if err != nil {
			return err
		}
		if live {
			f, readErr := x.read(ctx, ws, r)
			if errors.Is(readErr, labmemory.ErrNotFound) || (readErr == nil && r.Kind == "comment" && f.Superseded) {
				live = false
			} else if readErr != nil {
				return readErr
			}
		}
		if !live {
			if err := x.Index.Delete(ctx, ws, r); err != nil {
				return err
			}
		} else if err := x.Index.rdb.SAdd(ctx, membersKey(ws), r.String()).Err(); err != nil {
			return err
		}
	}
	x.orphanKeys = x.orphanKeys[n:]
	return nil
}
