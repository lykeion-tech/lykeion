package handler

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	db "lykeion/server/pkg/db/generated"
)

const oauthCookie = "lyk_oauth"

func validCLIPort(port string) (string, bool) {
	if port == "" {
		return "", true
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1024 || n > 65535 {
		return "", false
	}
	return strconv.Itoa(n), true
}

func validCLIState(state, port string) bool {
	if state == "" {
		return true
	}
	if port == "" || len(state) != 43 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(state)
	return err == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == state
}

func oauthSignature(key []byte, state, port string, cliState ...string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(state))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(port))
	if len(cliState) > 0 && cliState[0] != "" {
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte(cliState[0]))
	}
	return mac.Sum(nil)
}

func (h *H) googleStart(w http.ResponseWriter, r *http.Request) {
	if !h.Google.Enabled() {
		writeErr(w, http.StatusNotFound, "google sign-in is not configured")
		return
	}
	port, ok := validCLIPort(r.URL.Query().Get("cli_port"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "cli_port must be between 1024 and 65535")
		return
	}
	cliState := r.URL.Query().Get("cli_state")
	if !validCLIState(cliState, port) {
		writeErr(w, http.StatusBadRequest, "invalid CLI callback state")
		return
	}
	if len(h.JWT.Secret) == 0 {
		writeErr(w, http.StatusInternalServerError, "google sign-in is unavailable")
		return
	}

	rawState := make([]byte, 32)
	if _, err := rand.Read(rawState); err != nil {
		writeErr(w, http.StatusInternalServerError, "google sign-in is unavailable")
		return
	}
	state := base64.RawURLEncoding.EncodeToString(rawState)
	signature := base64.RawURLEncoding.EncodeToString(oauthSignature(h.JWT.Secret, state, port, cliState))
	cookieValue := state + "|" + port + "|" + signature
	if cliState != "" {
		cookieValue = state + "|" + port + "|" + cliState + "|" + signature
	}
	http.SetCookie(w, &http.Cookie{Name: oauthCookie, Value: cookieValue, Path: "/auth/google",
		HttpOnly: true, Secure: h.Secure, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	http.Redirect(w, r, h.Google.AuthCodeURL(state), http.StatusFound)
}

func (h *H) googleCallback(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(oauthCookie)
	if err != nil || len(h.JWT.Secret) == 0 {
		writeErr(w, http.StatusBadRequest, "sign-in expired; start again")
		return
	}
	parts := strings.Split(c.Value, "|")
	if (len(parts) != 3 && len(parts) != 4) || parts[0] == "" || parts[len(parts)-1] == "" {
		writeErr(w, http.StatusBadRequest, "sign-in expired; start again")
		return
	}
	state, port := parts[0], parts[1]
	cliState := ""
	if len(parts) == 4 {
		cliState = parts[2]
		if cliState == "" {
			writeErr(w, http.StatusBadRequest, "invalid CLI callback state")
			return
		}
	}
	if !validCLIState(cliState, port) {
		writeErr(w, http.StatusBadRequest, "invalid CLI callback state")
		return
	}
	providedSignature, err := base64.RawURLEncoding.DecodeString(parts[len(parts)-1])
	expectedSignature := oauthSignature(h.JWT.Secret, state, port, cliState)
	if err != nil || subtle.ConstantTimeCompare(providedSignature, expectedSignature) != 1 {
		writeErr(w, http.StatusBadRequest, "state mismatch")
		return
	}
	queryState := r.URL.Query().Get("state")
	if queryState == "" || subtle.ConstantTimeCompare([]byte(state), []byte(queryState)) != 1 {
		writeErr(w, http.StatusBadRequest, "state mismatch")
		return
	}
	port, ok := validCLIPort(port)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid CLI callback port")
		return
	}
	code := r.URL.Query().Get("code")
	if strings.TrimSpace(code) == "" {
		writeErr(w, http.StatusBadRequest, "authorization code required")
		return
	}

	http.SetCookie(w, &http.Cookie{Name: oauthCookie, Path: "/auth/google", MaxAge: -1})
	gu, err := h.Google.Exchange(r.Context(), code)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "google sign-in failed")
		return
	}
	u, err := h.Store.Global().UpsertUserByGoogle(r.Context(), db.UpsertUserByGoogleParams{Email: gu.Email, Name: gu.Name, GoogleSub: &gu.Sub})
	if err != nil {
		fail(w, r, err)
		return
	}
	tok, err := h.JWT.Issue(u.ID, u.Email)
	if err != nil {
		fail(w, r, err)
		return
	}
	if port != "" {
		query := url.Values{"token": {tok}}
		if cliState != "" {
			query.Set("state", cliState)
		}
		callbackURL := "http://127.0.0.1:" + port + "/callback?" + query.Encode()
		http.Redirect(w, r, callbackURL, http.StatusFound)
		return
	}
	h.setSession(w, tok)
	http.Redirect(w, r, "/", http.StatusFound)
}
