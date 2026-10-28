package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"lykeion/server/internal/service"
	"net/http"
	"time"
)

func coresearchClaim(r *http.Request, token *uuid.UUID, expires *time.Time, status string, receipt *string, terminal bool) error {
	supplied, err := uuid.Parse(r.URL.Query().Get("claim_token"))
	if err != nil || token == nil || supplied != *token {
		return service.ErrConflict
	}
	if status == "running" {
		if expires == nil || !expires.After(time.Now()) {
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
