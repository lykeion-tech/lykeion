package control

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/store"
)

func membershipCommand(name string) bool {
	switch name {
	case "member.invite", "invitation.revoke", "member.remove", "member.leave":
		return true
	}
	return false
}
func (s *Server) invitationProof(id string) string {
	h := hmac.New(sha256.New, s.Key)
	h.Write([]byte("lykeion:invitation:v1:" + id))
	return hex.EncodeToString(h.Sum(nil))
}
func expected(env envelope, kind, id string, revision int64) bool {
	for _, v := range env.Expected {
		if v.Kind == kind && v.ID == id && v.Revision == formatRevision(revision) {
			return true
		}
	}
	return false
}
func commandHash(env any) string {
	raw, _ := json.Marshal(env)
	var v any
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	d.Decode(&v)
	raw, _ = json.Marshal(v)
	return store.Hash(string(raw))
}

// The workspace lock serializes authorization, expected versions, effects and event publication.
func (s *Server) membership(w http.ResponseWriter, r *http.Request, session store.Session, workspace, name string) {
	key := r.Header.Get("Idempotency-Key")
	var env envelope
	if key == "" || len(key) > 128 || decode(w, r, &env) != nil || env.SchemaVersion != "1" || env.Target.Kind != "workspace" || env.Target.ID != workspace {
		problem(w, 400, "INVALID_COMMAND")
		return
	}
	ctx := r.Context()
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	defer tx.Rollback(ctx)
	var rev, seq, epoch int64
	if tx.QueryRow(ctx, "SELECT revision,event_sequence,auth_epoch FROM workspaces WHERE id=$1 FOR UPDATE", workspace).Scan(&rev, &seq, &epoch) != nil {
		problem(w, 404, "NOT_FOUND")
		return
	}
	var role, ownID string
	if name == "member.leave" && replayOperation(w, r, tx, workspace, session.UserID, name, key, commandHash(env)) {
		return
	}
	if tx.QueryRow(ctx, "SELECT access_role,id FROM memberships WHERE workspace_id=$1 AND user_id=$2 AND state='active'", workspace, session.UserID).Scan(&role, &ownID) != nil {
		problem(w, 404, "NOT_FOUND")
		return
	}
	if name != "member.leave" && role != "owner" && role != "admin" {
		problem(w, 403, "ACCESS_DENIED")
		return
	}
	hash := commandHash(env)
	if replayOperation(w, r, tx, workspace, session.UserID, name, key, hash) {
		return
	}
	if !expected(env, "workspace", workspace, rev) {
		problem(w, 409, "REVISION_CONFLICT")
		return
	}
	resultRefs := []ref{}
	changed := []string{}
	var audience any
	switch name {
	case "member.invite":
		var p struct {
			Destination string    `json:"destination"`
			Role        string    `json:"requested_role"`
			Expires     time.Time `json:"expires_at"`
		}
		if strictFrame(env.Payload, &p) != nil || !frameID.MatchString(p.Destination) || (p.Role != "admin" && p.Role != "member" && p.Role != "viewer") || !p.Expires.After(time.Now()) || p.Expires.After(time.Now().Add(30*24*time.Hour)) {
			problem(w, 422, "INVALID_INVITATION")
			return
		}
		if p.Role == "admin" && role != "owner" {
			problem(w, 403, "OWNER_REQUIRED")
			return
		}
		var eligible bool
		err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE id=$1) AND NOT EXISTS(SELECT 1 FROM memberships WHERE workspace_id=$2 AND user_id=$1 AND state='active')", p.Destination, workspace).Scan(&eligible)
		if err != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
		if !eligible {
			problem(w, 422, "RECIPIENT_UNAVAILABLE")
			return
		}
		// Expiry is checked under the same lock; expired proofs can never be revived.
		_, err = tx.Exec(ctx, "UPDATE invitations SET state='revoked',revision=revision+1 WHERE workspace_id=$1 AND recipient_id=$2 AND state='pending' AND expires_at<=clock_timestamp()", workspace, p.Destination)
		if err != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
		var pending bool
		if tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM invitations WHERE workspace_id=$1 AND recipient_id=$2 AND state='pending')", workspace, p.Destination).Scan(&pending) != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
		if pending {
			problem(w, 409, "INVITATION_PENDING")
			return
		}
		id := store.ID("inv")
		_, err = tx.Exec(ctx, "INSERT INTO invitations(id,workspace_id,recipient_id,created_by,requested_role,proof_digest,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)", id, workspace, p.Destination, session.UserID, p.Role, store.Hash(s.invitationProof(id)), p.Expires)
		resultRefs = append(resultRefs, ref{Kind: "invitation", ID: id, Revision: "1"})
		changed = []string{"invitations"}
		audience = session.UserID
	case "invitation.revoke":
		var p struct {
			ID     string `json:"invitation_id"`
			Reason string `json:"reason"`
		}
		if strictFrame(env.Payload, &p) != nil || strings.TrimSpace(p.Reason) == "" || len(p.Reason) > 10000 {
			problem(w, 422, "INVALID_REASON")
			return
		}
		var v int64
		var state string
		if tx.QueryRow(ctx, "SELECT revision,state FROM invitations WHERE id=$1 AND workspace_id=$2", p.ID, workspace).Scan(&v, &state) != nil {
			problem(w, 404, "NOT_FOUND")
			return
		}
		if !expected(env, "invitation", p.ID, v) {
			problem(w, 409, "REVISION_CONFLICT")
			return
		}
		if state != "pending" {
			problem(w, 409, "INVITATION_CLOSED")
			return
		}
		_, err = tx.Exec(ctx, "UPDATE invitations SET state='revoked',revision=revision+1 WHERE id=$1", p.ID)
		resultRefs = append(resultRefs, ref{Kind: "invitation", ID: p.ID, Revision: formatRevision(v + 1)})
		changed = []string{"invitations"}
		audience = session.UserID
	case "member.remove", "member.leave":
		var p struct {
			ID      string `json:"member_id"`
			Reason  string `json:"reason"`
			Handoff *ref   `json:"ownership_handoff_ref,omitempty"`
		}
		if strictFrame(env.Payload, &p) != nil {
			problem(w, 422, "INVALID_MEMBER")
			return
		}
		if name == "member.leave" {
			if p.ID != "" || p.Reason != "" {
				problem(w, 422, "INVALID_MEMBER")
				return
			}
			p.ID = ownID
		} else if strings.TrimSpace(p.Reason) == "" || len(p.Reason) > 10000 {
			problem(w, 422, "INVALID_REASON")
			return
		}
		// Handoff verification belongs to the work-ownership slice; never accept an arbitrary pointer.
		if p.Handoff != nil {
			problem(w, 422, "HANDOFF_NOT_IMPLEMENTED")
			return
		}
		var memberRole, user string
		var v int64
		if tx.QueryRow(ctx, "SELECT access_role,user_id,revision FROM memberships WHERE id=$1 AND workspace_id=$2 AND state='active'", p.ID, workspace).Scan(&memberRole, &user, &v) != nil {
			problem(w, 404, "NOT_FOUND")
			return
		}
		if memberRole == "owner" {
			problem(w, 409, "OWNER_TRANSFER_REQUIRED")
			return
		}
		if name == "member.remove" && memberRole == "admin" && role != "owner" {
			problem(w, 403, "OWNER_REQUIRED")
			return
		}
		if !expected(env, "membership", p.ID, v) {
			problem(w, 409, "REVISION_CONFLICT")
			return
		}
		_, err = tx.Exec(ctx, "UPDATE memberships SET state='removed',revision=revision+1 WHERE id=$1", p.ID)
		if err == nil {
			_, err = tx.Exec(ctx, "UPDATE users SET account_revision=account_revision+1 WHERE id=$1", user)
		}
		if err == nil {
			_, err = tx.Exec(ctx, "UPDATE programme_grants g SET capabilities='[]',revision=g.revision+1 FROM programmes p WHERE g.programme_id=p.id AND p.workspace_id=$1 AND g.user_id=$2", workspace, user)
		}
		epoch++
		resultRefs = append(resultRefs, ref{Kind: "membership", ID: p.ID, Revision: formatRevision(v + 1)})
		changed = []string{"members"}
	}
	if err != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	s.finishCommand(w, r, tx, workspace, session.UserID, name, key, hash, env.Target, rev+1, seq+1, epoch, resultRefs, changed, audience)
}
func replayOperation(w http.ResponseWriter, r *http.Request, tx pgx.Tx, workspace, user, name, key, hash string) bool {
	var old string
	var raw []byte
	err := tx.QueryRow(r.Context(), "SELECT request_hash,result FROM operations WHERE workspace_id=$1 AND user_id=$2 AND command_name=$3 AND idempotency_key=$4", workspace, user, name, key).Scan(&old, &raw)
	if err == nil {
		if old != hash {
			problem(w, 409, "IDEMPOTENCY_MISMATCH")
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(202)
			w.Write(raw)
		}
		return true
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		problem(w, 503, "STORE_UNAVAILABLE")
		return true
	}
	return false
}
func (s *Server) finishCommand(w http.ResponseWriter, r *http.Request, tx pgx.Tx, workspace, user, name, key, hash string, target any, revision, sequence, epoch int64, refs []ref, changed []string, audience any) {
	op := store.ID("op")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result := map[string]any{"schema_version": "1", "id": op, "command_name": name, "state": "succeeded", "target_ref": target, "accepted_at": now, "result_refs": refs, "event_cursor": s.signCursor(workspace, user, epoch, sequence)}
	resultRaw, _ := json.Marshal(result)
	event := map[string]any{"id": store.ID("evt"), "type": name + ".completed", "subject_ref": ref{Kind: "workspace", ID: workspace, Revision: formatRevision(revision)}, "revision": formatRevision(revision), "recorded_at": now, "operation_id": op, "causation_id": op, "correlation_id": op, "attribution_ref": ref{Kind: "operation", ID: op, Revision: "1"}, "payload": map[string]any{"changed_fields": changed}}
	eventRaw, _ := json.Marshal(event)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"UPDATE workspaces SET revision=$2,event_sequence=$3,auth_epoch=$4 WHERE id=$1", []any{workspace, revision, sequence, epoch}},
		{"INSERT INTO operations(id,workspace_id,user_id,command_name,idempotency_key,request_hash,result) VALUES($1,$2,$3,$4,$5,$6,$7)", []any{op, workspace, user, name, key, hash, resultRaw}},
		{"INSERT INTO events VALUES($1,$2,$3,$4)", []any{workspace, sequence, audience, eventRaw}},
	} {
		if _, err := tx.Exec(r.Context(), q.sql, q.args...); err != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
	}
	if tx.Commit(r.Context()) != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	write(w, 202, result)
}

