// Package migrate applies numbered SQL files once each, in order, under an
// advisory lock so several servers starting together do not race.
package migrate

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// LockKey coordinates the migration writer with startup compatibility checks.
const LockKey int64 = 724501

type file struct {
	version int
	name    string
}

func files(fsys fs.FS) ([]file, error) {
	files, err := fs.Glob(fsys, "*.up.sql")
	if err != nil {
		return nil, err
	}
	var todo []file
	versions := make(map[int]string)
	for _, f := range files {
		v, err := strconv.Atoi(strings.SplitN(f, "_", 2)[0])
		if err != nil {
			return nil, fmt.Errorf("migration %s: name must start with a number", f)
		}
		if previous, ok := versions[v]; ok {
			return nil, fmt.Errorf("duplicate migration version %d: %s and %s", v, previous, f)
		}
		versions[v] = f
		todo = append(todo, file{v, f})
	}
	sort.Slice(todo, func(i, j int) bool { return todo[i].version < todo[j].version })
	return todo, nil
}

// Versions returns every embedded migration version in application order.
func Versions(fsys fs.FS) ([]int32, error) {
	all, err := files(fsys)
	if err != nil {
		return nil, err
	}
	versions := make([]int32, 0, len(all))
	for _, f := range all {
		if f.version < 1 || int64(f.version) > 2147483647 {
			return nil, fmt.Errorf("invalid migration version: %d", f.version)
		}
		versions = append(versions, int32(f.version))
	}
	return versions, nil
}

func Up(ctx context.Context, conn *pgx.Conn, fsys fs.FS) error {
	todo, err := files(fsys)
	if err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", LockKey); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", LockKey)
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version int PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	for _, f := range todo {
		var done bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", f.version).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		sql, err := fs.ReadFile(fsys, f.name)
		if err != nil {
			return err
		}
		if err := apply(ctx, conn, f.version, string(sql)); err != nil {
			return fmt.Errorf("%s: %w", f.name, err)
		}
	}
	return nil
}

func apply(ctx context.Context, conn *pgx.Conn, version int, sql string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, sql); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
