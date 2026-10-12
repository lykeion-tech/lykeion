package control

import "net/http"

// Installation codes are short-lived, single-use authorizations. Device login
// creates a dedicated revocable CLI session, never a reusable browser cookie.
func (s *Server) exchangeDevice(w http.ResponseWriter, r *http.Request) {
	if !s.allowRequest(w, r, "login:"+peerKey(r), 12) {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if decode(w, r, &body) != nil || len(body.Code) > 256 || body.Code == "" {
		problem(w, 400, "INVALID_CODE")
		return
	}
	token, session, err := s.DB.ExchangeFor(r.Context(), body.Code, "cli")
	if err != nil {
		problem(w, 401, "INVALID_CODE")
		return
	}
	write(w, 200, map[string]any{"schema_version": "1", "data": map[string]any{"credential": token, "session": sessionData(session)}, "event_cursor": "bootstrap"})
}
