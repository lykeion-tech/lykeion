package handler

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
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

// clientIP is the rate-limit key: the peer's address, or the client a trusted
// reverse proxy forwarded for. Headers from any other peer are ignored.
func (h *H) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !slices.ContainsFunc(h.TrustedProxies, func(p netip.Prefix) bool { return p.Contains(peer.Unmap()) }) {
		return host
	}
	forwarded, _, _ := strings.Cut(r.Header.Get("X-Forwarded-For"), ",")
	if forwarded = strings.TrimSpace(forwarded); forwarded == "" {
		forwarded = strings.TrimSpace(r.Header.Get("X-Real-IP"))
	}
	if client, err := netip.ParseAddr(forwarded); err == nil {
		return client.Unmap().String()
	}
	return host
}

// ParseTrustedProxies reads comma-separated CIDRs, dropping invalid entries
// with a warning.
func ParseTrustedProxies(raw string) []netip.Prefix {
	var out []netip.Prefix
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item == "" {
			continue
		}
		p, err := netip.ParsePrefix(item)
		if err != nil {
			slog.Warn("TRUSTED_PROXIES: ignoring invalid CIDR", "value", item)
			continue
		}
		out = append(out, p.Masked())
	}
	return out
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
	if h.limited(w, r, map[string]int{"code:email:" + email: 5, "code:ip:" + h.clientIP(r): 30}) {
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
	if h.limited(w, r, map[string]int{"verify:ip:" + h.clientIP(r): 60}) {
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

var careerStages = []string{"", "undergraduate", "masters", "phd", "postdoc", "research_staff", "faculty", "industry", "other"}

// updateMe changes any of the caller's name, timezone and career stage, and
// marks onboarding finished (onboarded can only be true). Only timezone names
// the Go time database knows are stored, so later schedule and date rendering
// can load them without error.
func (h *H) updateMe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        *string `json:"name"`
		Timezone    *string `json:"timezone"`
		CareerStage *string `json:"career_stage"`
		Onboarded   *bool   `json:"onboarded"`
		AvatarURL   *string `json:"avatar_url"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "invalid profile: "+err.Error())
		return
	}
	switch {
	case in.Name == nil && in.Timezone == nil && in.CareerStage == nil && in.Onboarded == nil && in.AvatarURL == nil:
		writeErr(w, 400, "nothing to change")
		return
	case in.Name != nil && strings.TrimSpace(*in.Name) == "":
		writeErr(w, 400, "name cannot be empty")
		return
	case in.CareerStage != nil && !slices.Contains(careerStages, *in.CareerStage):
		writeErr(w, 400, "unknown career stage")
		return
	case in.Onboarded != nil && !*in.Onboarded:
		writeErr(w, 400, "onboarded can only be set to true")
		return
	}
	if in.AvatarURL != nil && !validUserAvatar(*in.AvatarURL) {
		writeErr(w, 400, "Choose a PNG or JPEG image of at most 512 KB.")
		return
	}
	if in.Timezone != nil {
		// LoadLocation also accepts "Local" and "", which are not portable names.
		if _, err := time.LoadLocation(*in.Timezone); err != nil || *in.Timezone == "" || *in.Timezone == "Local" {
			writeErr(w, 400, "unknown timezone")
			return
		}
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		in.Name = &name
	}
	u, err := h.Store.Global().UpdateMe(r.Context(), db.UpdateMeParams{Name: in.Name, Timezone: in.Timezone,
		CareerStage: in.CareerStage, AvatarUrl: in.AvatarURL, Onboarded: in.Onboarded != nil, ID: middleware.IdentityFrom(r.Context()).UserID})
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, 200, u)
}

func validUserAvatar(value string) bool {
	if value == "" {
		return true
	}
	var mime string
	switch {
	case strings.HasPrefix(value, "data:image/png;base64,"):
		mime = "image/png"
	case strings.HasPrefix(value, "data:image/jpeg;base64,"):
		mime = "image/jpeg"
	default:
		return false
	}
	encoded := value[strings.IndexByte(value, ',')+1:]
	if len(encoded) > base64.StdEncoding.EncodedLen(512*1024) {
		return false
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) == 0 || len(data) > 512*1024 || http.DetectContentType(data) != mime {
		return false
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	return err == nil && config.Width > 0 && config.Height > 0 && config.Width <= 4096 && config.Height <= 4096 && ((mime == "image/png" && format == "png") || (mime == "image/jpeg" && format == "jpeg"))
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
