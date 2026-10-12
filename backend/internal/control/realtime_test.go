package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/store"
	"testing"
	"time"
)

func TestPresenceLeasesExpireAndRespectRevocation(t *testing.T) {
	api, db, user, w, token, _ := fixture(t)
	ctx := context.Background()
	id, err := api.openLease(ctx, w, user, token, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, "UPDATE realtime_leases SET presence_until=clock_timestamp()+interval '30 seconds' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	count := func(epoch int64) int {
		raw, err := api.presenceSnapshot(ctx, w, epoch)
		if err != nil {
			t.Fatal(err)
		}
		var value struct {
			Participants []any `json:"participants"`
		}
		if err = json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		return len(value.Participants)
	}
	if count(1) != 1 || count(2) != 0 {
		t.Fatal("presence epoch not enforced")
	}
	db.Pool.Exec(ctx, "UPDATE realtime_leases SET presence_until=clock_timestamp()-interval '1 second' WHERE id=$1", id)
	if count(1) != 0 {
		t.Fatal("expired presence shown")
	}
	db.Pool.Exec(ctx, "UPDATE realtime_leases SET presence_until=clock_timestamp()+interval '30 seconds' WHERE id=$1", id)
	db.Pool.Exec(ctx, "DELETE FROM sessions WHERE digest=$1", store.Hash(token))
	if count(1) != 0 {
		t.Fatal("revoked session remained visible")
	}
	for i := 0; i < 7; i++ {
		if _, err = api.openLease(ctx, w, user, token, 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = api.openLease(ctx, w, user, token, 1); !errors.Is(err, errConnectionLimit) {
		t.Fatal("connection cap missing", err)
	}
	db.Pool.Exec(ctx, "UPDATE realtime_leases SET expires_at=clock_timestamp()-interval '1 second' WHERE user_id=$1", user)
	if _, err = api.openLease(ctx, w, user, token, 1); err != nil {
		t.Fatal("expired leases block reconnection", err)
	}
}
func TestCommittedEventNotificationAndRollback(t *testing.T) {
	api, db, _, w, _, _ := fixture(t)
	api.startHints()
	ctx := context.Background()
	hints, stop := api.hints.subscribe()
	defer stop()
	// Wait until the listener is attached, without assuming scheduler timing.
	ready := false
	for i := 0; i < 40; i++ {
		db.Pool.Exec(ctx, "SELECT pg_notify('lykeion_events','')")
		select {
		case <-hints:
			ready = true
		case <-time.After(25 * time.Millisecond):
		}
		if ready {
			break
		}
	}
	if !ready {
		t.Fatal("notification listener did not start")
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO events VALUES($1,1,NULL,'{}')", w); err != nil {
		t.Fatal(err)
	}
	tx.Rollback(ctx)
	select {
	case <-hints:
		t.Fatal("rolled back event published")
	case <-time.After(60 * time.Millisecond):
	}
	if _, err = db.Pool.Exec(ctx, "INSERT INTO events VALUES($1,1,NULL,'{}')", w); err != nil {
		t.Fatal(err)
	}
	select {
	case <-hints:
	case <-time.After(time.Second):
		t.Fatal("committed event did not wake listener")
	}
}
