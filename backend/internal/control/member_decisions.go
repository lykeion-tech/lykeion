package control

import (
	"encoding/json"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/store"
	"net/http"
	"time"
)

func decisionCommand(name string) bool {
	return name == "member.request-access-change" || name == "workspace.request-owner-transfer" || name == "decision.respond"
}
func (s *Server) memberDecision(w http.ResponseWriter, r *http.Request, session store.Session, workspace, name string) {
	var env envelope
	key := r.Header.Get("Idempotency-Key")
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
	var revision, sequence, epoch int64
	if tx.QueryRow(ctx, "SELECT revision,event_sequence,auth_epoch FROM workspaces WHERE id=$1 FOR UPDATE", workspace).Scan(&revision, &sequence, &epoch) != nil {
		problem(w, 404, "NOT_FOUND")
		return
	}
	var role string
	if tx.QueryRow(ctx, "SELECT access_role FROM memberships WHERE workspace_id=$1 AND user_id=$2 AND state='active'", workspace, session.UserID).Scan(&role) != nil {
		problem(w, 404, "NOT_FOUND")
		return
	}
	hash := commandHash(env)
	if replayOperation(w, r, tx, workspace, session.UserID, name, key, hash) {
		return
	}
	if !expected(env, "workspace", workspace, revision) {
		problem(w, 409, "REVISION_CONFLICT")
		return
	}
	refs := []ref{}
	changed := []string{"membership_decisions"}
	var audience any = session.UserID
	if name != "decision.respond" {
		var p struct {
			MemberID    string    `json:"member_id"`
			RecipientID string    `json:"recipient_member_id"`
			Role        string    `json:"requested_access"`
			Expires     time.Time `json:"expires_at"`
		}
		if strictFrame(env.Payload, &p) != nil || !p.Expires.After(time.Now()) || p.Expires.After(time.Now().Add(30*24*time.Hour)) {
			problem(w, 422, "INVALID_DECISION")
			return
		}
		action := "change-access"
		if name == "workspace.request-owner-transfer" {
			if p.MemberID != "" || p.Role != "" || role != "owner" {
				problem(w, 403, "OWNER_REQUIRED")
				return
			}
			p.MemberID = p.RecipientID
			p.Role = "owner"
			action = "transfer-owner"
		} else if p.RecipientID != "" || (p.Role != "admin" && p.Role != "member" && p.Role != "viewer") || (role != "owner" && role != "admin") {
			problem(w, 403, "ACCESS_DENIED")
			return
		}
		var user, memberRole string
		var v int64
		if tx.QueryRow(ctx, "SELECT user_id,access_role,revision FROM memberships WHERE id=$1 AND workspace_id=$2 AND state='active'", p.MemberID, workspace).Scan(&user, &memberRole, &v) != nil {
			problem(w, 404, "NOT_FOUND")
			return
		}
		if memberRole == "owner" || (role != "owner" && (memberRole == "admin" || p.Role == "admin")) {
			problem(w, 403, "OWNER_REQUIRED")
			return
		}
		if !expected(env, "membership", p.MemberID, v) {
			problem(w, 409, "REVISION_CONFLICT")
			return
		}
		responder := session.UserID
		if action == "transfer-owner" {
			responder = user
		}
		id := store.ID("dec")
		_, err = tx.Exec(ctx, "INSERT INTO membership_decisions(id,workspace_id,requester_id,responder_id,member_id,member_revision,workspace_revision,action,requested_role,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)", id, workspace, session.UserID, responder, p.MemberID, v, revision+1, action, p.Role, p.Expires)
		if err != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
		refs = append(refs, ref{Kind: "decision", ID: id, Revision: "1"})
		audience = responder
	} else {
		var p struct {
			ID       string `json:"decision_id"`
			Revision string `json:"decision_revision"`
			Choice   string `json:"choice_id"`
			Subjects []ref  `json:"subject_versions"`
		}
		if strictFrame(env.Payload, &p) != nil || (p.Choice != "approve" && p.Choice != "reject") {
			problem(w, 422, "INVALID_DECISION")
			return
		}
		var requester, responder, member, action, requestedRole, state string
		var v, memberRev, workspaceRev int64
		var valid bool
		err = tx.QueryRow(ctx, "SELECT requester_id,responder_id,member_id,member_revision,workspace_revision,action,requested_role,state,revision,expires_at>clock_timestamp() FROM membership_decisions WHERE id=$1 AND workspace_id=$2", p.ID, workspace).Scan(&requester, &responder, &member, &memberRev, &workspaceRev, &action, &requestedRole, &state, &v, &valid)
		if err != nil || responder != session.UserID {
			problem(w, 404, "NOT_FOUND")
			return
		}
		if state != "pending" || !valid || p.Revision != formatRevision(v) {
			problem(w, 409, "DECISION_UNAVAILABLE")
			return
		}
		if p.Choice == "approve" {
			if revision != workspaceRev || len(p.Subjects) != 2 || !expected(envelope{Expected: p.Subjects}, "workspace", workspace, workspaceRev) || !expected(envelope{Expected: p.Subjects}, "membership", member, memberRev) {
				problem(w, 409, "SUBJECT_CHANGED")
				return
			}
			var memberRole, user, requesterRole string
			var current int64
			if tx.QueryRow(ctx, "SELECT access_role,user_id,revision FROM memberships WHERE id=$1 AND workspace_id=$2 AND state='active'", member, workspace).Scan(&memberRole, &user, &current) != nil || current != memberRev {
				problem(w, 409, "SUBJECT_CHANGED")
				return
			}
			if tx.QueryRow(ctx, "SELECT access_role FROM memberships WHERE workspace_id=$1 AND user_id=$2 AND state='active'", workspace, requester).Scan(&requesterRole) != nil {
				problem(w, 409, "AUTHORITY_CHANGED")
				return
			}
			if action == "transfer-owner" {
				if requesterRole != "owner" || user != session.UserID || memberRole == "owner" {
					problem(w, 409, "AUTHORITY_CHANGED")
					return
				}
				_, err = tx.Exec(ctx, "UPDATE memberships SET access_role='admin',revision=revision+1 WHERE workspace_id=$1 AND user_id=$2", workspace, requester)
			} else if requester != session.UserID || memberRole == "owner" || (requesterRole != "owner" && (requesterRole != "admin" || memberRole == "admin" || requestedRole == "admin")) {
				problem(w, 409, "AUTHORITY_CHANGED")
				return
			}
			if err == nil {
				_, err = tx.Exec(ctx, "UPDATE memberships SET access_role=$2,revision=revision+1 WHERE id=$1", member, requestedRole)
			}
			if err == nil && requestedRole == "viewer" {
				_, err = tx.Exec(ctx, "UPDATE programme_grants g SET capabilities=CASE WHEN g.capabilities ? 'read' THEN '[\"read\"]'::jsonb ELSE '[]'::jsonb END,revision=g.revision+1 FROM programmes p WHERE g.programme_id=p.id AND p.workspace_id=$1 AND g.user_id=$2", workspace, user)
			}
			if err != nil {
				problem(w, 503, "STORE_UNAVAILABLE")
				return
			}
			epoch++
			refs = append(refs, ref{Kind: "membership", ID: member, Revision: formatRevision(memberRev + 1)})
			changed = append(changed, "members")
			audience = nil
		}
		response, _ := json.Marshal(map[string]any{"responder_id": session.UserID, "choice_id": p.Choice, "subject_versions": p.Subjects, "recorded_at": time.Now().UTC()})
		state = "rejected"
		if p.Choice == "approve" {
			state = "approved"
		}
		_, err = tx.Exec(ctx, "UPDATE membership_decisions SET state=$2,revision=revision+1,response=$3 WHERE id=$1", p.ID, state, response)
		if err != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
		refs = append(refs, ref{Kind: "decision", ID: p.ID, Revision: formatRevision(v + 1)})
	}
	s.finishCommand(w, r, tx, workspace, session.UserID, name, key, hash, env.Target, revision+1, sequence+1, epoch, refs, changed, audience)
}
func (s *Server) decisions(w http.ResponseWriter, r *http.Request, user, workspace string, epoch, revision int64, cursor string) {
	rows, err := s.DB.Pool.Query(r.Context(), "SELECT id,requester_id,responder_id,member_id,member_revision,workspace_revision,action,requested_role,CASE WHEN state='pending' AND expires_at<=clock_timestamp() THEN 'expired' ELSE state END,revision,to_char(expires_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"') FROM membership_decisions WHERE workspace_id=$1 AND (requester_id=$2 OR responder_id=$2) ORDER BY id", workspace, user)
	if err != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, requester, responder, member, action, role, state, expires string
		var mr, wr, rev int64
		if rows.Scan(&id, &requester, &responder, &member, &mr, &wr, &action, &role, &state, &rev, &expires) != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
		items = append(items, map[string]any{"id": id, "workspace_id": workspace, "requester_id": requester, "responder_id": responder, "member_id": member, "action": action, "requested_role": role, "state": state, "revision": formatRevision(rev), "expires_at": expires, "subject_versions": []ref{{Kind: "workspace", ID: workspace, Revision: formatRevision(wr)}, {Kind: "membership", ID: member, Revision: formatRevision(mr)}}})
	}
	if rows.Err() != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	s.page(w, r, items, workspace, user, epoch, revision, cursor)
}
