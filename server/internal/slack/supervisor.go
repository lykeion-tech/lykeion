package slack

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

const superviseEvery = 10 * time.Second

// renew extends a lease only while this instance still owns it.
var renew = redis.NewScript(`if redis.call('get', KEYS[1]) == ARGV[1] then return redis.call('pexpire', KEYS[1], ARGV[2]) end return 0`)

// release deletes a lease only while this instance still owns it.
var release = redis.NewScript(`if redis.call('get', KEYS[1]) == ARGV[1] then return redis.call('del', KEYS[1]) end return 0`)

func leaseKey(id uuid.UUID) string { return "lyk:slack:lease:" + id.String() }

type holding struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// supervisor holds the installations whose lease this instance owns.
type supervisor struct {
	s    *Service
	id   string
	held map[uuid.UUID]holding
}

func (s *Service) supervisor() *supervisor {
	return &supervisor{s: s, id: uuid.NewString(), held: map[uuid.UUID]holding{}}
}

func (s *Service) ttl() time.Duration {
	if s.LeaseTTL > 0 {
		return s.LeaseTTL
	}
	return 30 * time.Second
}

// Run keeps one Socket Mode connection per active installation across all
// server instances: each instance holds the installations whose Redis lease it
// owns, renews them every 10 seconds and takes over leases that lapse.
func (s *Service) Run(ctx context.Context) {
	sv := s.supervisor()
	defer sv.stop()
	t := time.NewTicker(superviseEvery)
	defer t.Stop()
	for {
		sv.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (sv *supervisor) tick(ctx context.Context) {
	r, ttl := sv.s.Redis, sv.s.ttl()
	for id, h := range sv.held {
		select {
		case <-h.done: // the connection ended (revoked or failed); free it for a retry
			sv.drop(id)
			continue
		default:
		}
		// An unconfirmed renewal counts as lost: another instance may own it now.
		if n, err := renew.Run(ctx, r, []string{leaseKey(id)}, sv.id, ttl.Milliseconds()).Int(); err != nil || n == 0 {
			sv.drop(id)
		}
	}
	rows, err := sv.s.Store.Global().SlackActiveInstallations(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("slack supervisor: list installations failed", "err", err)
		}
		return
	}
	active := make(map[uuid.UUID]bool, len(rows))
	for _, row := range rows {
		active[row.ID] = true
	}
	for id := range sv.held {
		if !active[id] { // revoked or disconnected
			sv.drop(id)
		}
	}
	for _, row := range rows {
		if _, ok := sv.held[row.ID]; ok {
			continue
		}
		err := r.Do(ctx, "SET", leaseKey(row.ID), sv.id, "NX", "PX", ttl.Milliseconds()).Err()
		if errors.Is(err, redis.Nil) {
			continue // another instance holds it
		}
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("slack supervisor: lease failed", "installation", row.ID, "err", err)
			}
			continue
		}
		hold := sv.s.holdFunc
		if hold == nil {
			hold = sv.s.hold
		}
		hctx, cancel := context.WithCancel(ctx)
		h := holding{cancel: cancel, done: make(chan struct{})}
		sv.held[row.ID] = h
		go func(ws, id uuid.UUID) {
			defer close(h.done)
			hold(hctx, ws, id)
		}(row.WorkspaceID, row.ID)
	}
}

// drop stops holding an installation and releases its lease if still ours.
func (sv *supervisor) drop(id uuid.UUID) {
	h := sv.held[id]
	delete(sv.held, id)
	h.cancel()
	<-h.done
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	release.Run(ctx, sv.s.Redis, []string{leaseKey(id)}, sv.id)
}

func (sv *supervisor) stop() {
	for id := range sv.held {
		sv.drop(id)
	}
}

// load reads an installation by id under its own workspace.
func (s *Service) load(ctx context.Context, ws, id uuid.UUID) (inst db.SlackInstallation, err error) {
	err = s.Store.InWorkspace(ctx, ws, uuid.Nil, func(tx *store.Tx) error {
		inst, err = tx.Q.GetSlackInstallationByID(ctx, id)
		return err
	})
	return
}

// hold runs an installation's Socket Mode connection and delivers its
// finished answers until ctx is done or the connection ends.
func (s *Service) hold(ctx context.Context, ws, id uuid.UUID) {
	inst, err := s.load(ctx, ws, id)
	if err != nil || inst.Status != "active" {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	socketDone := make(chan struct{})
	// Return only once the connection is closed, so a released lease never
	// leaves this connection open beside the next holder's.
	defer func() { cancel(); <-socketDone }()
	go func() {
		defer close(socketDone)
		defer cancel()
		if err := s.socket(ctx, inst); err != nil && ctx.Err() == nil {
			slog.Warn("slack connection ended", "installation", inst.ID, "err", err)
		}
	}()
	// ponytail: 2 s poll per installation; switch to a Redis wake-up if delivery latency matters.
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.DeliverOnce(ctx, inst); err != nil && ctx.Err() == nil {
				slog.Warn("slack delivery failed", "installation", inst.ID, "err", err)
			}
		}
	}
}

// revoke marks the installation revoked so owners see it in Settings.
func (s *Service) revoke(ctx context.Context, inst db.SlackInstallation) error {
	return s.Store.InWorkspace(ctx, inst.WorkspaceID, inst.InstalledBy, func(tx *store.Tx) error {
		if _, err := tx.Q.SetSlackInstallationStatus(ctx, db.SetSlackInstallationStatusParams{Status: "revoked", ID: inst.ID}); err != nil {
			return err
		}
		return tx.Emit(ctx, "slack.changed", nil, store.System, nil)
	})
}
