package handler

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/service"
)

func TestCoresearchClaimAtUsesSuppliedDatabaseTime(t *testing.T) {
	token := uuid.New()
	req := httptest.NewRequest("GET", "/?claim_token="+token.String(), nil)
	expires := time.Now().Add(time.Hour)
	if err := coresearchClaimAt(req, &token, &expires, "running", nil, false, expires.Add(time.Second)); !errors.Is(err, service.ErrConflict) {
		t.Fatalf("database-expired lease accepted while app clock is behind: %v", err)
	}
	if err := coresearchClaimAt(req, &token, &expires, "running", nil, false, expires.Add(-time.Second)); err != nil {
		t.Fatalf("database-live lease rejected: %v", err)
	}
}
