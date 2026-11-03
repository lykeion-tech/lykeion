package labindex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
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
	ws         uuid.UUID
	event      int64 // zero for backfill
	after      string
	sourceDone bool
	scan       uint64
	keys       []string
	scanDone   bool
}
type indexEvent struct {
	id   int64
	ws   uuid.UUID
	typ  string
	card *uuid.UUID
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
	if err := x.sweepOrphans(ctx); err != nil {
		return err
	}
	if x.work == nil && x.preferEvents {
		if err := x.chooseEvent(ctx); err != nil {
			return err
		}
		x.preferEvents = false
	}
	if x.work == nil && !x.initialized {
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
		if found {
			x.work = &indexWork{ws: ws}
		} else {
			x.initialized = true
		}
	}
	if x.work == nil {
		if err := x.chooseEvent(ctx); err != nil {
			return err
		}
	}

	if x.work == nil {
		return nil
	}
	done, err := x.step(ctx, x.work)
	if err != nil {
		return err
	}
	if !done {
		return nil
	}
	if x.work.event != 0 {
		if err := x.ack(ctx, x.work.event); err != nil {
			return err
		}
	} else {
		x.afterWorkspace = x.work.ws
		x.preferEvents = true
	}
	x.work = nil
	return nil
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
func (x *Indexer) chooseEvent(ctx context.Context) error {
	events, err := x.pending(ctx)
	if err != nil {
		return err
	}
	for _, e := range events {
		if relevantDelivery(e) {
			x.work = &indexWork{ws: e.ws, event: e.id}
			break
		}
		if err := x.ack(ctx, e.id); err != nil {
			return err
		}
	}
	return nil
}
func (x *Indexer) pending(ctx context.Context) ([]indexEvent, error) {
	rows, err := x.Store.Pool.Query(ctx, `SELECT id,workspace_id,type,card_id FROM lab_pending_events('indexer',16)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []indexEvent
	for rows.Next() {
		var e indexEvent
		if err := rows.Scan(&e.id, &e.ws, &e.typ, &e.card); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
func (x *Indexer) ack(ctx context.Context, id int64) error {
	_, err := x.Store.Pool.Exec(ctx, `SELECT lab_ack_event('indexer',$1)`, id)
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
	if !w.sourceDone {
		var refs []string
		err := x.Store.Maintenance(ctx, func(tx *store.Tx) error {
			if err := tx.Workspace(ctx, w.ws); err != nil {
				return err
			}
			var err error
			refs, err = tx.LabFactRefs(ctx, w.after, factBudget+1)
			return err
		})
		if err != nil {
			return false, err
		}
		n := len(refs)
		if n > factBudget {
			n = factBudget
		}
		for _, s := range refs[:n] {
			r, err := labmemory.ParseRef(s)
			if err != nil {
				return false, err
			}
			f, err := x.read(ctx, w.ws, r)
			if errors.Is(err, labmemory.ErrNotFound) || (err == nil && r.Kind == "comment" && f.Superseded) {
				if err := x.Index.Delete(ctx, w.ws, r); err != nil {
					return false, err
				}
				w.after = s
				continue
			}
			if err != nil {
				return false, err
			}
			fields, err := x.Index.rdb.HMGet(ctx, key(w.ws, r), "model", "version").Result()
			if err != nil {
				return false, err
			}
			if len(fields) != 2 || fields[0] != x.Index.model || fields[1] != f.Version {
				if err := x.Index.Put(ctx, w.ws, r, f.Version, f.Title+"\n"+f.Text); err != nil {
					return false, err
				}
			}
			w.after = s
		}
		w.sourceDone = len(refs) <= factBudget
		// Source processing and pruning each consume separate bounded ticks.
		return false, nil
	}
	if len(w.keys) == 0 && !w.scanDone {
		keys, next, err := x.Index.rdb.Scan(ctx, w.scan, "lab:"+w.ws.String()+":*", factBudget).Result()
		if err != nil {
			return false, err
		}
		w.keys = keys
		w.scan = next
		w.scanDone = next == 0
	}
	n := len(w.keys)
	if n > factBudget {
		n = factBudget
	}
	for _, k := range w.keys[:n] {
		r, err := labmemory.ParseRef(strings.TrimPrefix(k, "lab:"+w.ws.String()+":"))
		if err != nil {
			return false, err
		}
		f, err := x.read(ctx, w.ws, r)
		if errors.Is(err, labmemory.ErrNotFound) || (err == nil && r.Kind == "comment" && f.Superseded) {
			if err := x.Index.rdb.Del(ctx, k).Err(); err != nil {
				return false, err
			}
		} else if err != nil {
			return false, err
		}
	}
	w.keys = w.keys[n:]
	return w.scanDone && len(w.keys) == 0, nil
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
		if !live {
			if err := x.Index.rdb.Del(ctx, k).Err(); err != nil {
				return err
			}
		}
	}
	x.orphanKeys = x.orphanKeys[n:]
	return nil
}
