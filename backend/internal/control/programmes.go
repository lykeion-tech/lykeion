package control

import (
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/artifacts"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/policy"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/store"
	"net/http"
	"strconv"
	"strings"
)

type configManifest struct {
	Files []struct {
		Path   string `json:"path"`
		Upload ref    `json:"upload_ref"`
	} `json:"files"`
}
type grantChange struct {
	User         string   `json:"user_id"`
	Capabilities []string `json:"capabilities"`
}
type grantPlan struct {
	Subject ref           `json:"subject_ref"`
	Changes []grantChange `json:"changes"`
}
type budgetCeiling struct {
	Currency string `json:"currency"`
	Model    string `json:"model_microunits"`
	CPU      string `json:"cpu_milliseconds"`
	GPU      string `json:"gpu_milliseconds"`
	Elapsed  string `json:"elapsed_milliseconds"`
}

func validBudget(b budgetCeiling) bool {
	if len(b.Currency) != 3 || strings.Trim(b.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return false
	}
	for _, v := range []string{b.Model, b.CPU, b.GPU, b.Elapsed} {
		n, e := strconv.ParseUint(v, 10, 63)
		if e != nil || strconv.FormatUint(n, 10) != v {
			return false
		}
	}
	return true
}
func validConfig(kind string, data []byte) bool {
	switch kind {
	case "domain-profile":
		var p struct {
			Name        string `json:"name"`
			Domain      string `json:"domain"`
			Description string `json:"description"`
		}
		return strictFrame(data, &p) == nil && strings.TrimSpace(p.Name) != "" && len(p.Name) <= 120 && p.Domain == "computational" && strings.TrimSpace(p.Description) != ""
	case "gate-policy":
		var p policy.Policy
		if strictFrame(data, &p) != nil || len(p.Required) == 0 || len(p.Rules) == 0 {
			return false
		}
		_, err := policy.Evaluate(p, policy.Input{ManifestHash: strings.Repeat("0", 64)})
		return err == nil
	case "grant-changes":
		var p grantPlan
		if strictFrame(data, &p) != nil || p.Subject.Kind != "programme" || !frameID.MatchString(p.Subject.ID) || p.Subject.Revision == "" || len(p.Changes) == 0 || len(p.Changes) > 100 {
			return false
		}
		seen := map[string]bool{}
		for _, c := range p.Changes {
			if seen[c.User] || !frameID.MatchString(c.User) {
				return false
			}
			seen[c.User] = true
			caps := map[string]bool{}
			for _, cap := range c.Capabilities {
				if caps[cap] || (cap != "read" && cap != "comment" && cap != "edit" && cap != "manage-access") {
					return false
				}
				caps[cap] = true
			}
			if len(caps) > 0 && !caps["read"] {
				return false
			}
		}
		return true
	}
	return false
}
func (s *Server) configBytes(r *http.Request, tx pgx.Tx, workspace, user string, p ref, kind string) ([]byte, error) {
	var manifest configManifest
	var raw []byte
	var hash string
	err := tx.QueryRow(r.Context(), "SELECT manifest,sha256 FROM artifacts WHERE id=$1 AND workspace_id=$2 AND author_id=$3 AND authored_kind=$4", p.ID, workspace, user, kind).Scan(&raw, &hash)
	if err != nil {
		return nil, err
	}
	if p.Kind != "artifact" || p.Revision != "1" || p.SHA256 != hash || artifacts.Hash(raw) != hash {
		return nil, pgx.ErrNoRows
	}
	if json.Unmarshal(raw, &manifest) != nil || len(manifest.Files) != 1 {
		return nil, pgx.ErrNoRows
	}
	return artifacts.Read(s.ArtifactRoot, manifest.Files[0].Upload.SHA256)
}
func (s *Server) programmeCommand(w http.ResponseWriter, r *http.Request, session store.Session, workspace, name string) {
	key := r.Header.Get("Idempotency-Key")
	var env envelope
	if key == "" || len(key) > 128 || decode(w, r, &env) != nil || env.SchemaVersion != "1" || env.Target.Kind != "workspace" || env.Target.ID != workspace {
		problem(w, 400, "INVALID_COMMAND")
		return
	}
	tx, err := s.DB.Pool.Begin(r.Context())
	if err != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	defer tx.Rollback(r.Context())
	var revision, sequence, epoch int64
	if tx.QueryRow(r.Context(), "SELECT revision,event_sequence,auth_epoch FROM workspaces WHERE id=$1 FOR UPDATE", workspace).Scan(&revision, &sequence, &epoch) != nil {
		problem(w, 404, "NOT_FOUND")
		return
	}
	var role string
	if tx.QueryRow(r.Context(), "SELECT access_role FROM memberships WHERE workspace_id=$1 AND user_id=$2 AND state='active'", workspace, session.UserID).Scan(&role) != nil {
		problem(w, 404, "NOT_FOUND")
		return
	}
	if role != "owner" {
		problem(w, 403, "OWNER_REQUIRED")
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
	changed := []string{}
	switch name {
	case "artifact.submit":
		var p struct {
			Receipts []ref          `json:"upload_receipt_refs"`
			Manifest configManifest `json:"manifest_candidate"`
			Kind     string         `json:"authored_kind"`
		}
		if strictFrame(env.Payload, &p) != nil || len(p.Receipts) != 1 || len(p.Manifest.Files) != 1 || p.Manifest.Files[0].Path != "configuration.json" || p.Receipts[0] != p.Manifest.Files[0].Upload {
			problem(w, 422, "INVALID_CONFIG_MANIFEST")
			return
		}
		upload := p.Receipts[0]
		var contentHash string
		if upload.Kind != "upload" || upload.Revision != "1" || tx.QueryRow(r.Context(), "SELECT sha256 FROM uploads WHERE id=$1 AND workspace_id=$2 AND user_id=$3 AND complete=true", upload.ID, workspace, session.UserID).Scan(&contentHash) != nil || contentHash != upload.SHA256 {
			problem(w, 422, "UPLOAD_RECEIPT_REQUIRED")
			return
		}
		data, e := artifacts.Read(s.ArtifactRoot, contentHash)
		if e != nil {
			problem(w, 503, "ARTIFACT_INTEGRITY_FAILURE")
			return
		}
		if !validConfig(p.Kind, data) {
			problem(w, 422, "INVALID_CONFIGURATION")
			return
		}
		raw, _ := json.Marshal(p.Manifest)
		manifestHash, e := artifacts.Put(s.ArtifactRoot, raw)
		if e != nil {
			problem(w, 503, "ARTIFACT_STORE_UNAVAILABLE")
			return
		}
		id := store.ID("art")
		// Keep canonical bytes as bytea in the database. JSONB whitespace normalization
		// must never change the manifest hash used by a submitted reference.
		_, err = tx.Exec(r.Context(), "INSERT INTO artifacts(id,workspace_id,author_id,authored_kind,manifest,sha256) VALUES($1,$2,$3,$4,$5,$6)", id, workspace, session.UserID, p.Kind, raw, manifestHash)
		refs = append(refs, ref{Kind: "artifact", ID: id, Revision: "1", SHA256: manifestHash})
		changed = []string{"artifacts"}
	case "programme.create":
		var p struct {
			Name      string        `json:"name"`
			Objective string        `json:"objective"`
			Profile   ref           `json:"profile_ref"`
			Policies  []ref         `json:"initial_policy_refs"`
			Budget    budgetCeiling `json:"budget_ceiling"`
		}
		if strictFrame(env.Payload, &p) != nil || strings.TrimSpace(p.Name) == "" || len(p.Name) > 120 || strings.TrimSpace(p.Objective) == "" || len(p.Objective) > 10000 || len(p.Policies) == 0 || len(p.Policies) > 10 || !validBudget(p.Budget) {
			problem(w, 422, "INVALID_PROGRAMME")
			return
		}
		if _, e := s.configBytes(r, tx, workspace, session.UserID, p.Profile, "domain-profile"); e != nil {
			problem(w, 422, "SEALED_PROFILE_REQUIRED")
			return
		}
		for _, policy := range p.Policies {
			if _, e := s.configBytes(r, tx, workspace, session.UserID, policy, "gate-policy"); e != nil {
				problem(w, 422, "SEALED_POLICY_REQUIRED")
				return
			}
		}
		id := store.ID("prg")
		profile, _ := json.Marshal(p.Profile)
		policies, _ := json.Marshal(p.Policies)
		budget, _ := json.Marshal(p.Budget)
		if _, err = tx.Exec(r.Context(), "INSERT INTO programmes(id,workspace_id,name,objective,profile_ref,policy_refs,budget_ceiling) VALUES($1,$2,$3,$4,$5,$6,$7)", id, workspace, p.Name, p.Objective, profile, policies, budget); err == nil {
			_, err = tx.Exec(r.Context(), `INSERT INTO programme_grants VALUES($1,$2,'["read","comment","edit","manage-access"]',1)`, id, session.UserID)
		}
		if err == nil {
			_, err = tx.Exec(r.Context(), "INSERT INTO programme_ceilings(programme_id,revision,amounts,author_id) VALUES($1,1,$2,$3)", id, budget, session.UserID)
		}
		refs = append(refs, ref{Kind: "programme", ID: id, Revision: "1"})
		changed = []string{"programmes"}
	case "access.change":
		var p struct {
			Subject ref `json:"subject_ref"`
			Changes ref `json:"grant_changes_ref"`
		}
		if strictFrame(env.Payload, &p) != nil || p.Subject.Kind != "programme" {
			problem(w, 422, "INVALID_GRANT_CHANGE")
			return
		}
		raw, e := s.configBytes(r, tx, workspace, session.UserID, p.Changes, "grant-changes")
		if e != nil {
			problem(w, 422, "SEALED_GRANTS_REQUIRED")
			return
		}
		var plan grantPlan
		json.Unmarshal(raw, &plan)
		if plan.Subject != p.Subject {
			problem(w, 409, "GRANT_SUBJECT_MISMATCH")
			return
		}
		var v int64
		if tx.QueryRow(r.Context(), "SELECT revision FROM programmes WHERE id=$1 AND workspace_id=$2", p.Subject.ID, workspace).Scan(&v) != nil {
			problem(w, 404, "NOT_FOUND")
			return
		}
		if p.Subject.Revision != formatRevision(v) || !expected(env, "programme", p.Subject.ID, v) {
			problem(w, 409, "REVISION_CONFLICT")
			return
		}
		for _, change := range plan.Changes {
			var access string
			if tx.QueryRow(r.Context(), "SELECT access_role FROM memberships WHERE workspace_id=$1 AND user_id=$2 AND state='active'", workspace, change.User).Scan(&access) != nil {
				problem(w, 422, "ACTIVE_MEMBER_REQUIRED")
				return
			}
			if access == "viewer" {
				for _, c := range change.Capabilities {
					if c != "read" {
						problem(w, 403, "PARENT_ACCESS_LIMIT")
						return
					}
				}
			}
			caps, _ := json.Marshal(change.Capabilities)
			if change.Capabilities == nil {
				caps = []byte("[]")
			}
			if _, err = tx.Exec(r.Context(), "INSERT INTO programme_grants(programme_id,user_id,capabilities) VALUES($1,$2,$3) ON CONFLICT(programme_id,user_id) DO UPDATE SET capabilities=excluded.capabilities,revision=programme_grants.revision+1", p.Subject.ID, change.User, caps); err != nil {
				break
			}
		}
		if err == nil {
			_, err = tx.Exec(r.Context(), "UPDATE programmes SET revision=revision+1 WHERE id=$1", p.Subject.ID)
		}
		epoch++
		refs = append(refs, ref{Kind: "programme", ID: p.Subject.ID, Revision: formatRevision(v + 1)})
		changed = []string{"access-grants"}
	default:
		problem(w, 422, "UNSUPPORTED_COMMAND")
		return
	}
	if err != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	s.finishCommand(w, r, tx, workspace, session.UserID, name, key, hash, env.Target, revision+1, sequence+1, epoch, refs, changed, session.UserID)
}

func (s *Server) programmeDetail(w http.ResponseWriter, r *http.Request, user, workspace, id, cursor string) {
	var name, objective string
	var revision int64
	var profile, policies, budget json.RawMessage
	err := s.DB.Pool.QueryRow(r.Context(), `SELECT p.name,p.objective,p.revision,p.profile_ref,p.policy_refs,p.budget_ceiling FROM programmes p JOIN programme_grants g ON g.programme_id=p.id WHERE p.id=$1 AND p.workspace_id=$2 AND g.user_id=$3 AND g.capabilities ? 'read'`, id, workspace, user).Scan(&name, &objective, &revision, &profile, &policies, &budget)
	if err != nil {
		problem(w, 404, "NOT_FOUND")
		return
	}
	read(w, map[string]any{"id": id, "workspace_id": workspace, "name": name, "objective": objective, "revision": formatRevision(revision), "profile_ref": profile, "policy_refs": policies, "budget_ceiling": budget}, cursor)
}
func (s *Server) accessGrants(w http.ResponseWriter, r *http.Request, user, workspace, role, cursor string, epoch, revision int64) {
	rows, err := s.DB.Pool.Query(r.Context(), "SELECT g.programme_id,g.user_id,g.capabilities,g.revision FROM programme_grants g JOIN programmes p ON p.id=g.programme_id WHERE p.workspace_id=$1 AND ($2='owner' OR g.user_id=$3) ORDER BY g.programme_id,g.user_id", workspace, role, user)
	if err != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var programme, account string
		var caps json.RawMessage
		var rev int64
		if rows.Scan(&programme, &account, &caps, &rev) != nil {
			problem(w, 503, "STORE_UNAVAILABLE")
			return
		}
		items = append(items, map[string]any{"programme_id": programme, "user_id": account, "capabilities": caps, "revision": formatRevision(rev)})
	}
	if rows.Err() != nil {
		problem(w, 503, "STORE_UNAVAILABLE")
		return
	}
	s.page(w, r, items, workspace, user, epoch, revision, cursor)
}
