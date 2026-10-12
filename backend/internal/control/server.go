package control

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/store"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	DB            *store.Store
	Key           []byte
	Origins       map[string]bool
	SecureCookies bool
}

func New(db *store.Store, key []byte, origins []string, secure bool) (*Server, error) {
	if len(key) < 32 {
		return nil, errors.New("cursor signing key must be at least 32 bytes")
	}
	m := map[string]bool{}
	for _, o := range origins {
		if o != "" {
			m[o] = true
		}
	}
	if len(m) == 0 {
		return nil, errors.New("explicit trusted origins required")
	}
	return &Server{db, key, m, secure}, nil
}
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { write(w, 200, map[string]string{"status": "live"}) })
	m.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if s.DB.Pool.Ping(r.Context()) != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
		write(w, 200, map[string]string{"status": "ready"})
	})
	m.HandleFunc("POST /v1/auth/exchange", s.exchange)
	m.HandleFunc("/v1/", s.authenticated)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		origin := r.Header.Get("Origin")
		if origin != "" && !s.Origins[origin] {
			problem(w, 403, "ORIGIN_DENIED")
			return
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == "OPTIONS" {
			if origin == "" {
				problem(w, 403, "ORIGIN_REQUIRED")
				return
			}
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token, Idempotency-Key")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.WriteHeader(204)
			return
		}
		m.ServeHTTP(w, r)
	})
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"schema_version": "1", "code": code, "message": strings.ReplaceAll(code, "_", " "), "request_id": store.ID("req"), "retryable": status == 503, "violations": []string{}, "current_versions": []any{}})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return errors.New("trailing json")
	}
	return nil
}
func read(w http.ResponseWriter, v any, cursor string) {
	write(w, 200, map[string]any{"schema_version": "1", "data": v, "event_cursor": cursor})
}
func sessionData(v store.Session) map[string]any {
	return map[string]any{"user_id": v.UserID, "display_name": v.DisplayName, "csrf_token": v.CSRF, "expires_at": v.ExpiresAt}
}
func (s *Server) exchange(w http.ResponseWriter, r *http.Request) {
	if !s.Origins[r.Header.Get("Origin")] {
		problem(w, 403, "ORIGIN_REQUIRED")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if decode(w, r, &body) != nil || len(body.Code) > 256 {
		problem(w, 400, "INVALID_CODE")
		return
	}
	token, v, e := s.DB.Exchange(r.Context(), body.Code)
	if e != nil {
		problem(w, 401, "INVALID_CODE")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "lykeion_session", Value: token, Path: "/", HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: 28800})
	read(w, sessionData(v), "bootstrap")
}
func (s *Server) authenticated(w http.ResponseWriter, r *http.Request) {
	cookie, e := r.Cookie("lykeion_session")
	if e != nil {
		problem(w, 401, "AUTH_REQUIRED")
		return
	}
	session, e := s.DB.Authenticate(r.Context(), cookie.Value)
	if e != nil {
		problem(w, 401, "SESSION_EXPIRED")
		return
	}
	if r.Method != "GET" && (subtle.ConstantTimeCompare([]byte(session.CSRF), []byte(r.Header.Get("X-CSRF-Token"))) != 1 || !s.Origins[r.Header.Get("Origin")]) {
		problem(w, 403, "CSRF_DENIED")
		return
	}
	switch r.URL.Path {
	case "/v1/session":
		if r.Method != "GET" {
			problem(w, 405, "METHOD_NOT_ALLOWED")
			return
		}
		read(w, sessionData(session), "bootstrap")
		return
	case "/v1/capabilities":
		if r.Method != "GET" {
			problem(w, 405, "METHOD_NOT_ALLOWED")
			return
		}
		read(w, map[string]any{"api_version": "1", "workspace_protocol": "lykeion.workspace.v1", "worker_protocol": "lykeion.worker.v1", "supported_commands": []string{"preference.update"}, "supported_features": []string{"workspace.read", "membership.read", "events.replay", "workspace.websocket"}}, "bootstrap")
		return
	case "/v1/auth/logout":
		if r.Method != "POST" {
			problem(w, 405, "METHOD_NOT_ALLOWED")
			return
		}
		_, e = s.DB.Pool.Exec(r.Context(), "DELETE FROM sessions WHERE digest=$1", store.Hash(cookie.Value))
		if e != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "lykeion_session", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteStrictMode})
		w.WriteHeader(204)
		return
	case "/v1/workspaces":
		s.workspaces(w, r, session.UserID)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 || parts[0] != "v1" || parts[1] != "workspaces" {
		problem(w, 404, "NOT_FOUND")
		return
	}
	workspace := parts[2]
	role, epoch, seq, revision, e := s.DB.Access(r.Context(), workspace, session.UserID)
	if e != nil {
		problem(w, 404, "NOT_FOUND")
		return
	}
	cursor := s.signCursor(workspace, session.UserID, epoch, seq)
	if len(parts) == 5 && parts[3] == "commands" && r.Method == "POST" {
		s.command(w, r, session, workspace, role, epoch, parts[4])
		return
	}
	if len(parts) == 5 && parts[3] == "operations" && r.Method == "GET" {
		var raw []byte
		e = s.DB.Pool.QueryRow(r.Context(), "SELECT result FROM operations WHERE id=$1 AND workspace_id=$2 AND user_id=$3", parts[4], workspace, session.UserID).Scan(&raw)
		if e != nil {
			problem(w, 404, "NOT_FOUND")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
		return
	}
	if len(parts) != 4 || r.Method != "GET" {
		problem(w, 404, "NOT_FOUND")
		return
	}
	switch parts[3] {
	case "members":
		rows, e := s.DB.Pool.Query(r.Context(), "SELECT m.id,m.user_id,u.display_name,m.access_role,m.state,m.revision FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.workspace_id=$1 AND m.state='active' ORDER BY m.id", workspace)
		if e != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
		defer rows.Close()
		items := []any{}
		for rows.Next() {
			var id, user, name, access, state string
			var rev int64
			if e = rows.Scan(&id, &user, &name, &access, &state, &rev); e != nil {
				problem(w, 503, "STORE_UNAVAILABLE")
				return
			}
			items = append(items, map[string]any{"id": id, "user_id": user, "workspace_id": workspace, "display_name": name, "access_role": access, "state": state, "revision": strconv.FormatInt(rev, 10)})
		}
		if rows.Err() != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
		s.page(w, r, items, workspace, session.UserID, epoch, revision, cursor)
	case "programmes":
		rows, e := s.DB.Pool.Query(r.Context(), "SELECT id,name,objective,revision,profile_ref,policy_refs,budget_ceiling FROM programmes WHERE workspace_id=$1 ORDER BY id", workspace)
		if e != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
		defer rows.Close()
		items := []any{}
		for rows.Next() {
			var id, name, objective string
			var rev int64
			var profile, policies, budget json.RawMessage
			if rows.Scan(&id, &name, &objective, &rev, &profile, &policies, &budget) != nil {
				problem(w, 503, "STORE_UNAVAILABLE")
				return
			}
			items = append(items, map[string]any{"id": id, "workspace_id": workspace, "name": name, "objective": objective, "revision": formatRevision(rev), "profile_ref": profile, "policy_refs": policies, "budget_ceiling": budget})
		}
		if rows.Err() != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
		s.page(w, r, items, workspace, session.UserID, epoch, revision, cursor)
	case "events":
		s.replay(w, r, session.UserID, workspace, epoch, seq, revision)
	case "realtime":
		s.socket(w, r, cookie.Value, session.UserID, workspace, epoch)
	default:
		problem(w, 404, "NOT_FOUND")
	}
}
func (s *Server) workspaces(w http.ResponseWriter, r *http.Request, user string) {
	if r.Method != "GET" {
		problem(w, 405, "METHOD_NOT_ALLOWED")
		return
	}
	rows, e := s.DB.Pool.Query(r.Context(), "SELECT w.id,w.name,w.revision FROM workspaces w JOIN memberships m ON m.workspace_id=w.id WHERE m.user_id=$1 AND m.state='active' ORDER BY w.id", user)
	if e != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, name string
		var rev int64
		if rows.Scan(&id, &name, &rev) != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
		items = append(items, map[string]any{"id": id, "name": name, "revision": strconv.FormatInt(rev, 10)})
	}
	if rows.Err() != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	s.page(w, r, items, "account", user, 0, 1, "bootstrap")
}
func (s *Server) page(w http.ResponseWriter, r *http.Request, items []any, workspace, user string, epoch, revision int64, eventCursor string) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 100 {
			problem(w, 400, "INVALID_LIMIT")
			return
		}
		limit = n
	}
	offset := int64(0)
	if value := r.URL.Query().Get("cursor"); value != "" {
		n, e := s.parseCursor(value, workspace+":"+r.URL.Path, user, epoch+revision)
		if e != nil {
			problem(w, 409, "SNAPSHOT_EXPIRED")
			return
		}
		offset = n
	}
	if offset > int64(len(items)) {
		problem(w, 409, "SNAPSHOT_EXPIRED")
		return
	}
	end := min(int(offset)+limit, len(items))
	body := map[string]any{"schema_version": "1", "items": items[int(offset):end], "snapshot_revision": strconv.FormatInt(revision, 10), "event_cursor": eventCursor}
	if end < len(items) {
		body["next_cursor"] = s.signCursor(workspace+":"+r.URL.Path, user, epoch+revision, int64(end))
	}
	write(w, 200, body)
}

