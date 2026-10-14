package handler

import (
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"lykeion/server/internal/auth"
	"lykeion/server/internal/middleware"
	db "lykeion/server/pkg/db/generated"
)

const (
	codeTTL      = 10 * time.Minute
	codeAttempts = 5
)

func normEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// limited applies each (key, n, window) limit; any refusal or Redis error
// refuses the request.
func (h *H) limited(w http.ResponseWriter, r *http.Request, keys map[string]int) bool {
	for k, n := range keys {
		ok, err := h.Limit.Allow(r.Context(), k, n, time.Hour)
		if err != nil {
			writeErr(w, 503, "try again later")
			return true
		}
		if !ok {
			writeErr(w, 429, "too many attempts")
			return true
		}
	}
	return false
}

func (h *H) sendCode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if err := readJSON(r, &in); err != nil || !strings.Contains(in.Email, "@") {
		writeErr(w, 400, "email required")
		return
	}
	email := normEmail(in.Email)
	if h.limited(w, r, map[string]int{"code:email:" + email: 5, "code:ip:" + clientIP(r): 30}) {
		return
	}
	code := auth.NewCode()
	q := h.Store.Global()
	if err := q.SetVerificationCode(r.Context(), dbSetCode(email, code)); err != nil {
		fail(w, r, err)
		return
	}
	if err := h.Mail.Send(r.Context(), email, "Your Lykeion sign-in code", "Your code is "+code+". It expires in 10 minutes."); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (h *H) verifyCode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "email and code required")
		return
	}
	email := normEmail(in.Email)
	if h.limited(w, r, map[string]int{"verify:ip:" + clientIP(r): 60}) {
		return
	}
	tx, err := h.Store.Pool.Begin(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	q := db.New(tx)
	var vc db.VerificationCode
	err = tx.QueryRow(r.Context(), "SELECT email, code_hash, expires_at, attempts FROM verification_code WHERE email = $1 FOR UPDATE", email).Scan(&vc.Email, &vc.CodeHash, &vc.ExpiresAt, &vc.Attempts)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (time.Now().After(vc.ExpiresAt) || vc.Attempts >= codeAttempts)) {
		writeErr(w, 401, "invalid or expired code")
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	if subtle.ConstantTimeCompare([]byte(vc.CodeHash), []byte(auth.CodeHash(email, strings.TrimSpace(in.Code)))) != 1 {
		if err := q.BumpVerificationAttempts(r.Context(), email); err != nil {
			fail(w, r, err)
			return
		}
		if err := tx.Commit(r.Context()); err != nil {
			fail(w, r, err)
			return
		}
		writeErr(w, 401, "invalid or expired code")
		return
	}
	if err := q.DeleteVerificationCode(r.Context(), email); err != nil {
		fail(w, r, err)
		return
	}
	u, err := q.UpsertUserByEmail(r.Context(), email)
	if err != nil {
		fail(w, r, err)
		return
	}
	tok, err := h.JWT.Issue(u.ID, u.Email)
	if err != nil {
		fail(w, r, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		fail(w, r, err)
		return
	}
	h.setSession(w, tok)
	writeJSON(w, 200, map[string]any{"token": tok, "user": u})
}

func (h *H) setSession(w http.ResponseWriter, tok string) {
	http.SetCookie(w, &http.Cookie{Name: middleware.SessionCookie, Value: tok, Path: "/", HttpOnly: true,
		Secure: h.Secure, SameSite: http.SameSiteLaxMode, MaxAge: int(h.JWT.TTL.Seconds())})
}

func (h *H) logout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: middleware.SessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: h.Secure, MaxAge: -1})
	w.WriteHeader(204)
}

func (h *H) me(w http.ResponseWriter, r *http.Request) {
	u, err := h.Store.Global().GetUser(r.Context(), middleware.IdentityFrom(r.Context()).UserID)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, u)
}

// updateMe sets the caller's timezone. Only names the Go time database knows
// are stored, so later schedule and date rendering can load them without error.
func (h *H) updateMe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Timezone string `json:"timezone"`
	}
	if err := readJSON(r, &in); err != nil || in.Timezone == "" {
		writeErr(w, 400, "timezone required")
		return
	}
	// LoadLocation also accepts "Local" and "", which are not portable names.
	if _, err := time.LoadLocation(in.Timezone); err != nil || in.Timezone == "Local" {
		writeErr(w, 400, "unknown timezone")
		return
	}
	u, err := h.Store.Global().SetUserTimezone(r.Context(), db.SetUserTimezoneParams{
		ID: middleware.IdentityFrom(r.Context()).UserID, Timezone: in.Timezone})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, u)
}

// cliToken gives a signed-in browser a fresh session token to hand to the CLI.
func (h *H) cliToken(w http.ResponseWriter, r *http.Request) {
	id := middleware.IdentityFrom(r.Context())
	tok, err := h.JWT.Issue(id.UserID, id.Email)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]string{"token": tok})
}

func dbSetCode(email, code string) db.SetVerificationCodeParams {
	return db.SetVerificationCodeParams{Email: email, CodeHash: auth.CodeHash(email, code), ExpiresAt: time.Now().Add(codeTTL)}
}
