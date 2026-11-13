package migrate_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/migrate"
	"lykeion/server/internal/testutil"
)

func TestVersionsValidatesAndSorts(t *testing.T) {
	files := fstest.MapFS{"0010_second.up.sql": {Data: []byte("SELECT 1")}, "0002_first.up.sql": {Data: []byte("SELECT 1")}}
	got, err := migrate.Versions(files)
	if err != nil || !reflect.DeepEqual(got, []int32{2, 10}) {
		t.Fatalf("versions: %v %v", got, err)
	}
	files["0010_duplicate.up.sql"] = &fstest.MapFile{Data: []byte("SELECT 1")}
	if _, err := migrate.Versions(files); err == nil {
		t.Fatal("duplicate accepted")
	}
	delete(files, "0010_duplicate.up.sql")
	files["bad.up.sql"] = &fstest.MapFile{Data: []byte("SELECT 1")}
	if _, err := migrate.Versions(files); err == nil {
		t.Fatal("invalid name accepted")
	}
}

func TestUpAppliesOnceInOrder(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, env.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	fsys := fstest.MapFS{
		"0101_a.up.sql": {Data: []byte("CREATE TABLE t_a (n int); INSERT INTO t_a VALUES (1);")},
		"0102_b.up.sql": {Data: []byte("INSERT INTO t_a VALUES (2);")},
	}
	for i := 0; i < 2; i++ {
		if err := migrate.Up(ctx, conn, fsys); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM t_a").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("migrations ran %d inserts, want 2", n)
	}
}

func TestFailedMigrationLeavesNoTrace(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, env.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	bad := fstest.MapFS{"0201_bad.up.sql": {Data: []byte("CREATE TABLE t_b (n int); SELECT nope;")}}
	if err := migrate.Up(ctx, conn, bad); err == nil {
		t.Fatal("want error")
	}
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('t_b') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("a failed migration left its table behind")
	}
}

func TestFailedMigrationDoesNotRecordVersionAndCanRetry(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, env.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	bad := fstest.MapFS{"0301_retry.up.sql": {Data: []byte("CREATE TABLE t_retry (n int); SELECT nope;")}}
	if err := migrate.Up(ctx, conn, bad); err == nil {
		t.Fatal("want migration error")
	}
	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM schema_migrations WHERE version = 301").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("failed migration recorded its version")
	}
	bad["0301_retry.up.sql"].Data = []byte("CREATE TABLE t_retry (n int); INSERT INTO t_retry VALUES (7)")
	if err := migrate.Up(ctx, conn, bad); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, "SELECT n FROM t_retry").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 7 {
		t.Fatalf("retried migration inserted %d, want 7", n)
	}
}

func TestDuplicateVersionsFailBeforeApplyingAnyFile(t *testing.T) {
	env := testutil.Setup(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, env.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	fsys := fstest.MapFS{
		"0400_first.up.sql": {Data: []byte("CREATE TABLE t_first (n int)")},
		"0401_a.up.sql":     {Data: []byte("CREATE TABLE t_duplicate_a (n int)")},
		"401_b.up.sql":      {Data: []byte("CREATE TABLE t_duplicate_b (n int)")},
	}
	err = migrate.Up(ctx, conn, fsys)
	if err == nil {
		t.Fatal("duplicate numeric migration versions were accepted")
	}
	if !strings.Contains(err.Error(), "duplicate migration version 401") {
		t.Fatalf("want explicit duplicate error, got %v", err)
	}
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('t_first') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("applied earlier file before rejecting duplicate versions")
	}
	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM schema_migrations WHERE version IN (400, 401)").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("recorded %d invalid batch versions", n)
	}
}