func (s *Server) acceptInvitation(w http.ResponseWriter, r *http.Request, session store.Session, id string) {
	var body struct {
		Proof    string `json:"proof"`
		Revision string `json:"revision"`
	}
	key := r.Header.Get("Idempotency-Key")
	if r.Method != "POST" || key == "" || len(key) > 128 || decode(w, r, &body) != nil || len(body.Proof) > 256 {
		problem(w, 400, "INVALID_INVITATION")
		return
	}
	ctx := r.Context()
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	defer tx.Rollback(ctx)
	var workspace string
	// Recipient scope is checked before workspace metadata is ever read or returned.
	if tx.QueryRow(ctx, "SELECT workspace_id FROM invitations WHERE id=$1 AND recipient_id=$2 AND proof_digest=$3", id, session.UserID, store.Hash(body.Proof)).Scan(&workspace) != nil {
		problem(w, 404, "NOT_FOUND")
		return
	}
	var rev, seq, epoch int64
	if tx.QueryRow(ctx, "SELECT revision,event_sequence,auth_epoch FROM workspaces WHERE id=$1 FOR UPDATE", workspace).Scan(&rev, &seq, &epoch) != nil {
		problem(w, 404, "NOT_FOUND")
		return
	}
	hash := commandHash(map[string]any{"invitation_id": id, "body": body})
	if replayOperation(w, r, tx, workspace, session.UserID, "invitation.accept", key, hash) {
		return
	}
	var state, role string
	var valid bool
	var v int64
	if tx.QueryRow(ctx, "SELECT state,requested_role,expires_at>clock_timestamp(),revision FROM invitations WHERE id=$1", id).Scan(&state, &role, &valid, &v) != nil {
		problem(w, 404, "NOT_FOUND")
		return
	}
	if state != "pending" || !valid || body.Revision != formatRevision(v) {
		problem(w, 409, "INVITATION_UNAVAILABLE")
		return
	}
	var active bool
	if tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM memberships WHERE workspace_id=$1 AND user_id=$2 AND state='active')", workspace, session.UserID).Scan(&active) != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	if active {
		problem(w, 409, "ALREADY_MEMBER")
		return
	}
	member := store.ID("mem")
	var memberRev int64
	err = tx.QueryRow(ctx, "INSERT INTO memberships(id,workspace_id,user_id,access_role) VALUES($1,$2,$3,$4) ON CONFLICT(workspace_id,user_id) DO UPDATE SET state='active',access_role=excluded.access_role,revision=memberships.revision+1 RETURNING id,revision", member, workspace, session.UserID, role).Scan(&member, &memberRev)
	if err == nil {
		_, err = tx.Exec(ctx, "UPDATE invitations SET state='accepted',revision=revision+1 WHERE id=$1", id)
	}
	if err == nil {
		_, err = tx.Exec(ctx, "UPDATE users SET account_revision=account_revision+1 WHERE id=$1", session.UserID)
	}
	if err != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	s.finishCommand(w, r, tx, workspace, session.UserID, "invitation.accept", key, hash, map[string]string{"kind": "invitation", "id": id}, rev+1, seq+1, epoch+1, []ref{{Kind: "membership", ID: member, Revision: formatRevision(memberRev)}}, []string{"members"}, nil)
}
func (s *Server) invitations(w http.ResponseWriter, r *http.Request, user, workspace, role string, epoch, revision int64, cursor string) {
	if role != "owner" && role != "admin" {
		problem(w, 403, "ACCESS_DENIED")
		return
	}
	rows, err := s.DB.Pool.Query(r.Context(), "SELECT id,recipient_id,requested_role,CASE WHEN state='pending' AND expires_at<=clock_timestamp() THEN 'expired' ELSE state END,to_char(expires_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'),revision FROM invitations WHERE workspace_id=$1 ORDER BY id", workspace)
	if err != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, recipient, access, state, expiry string
		var rev int64
		if rows.Scan(&id, &recipient, &access, &state, &expiry, &rev) != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
		items = append(items, map[string]any{"id": id, "workspace_id": workspace, "recipient": recipient, "requested_role": access, "state": state, "expires_at": expiry, "revision": formatRevision(rev)})
	}
	if rows.Err() != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	s.page(w, r, items, workspace, user, epoch, revision, cursor)
}
func (s *Server) proof(w http.ResponseWriter, r *http.Request, user, workspace, id, role string) {
	if role != "owner" && role != "admin" {
		problem(w, 404, "NOT_FOUND")
		return
	}
	var found bool
	err := s.DB.Pool.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM invitations WHERE id=$1 AND workspace_id=$2 AND created_by=$3 AND state='pending' AND expires_at>clock_timestamp())", id, workspace, user).Scan(&found)
	if err != nil || !found {
		problem(w, 404, "NOT_FOUND")
		return
	}
	read(w, map[string]string{"invitation_id": id, "proof": s.invitationProof(id)}, "bootstrap")
}