type ref struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Revision string `json:"revision"`
	SHA256   string `json:"sha256,omitempty"`
}
type envelope struct {
	SchemaVersion string `json:"schema_version"`
	Target        struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	} `json:"target"`
	Expected []ref           `json:"expected_versions"`
	Payload  json.RawMessage `json:"payload"`
}

func (s *Server) command(w http.ResponseWriter, r *http.Request, session store.Session, workspace, role string, epoch int64, name string) {
	if name != "preference.update" {
		problem(w, 422, "COMMAND_NOT_IMPLEMENTED")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" || len(key) > 128 {
		problem(w, 400, "IDEMPOTENCY_KEY_REQUIRED")
		return
	}
	var env envelope
	if decode(w, r, &env) != nil || env.SchemaVersion != "1" || env.Target.Kind != "workspace" || env.Target.ID != workspace {
		problem(w, 400, "INVALID_COMMAND")
		return
	}
	var payload struct {
		Kind  string          `json:"preference_kind"`
		Value json.RawMessage `json:"value"`
	}
	d := json.NewDecoder(bytes.NewReader(env.Payload))
	d.DisallowUnknownFields()
	if d.Decode(&payload) != nil || !validPreference(payload.Kind, payload.Value) {
		problem(w, 422, "INVALID_PREFERENCE")
		return
	}
	raw, _ := json.Marshal(env)
	var canonical any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.Decode(&canonical)
	raw, _ = json.Marshal(canonical)
	hash := store.Hash(string(raw))
	tx, e := s.DB.Pool.Begin(r.Context())
	if e != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	defer tx.Rollback(r.Context())
	var revision, sequence, currentEpoch int64
	e = tx.QueryRow(r.Context(), "SELECT revision,event_sequence,auth_epoch FROM workspaces WHERE id=$1 FOR UPDATE", workspace).Scan(&revision, &sequence, &currentEpoch)
	if e != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	var active bool
	e = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM memberships WHERE workspace_id=$1 AND user_id=$2 AND state='active')", workspace, session.UserID).Scan(&active)
	if e != nil || !active {
		problem(w, 404, "NOT_FOUND")
		return
	}
	var priorHash string
	var prior []byte
	e = tx.QueryRow(r.Context(), "SELECT request_hash,result FROM operations WHERE workspace_id=$1 AND user_id=$2 AND command_name=$3 AND idempotency_key=$4", workspace, session.UserID, name, key).Scan(&priorHash, &prior)
	if e == nil {
		if priorHash != hash {
			problem(w, 409, "IDEMPOTENCY_MISMATCH")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		w.Write(prior)
		return
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	match := false
	for _, v := range env.Expected {
		if v.Kind == "workspace" && v.ID == workspace && v.Revision == strconv.FormatInt(revision, 10) {
			match = true
		}
	}
	if !match {
		problem(w, 409, "REVISION_CONFLICT")
		return
	}
	if _, e = tx.Exec(r.Context(), "INSERT INTO preferences VALUES($1,$2,$3,$4) ON CONFLICT(workspace_id,user_id,kind) DO UPDATE SET value=excluded.value", workspace, session.UserID, payload.Kind, payload.Value); e != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	sequence++
	op := store.ID("op")
	cursor := s.signCursor(workspace, session.UserID, currentEpoch, sequence)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result := map[string]any{"schema_version": "1", "id": op, "command_name": name, "state": "succeeded", "target_ref": env.Target, "accepted_at": now, "result_refs": []any{}, "event_cursor": cursor}
	resultRaw, _ := json.Marshal(result)
	event := map[string]any{"id": store.ID("evt"), "type": "preference.changed", "subject_ref": ref{Kind: "workspace", ID: workspace, Revision: strconv.FormatInt(revision, 10)}, "revision": strconv.FormatInt(revision, 10), "recorded_at": now, "operation_id": op, "causation_id": op, "correlation_id": op, "attribution_ref": ref{Kind: "operation", ID: op, Revision: "1"}, "payload": map[string]any{"changed_fields": []string{payload.Kind}}}
	eventRaw, _ := json.Marshal(event)
	for _, q := range []struct {
		sql  string
		args []any
	}{{"UPDATE workspaces SET revision=$2,event_sequence=$3 WHERE id=$1", []any{workspace, revision, sequence}}, {"INSERT INTO operations(id,workspace_id,user_id,command_name,idempotency_key,request_hash,result) VALUES($1,$2,$3,$4,$5,$6,$7)", []any{op, workspace, session.UserID, name, key, hash, resultRaw}}, {"INSERT INTO events VALUES($1,$2,$3,$4)", []any{workspace, sequence, session.UserID, eventRaw}}} {
		if _, e = tx.Exec(r.Context(), q.sql, q.args...); e != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
	}
	if e = tx.Commit(r.Context()); e != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	write(w, 202, result)
}
func formatRevision(n int64) string { return strconv.FormatInt(n, 10) }
func validPreference(kind string, raw json.RawMessage) bool {
	switch kind {
	case "theme":
		var v string
		return json.Unmarshal(raw, &v) == nil && (v == "light" || v == "dark" || v == "system")
	case "sidebar":
		var v bool
		return json.Unmarshal(raw, &v) == nil
	case "pane_width":
		var v int
		return json.Unmarshal(raw, &v) == nil && v >= 240 && v <= 960
	}
	return false
}
func (s *Server) events(ctx context.Context, user, workspace string, after, through int64) ([]json.RawMessage, error) {
	rows, e := s.DB.Pool.Query(ctx, "SELECT payload FROM events WHERE workspace_id=$1 AND sequence>$2 AND sequence<=$3 AND (user_id IS NULL OR user_id=$4) ORDER BY sequence", workspace, after, through, user)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		items = append(items, raw)
	}
	return items, rows.Err()
}
func (s *Server) replay(w http.ResponseWriter, r *http.Request, user, workspace string, epoch, seq, revision int64) {
	after := int64(0)
	value := r.URL.Query().Get("after")
	if value == "" {
		value = r.URL.Query().Get("cursor")
	}
	if value != "" {
		var e error
		after, e = s.parseCursor(value, workspace, user, epoch)
		if e != nil {
			problem(w, 409, "SNAPSHOT_EXPIRED")
			return
		}
	}
	if after > seq {
		problem(w, 409, "SNAPSHOT_EXPIRED")
		return
	}
	through := min(after+100, seq)
	items, e := s.events(r.Context(), user, workspace, after, through)
	if e != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	body := map[string]any{"schema_version": "1", "items": items, "snapshot_revision": strconv.FormatInt(revision, 10), "event_cursor": s.signCursor(workspace, user, epoch, through)}
	if through < seq {
		body["next_cursor"] = s.signCursor(workspace, user, epoch, through)
	}
	write(w, 200, body)
}
