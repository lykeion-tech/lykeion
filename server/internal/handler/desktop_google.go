package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"html"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const desktopGoogleTTL = 5 * time.Minute

func desktopSecret(value string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(raw) == 32 && base64.RawURLEncoding.EncodeToString(raw) == value
}
func desktopGoogleKey(ticket string) string {
	digest := sha256.Sum256([]byte(ticket))
	return "lyk:desktop-google:" + base64.RawURLEncoding.EncodeToString(digest[:])
}

var beginDesktopGoogle = redis.NewScript(`
if redis.call('HGET',KEYS[1],'state') ~= 'created' then return nil end
redis.call('HSET',KEYS[1],'state','started')
return redis.call('HGET',KEYS[1],'challenge')
`)
var completeDesktopGoogle = redis.NewScript(`
if redis.call('HGET',KEYS[1],'state') ~= 'started' then return 0 end
redis.call('HSET',KEYS[1],'state','complete','user',ARGV[1],'email',ARGV[2],'code_hash',ARGV[3])
return 1
`)
var exchangeDesktopGoogle = redis.NewScript(`
local failures = tonumber(redis.call('GET',KEYS[2]) or '0')
if not failures or failures >= 1000 then return {'rate'} end
local valid_verifier = redis.call('HGET',KEYS[1],'challenge') == ARGV[1]
local valid_code = redis.call('HGET',KEYS[1],'code_hash') == ARGV[2]
if not valid_verifier or not valid_code then
  if valid_verifier and redis.call('HGET',KEYS[1],'state') == 'complete' then
    if redis.call('HINCRBY',KEYS[1],'bad_codes',1) >= 5 then redis.call('DEL',KEYS[1]) end
  end
  failures = redis.call('INCR',KEYS[2])
  if failures == 1 then redis.call('PEXPIRE',KEYS[2],3600000) end
  return {'invalid'}
end
if redis.call('HINCRBY',KEYS[1],'polls',1) > 32 then return {'exhausted'} end
if redis.call('HGET',KEYS[1],'state') ~= 'complete' then return {'pending'} end
local user = redis.call('HGET',KEYS[1],'user')
local email = redis.call('HGET',KEYS[1],'email')
redis.call('DEL',KEYS[1])
return {'complete',user,email}
`)

func (h *H) desktopGoogleStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input struct {
		Challenge string `json:"challenge"`
	}
	if readJSON(r, &input) != nil || !desktopSecret(input.Challenge) {
		writeErr(w, http.StatusBadRequest, "invalid challenge")
		return
	}
	if !h.Google.Enabled() || h.Limit.R == nil {
		writeErr(w, http.StatusServiceUnavailable, "Google sign-in unavailable")
		return
	}
	if h.limited(w, r, map[string]int{"google:desktop:start:ip:" + h.clientIP(r): 30}) {
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeErr(w, 503, "sign-in unavailable")
		return
	}
	ticket := base64.RawURLEncoding.EncodeToString(raw)
	key := desktopGoogleKey(ticket)
	// The random ticket is created and expires atomically; collisions fail closed.
	script := redis.NewScript(`if redis.call('EXISTS',KEYS[1]) ~= 0 then return 0 end redis.call('HSET',KEYS[1],'state','created','challenge',ARGV[1]) redis.call('PEXPIRE',KEYS[1],ARGV[2]) return 1`)
	created, err := script.Run(r.Context(), h.Limit.R, []string{key}, input.Challenge, desktopGoogleTTL.Milliseconds()).Int()
	if err != nil || created != 1 {
		writeErr(w, 503, "sign-in unavailable")
		return
	}
	writeJSON(w, 200, map[string]string{"ticket": ticket})
}

func (h *H) desktopGoogleExchange(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input struct {
		Ticket   string `json:"ticket"`
		Verifier string `json:"verifier"`
		Code     string `json:"code"`
	}
	if readJSON(r, &input) != nil || !desktopSecret(input.Ticket) || !desktopSecret(input.Verifier) || !desktopSecret(input.Code) {
		writeErr(w, 400, "invalid sign-in request")
		return
	}
	if h.Limit.R == nil {
		writeErr(w, 503, "sign-in unavailable")
		return
	}
	challenge := sha256.Sum256([]byte(input.Verifier))
	codeHash := sha256.Sum256([]byte(input.Code))
	result, err := exchangeDesktopGoogle.Run(r.Context(), h.Limit.R, []string{desktopGoogleKey(input.Ticket), "lyk:rl:google:desktop:exchange:ip:" + h.clientIP(r)}, base64.RawURLEncoding.EncodeToString(challenge[:]), base64.RawURLEncoding.EncodeToString(codeHash[:])).StringSlice()
	if err != nil {
		writeErr(w, 503, "sign-in unavailable")
		return
	}
	if len(result) > 0 && result[0] == "rate" {
		writeErr(w, 429, "too many attempts")
		return
	}
	if len(result) == 0 || result[0] == "invalid" {
		writeErr(w, 400, "sign-in expired")
		return
	}
	if result[0] == "exhausted" {
		writeErr(w, 429, "sign-in expired")
		return
	}
	if result[0] == "pending" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if len(result) != 3 || result[0] != "complete" {
		writeErr(w, 503, "sign-in unavailable")
		return
	}
	id, err := uuid.Parse(result[1])
	if err != nil {
		writeErr(w, 503, "sign-in unavailable")
		return
	}
	token, err := h.JWT.Issue(id, result[2])
	if err != nil {
		writeErr(w, 503, "sign-in unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"token": token, "expires_in": int64(h.JWT.TTL.Seconds()), "email": result[2]})
}

func (h *H) desktopGoogleBegin(w http.ResponseWriter, r *http.Request, ticket string) bool {
	if !desktopSecret(ticket) || h.Limit.R == nil {
		writeErr(w, 400, "sign-in expired")
		return false
	}
	_, err := beginDesktopGoogle.Run(r.Context(), h.Limit.R, []string{desktopGoogleKey(ticket)}).Text()
	if errors.Is(err, redis.Nil) {
		writeErr(w, 400, "sign-in expired")
		return false
	}
	if err != nil {
		writeErr(w, 503, "sign-in unavailable")
		return false
	}
	return true
}
func (h *H) desktopGoogleComplete(w http.ResponseWriter, r *http.Request, ticket string, id uuid.UUID, email string) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "sign-in unavailable")
		return
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(code))
	result, err := completeDesktopGoogle.Run(r.Context(), h.Limit.R, []string{desktopGoogleKey(ticket)}, id.String(), email, base64.RawURLEncoding.EncodeToString(hash[:])).Int()
	if err != nil || result != 1 {
		writeErr(w, 400, "sign-in expired")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<!doctype html><html lang=\"en\"><title>Lykeion sign-in</title><h1>Finish signing in</h1><p>Google account: " + html.EscapeString(email) + "</p><p>Paste this code only into Lykeion Desktop on this computer. Never give it to anyone or paste it into a website. If you did not start sign-in in your own app, close this page.</p><p><code id=\"desktop-code\">" + code + "</code></p><p>This code expires shortly and can be used only once.</p></html>"))
}
