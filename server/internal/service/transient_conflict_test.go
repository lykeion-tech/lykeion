package service

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestTransientConflictIsDeadlockOrSerialization(t *testing.T) {
	deadlock := fmt.Errorf("consume: %w", &pgconn.PgError{Code: "40P01"})
	if !transientConflict(errors.Join(deadlock, nil)) || !transientConflict(&pgconn.PgError{Code: "40001"}) {
		t.Fatal("a deadlock or serialization failure is transient")
	}
	if transientConflict(&pgconn.PgError{Code: "23505"}) || transientConflict(errors.New("lease lost")) || transientConflict(nil) {
		t.Fatal("other errors are not")
	}
}
