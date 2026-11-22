package github

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"lykeion/server/internal/store"
	"lykeion/server/internal/testutil"
)

func TestGithubMirrorPRState(t *testing.T) {
	merged := time.Now()
	for _, tc := range []struct {
		p    PullRequest
		want string
	}{
		{PullRequest{State: "open"}, "open"},
		{PullRequest{State: "open", Draft: true}, "draft"},
		{PullRequest{State: "closed", Draft: true}, "closed"},
		{PullRequest{State: "closed", Draft: true, Merged: true}, "merged"},
		{PullRequest{State: "closed", MergedAt: &merged}, "merged"},
	} {
		if got := tc.p.state(); got != tc.want {
			t.Errorf("%+v: got %s, want %s", tc.p, got, tc.want)
		}
	}
}

func TestGithubMirrorDiscardsSessionOnUnconfirmedUnlock(t *testing.T) {
	env := testutil.Setup(t)
	cfg := env.App.Config()
	cfg.MinConns = 0
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := Service{Store: &store.Store{Pool: pool}}
	var lockedPID uint32
	err = s.withMirrorLock(t.Context(), 42, PullRequest{Owner: "org", Repo: "repo", Number: 1}, func(conn *pgxpool.Conn) error {
		lockedPID = conn.Conn().PgConn().PID()
		// Simulate a session that cannot confirm releasing its expected lock.
		_, err := conn.Exec(t.Context(), "select pg_advisory_unlock_all()")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Release()
	if replacement.Conn().PgConn().PID() == lockedPID {
		t.Fatal("pool reused a session after an unconfirmed unlock")
	}
}
