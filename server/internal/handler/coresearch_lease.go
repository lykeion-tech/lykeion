package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"net/http"
	"time"
)

func coresearchClaim(r *http.Request, tx *store.Tx, token *uuid.UUID, expires *time.Time, status string, receipt *string, terminal bool) error {
	now, err := tx.CoresearchClock(r.Context())
	if err != nil {
		return err
	}
	return coresearchClaimAt(r, token, expires, status, receipt, terminal, now)
}

func coresearchClaimAt(r *http.Request, token *uuid.UUID, expires *time.Time, status string, receipt *string, terminal bool, now time.Time) error {
	supplied, err := uuid.Parse(r.URL.Query().Get("claim_token"))
	if err != nil || token == nil || supplied != *token {
		return service.ErrConflict
	}
	if status == "running" {
		if expires == nil || !expires.After(now) {
			return service.ErrConflict
		}
		return nil
	}
	if terminal && receipt != nil {
		return nil
	}
	return service.ErrConflict
}
func coresearchReceipt(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func coresearchFenceError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return service.ErrConflict
	}
	return err
}
func coresearchFenceRows(n int64, err error) error {
	if err != nil {
		return err
	}
	if n != 1 {
		return service.ErrConflict
	}
	return nil
}

func coresearchFenceAfterWrite(r *http.Request, tx *store.Tx, kind string, id uuid.UUID, token *uuid.UUID) error {
	live, err := tx.CoresearchClaimStillLive(r.Context(), kind, id, token)
	if err != nil {
		return err
	}
	if !live {
		return service.ErrConflict
	}
	return nil
}
