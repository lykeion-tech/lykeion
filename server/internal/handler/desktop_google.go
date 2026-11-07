package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type desktopExchangeRecord struct {
	Challenge  string `json:"challenge"`
	Host       string `json:"host"`
	UserID     string `json:"user_id"`
	Email      string `json:"email"`
	Deployment string `json:"deployment"`
}

func (h *H) desktopDeployment() (string, error) {
	u, err := url.Parse(h.PublicURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("invalid public URL")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return "", errors.New("invalid public URL")
		}
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), nil
}

func desktopExchangeKey(code string) string {
	hash := sha256.Sum256([]byte(code))
	return "lyk:desktop:exchange:" + base64.RawURLEncoding.EncodeToString(hash[:])
}
func (h *H) createDesktopExchange(ctx context.Context, challenge, host, userID, email string) (string, error) {
	if h.Limit.R == nil {
		return "", errors.New("exchange unavailable")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	deployment, err := h.desktopDeployment()
	if err != nil {
		return "", err
	}
	record, err := json.Marshal(desktopExchangeRecord{Challenge: challenge, Host: strings.ToLower(host), UserID: userID, Email: email, Deployment: deployment})
	if err != nil {
		return "", err
	}
	ok, err := h.Limit.R.SetNX(ctx, desktopExchangeKey(code), record, 90*time.Second).Result()
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("exchange collision")
	}
	return code, nil
}

var redeemDesktopScript = redis.NewScript(`
local val = redis.call('GET', KEYS[1])
if not val then return '' end
local obj = cjson.decode(val)
if obj.challenge ~= ARGV[1] or obj.host ~= ARGV[2] or obj.deployment ~= ARGV[3] then return '' end
redis.call('DEL', KEYS[1])
return val
`)

func (h *H) desktopExchange(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	// This endpoint is for the native client, which has no browser Origin. Do
	// not accept browser submissions even when they hold a valid exchange proof.
	if err != nil || mediaType != "application/json" || len(r.Header.Values("Origin")) != 0 ||
		(r.Header.Get("Sec-Fetch-Site") != "" && r.Header.Get("Sec-Fetch-Site") != "none") ||
		r.Header.Get("Sec-Fetch-Mode") == "navigate" || r.Header.Get("Sec-Fetch-Dest") == "document" {
		writeErr(w, http.StatusForbidden, "invalid exchange request")
		return
	}
	if h.limited(w, r, map[string]int{"desktop:exchange:ip:" + clientIP(r): 60}) {
		return
	}
	var input struct {
		Code     string `json:"code"`
		Verifier string `json:"verifier"`
	}
	if readJSON(r, &input) != nil || !validCLIState(input.Code, "1") || !validCLIState(input.Verifier, "1") || input.Code == "" || input.Verifier == "" {
		writeErr(w, http.StatusBadRequest, "invalid exchange")
		return
	}
	digest := sha256.Sum256([]byte(input.Verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	if h.Limit.R == nil {
		writeErr(w, http.StatusServiceUnavailable, "desktop sign-in unavailable")
		return
	}
	deployment, err := h.desktopDeployment()
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "desktop sign-in unavailable")
		return
	}
	value, err := redeemDesktopScript.Run(r.Context(), h.Limit.R, []string{desktopExchangeKey(input.Code)}, challenge, strings.ToLower(r.Host), deployment).Text()
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "desktop sign-in unavailable")
		return
	}
	if value == "" {
		writeErr(w, http.StatusUnauthorized, "invalid or expired exchange")
		return
	}
	var record desktopExchangeRecord
	if json.Unmarshal([]byte(value), &record) != nil {
		writeErr(w, http.StatusServiceUnavailable, "desktop sign-in unavailable")
		return
	}
	id, err := uuid.Parse(record.UserID)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "desktop sign-in unavailable")
		return
	}
	tok, err := h.JWT.Issue(id, record.Email)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "desktop sign-in unavailable")
		return
	}
	h.setSession(w, tok)
	w.WriteHeader(http.StatusNoContent)
}
