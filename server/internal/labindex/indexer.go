package labindex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
	"lykeion/server/internal/labmemory"
	"lykeion/server/internal/store"
)

// Indexer consumes durable events. Redis writes may be replayed after a failed
// PostgreSQL cursor commit; writes and deletions are intentionally idempotent.
// Construct once and call Tick serially. Every process start backfills current
// shared facts, including facts predating the consumer and changed model IDs.
type Indexer struct {
	Store       *store.Store
	Index       *Index
	mu          sync.Mutex
	initialized bool
}

func relevantEvent(typ string) bool {
	// Strict allowlist: chat/answer and Coresearch events never trigger indexing.
	for _, prefix := range []string{"record.", "card.", "comment.", "inbox.", "workspace.", "team.", "pack.", "attachment.", "agent_run.", "lesson."} {
		if strings.HasPrefix(typ, prefix) {
			return true
		}
	}
	return false
}

func (x *Indexer) Tick(ctx context.Context) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.Store == nil || x.Index == nil {
		return errors.New("indexer dependencies required")
	}
	// Serialize consumers across server replicas, keeping reconciliation/cursor
	// order consistent. A connection-scoped lock survives the short fact txs.
	conn, err := x.Store.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(63267492245901)`).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(63267492245901)`)
	if !x.initialized {
		ids, err := x.Store.Global().AllWorkspaceIDs(ctx)
		if err != nil {
			return err
		}
		for _, ws := range ids {
			if err := x.reconcile(ctx, ws); err != nil {
				return err
			}
		}
		x.initialized = true
	}
	rows, err := conn.Query(ctx, `SELECT id,workspace_id,type FROM lab_events_after('indexer',200)`)
	if err != nil {
		return err
	}
	type event struct {
		id  int64
		ws  uuid.UUID
		typ string
	}
	var events []event
	for rows.Next() {
		var e event
		if err := rows.Scan(&e.id, &e.ws, &e.typ); err != nil {
			rows.Close()
			return err
		}
		events = append(events, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, e := range events {
		if relevantEvent(e.typ) {
			if err := x.reconcile(ctx, e.ws); err != nil {
				return fmt.Errorf("index event %d: %w", e.id, err)
			}
		}
		if _, err := conn.Exec(ctx, `SELECT lab_advance('indexer',$1)`, e.id); err != nil {
			return err
		}
	}
	return nil
}

// Reconcile the event's workspace rather than trusting payload IDs: several
// inbox.created events carry only card_id, and card.deleted carries no CardID.
// This also refreshes parent links and removes deleted study descendants.
func (x *Indexer) reconcile(ctx context.Context, ws uuid.UUID) error {
	live := map[string]bool{}
	err := x.Store.Maintenance(ctx, func(tx *store.Tx) error {
		if err := tx.Workspace(ctx, ws); err != nil {
			return err
		}
		refs, err := tx.LabFactRefs(ctx)
		if err != nil {
			return err
		}
		for _, s := range refs {
			ref, err := labmemory.ParseRef(s)
			if err != nil {
				return err
			}
			f, err := labmemory.ReadMaintenance(ctx, tx, ref)
			if errors.Is(err, labmemory.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if ref.Kind == "comment" && f.Superseded {
				continue
			}
			k := key(ws, ref)
			live[k] = true
			fields, err := x.Index.rdb.HMGet(ctx, k, "model", "version").Result()
			if err != nil {
				return err
			}
			if len(fields) == 2 && fields[0] == x.Index.model && fields[1] == f.Version {
				continue
			}
			if err := x.Index.Put(ctx, ws, ref, f.Version, f.Title+"\n"+f.Text); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	var cursor uint64
	for {
		keys, next, err := x.Index.rdb.Scan(ctx, cursor, "lab:"+ws.String()+":*", 200).Result()
		if err != nil {
			return err
		}
		for _, k := range keys {
			if !live[k] {
				if err := x.Index.rdb.Del(ctx, k).Err(); err != nil {
					return err
				}
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return nil
}
