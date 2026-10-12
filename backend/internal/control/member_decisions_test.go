package control

import (
	"context"
	"encoding/json"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/store"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOwnerTransferRequiresRecipientAndExactVersions(t *testing.T) {
	api, db, owner, w, token, csrf := fixture(t)
	ctx := context.Background()
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	user, _, code, err := db.Provision(ctx, "Future owner", "Recipient workspace")
	if err != nil {
		t.Fatal(err)
	}
	rt, rs, err := db.Exchange(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	member := store.ID("mem")
	if _, err = db.Pool.Exec(ctx, "INSERT INTO memberships(id,workspace_id,user_id,access_role) VALUES($1,$2,$3,'member')", member, w, user); err != nil {
		t.Fatal(err)
	}
	call := func(name, who, csrf, key string, payload any, refs ...ref) (int, []byte) {
		_, _, _, v, err := db.Access(ctx, w, owner)
		if err != nil {
			t.Fatal(err)
		}
		return request(t, server.Client(), "POST", server.URL+"/v1/workspaces/"+w+"/commands/"+name, who, csrf, key, map[string]any{"schema_version": "1", "target": map[string]string{"kind": "workspace", "id": w}, "expected_versions": append([]ref{{Kind: "workspace", ID: w, Revision: formatRevision(v)}}, refs...), "payload": payload})
	}
	create := func(key string) string {
		status, b := call("workspace.request-owner-transfer", token, csrf, key, map[string]any{"recipient_member_id": member, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, ref{Kind: "membership", ID: member, Revision: "1"})
		if status != 202 {
			t.Fatalf("request %d %s", status, b)
		}
		var result struct {
			Refs []ref `json:"result_refs"`
		}
		json.Unmarshal(b, &result)
		return result.Refs[0].ID
	}
	id := create("first")
	_, _, _, v, _ := db.Access(ctx, w, owner)
	body := map[string]any{"decision_id": id, "decision_revision": "1", "choice_id": "approve", "subject_versions": []ref{{Kind: "workspace", ID: w, Revision: formatRevision(v)}, {Kind: "membership", ID: member, Revision: "1"}}}
	if status, _ := call("decision.respond", token, csrf, "self-approve", body); status != 404 {
		t.Fatal("owner self-approved transfer", status)
	}
	// A new request changes the pinned workspace revision. The old approval must fail.
	next := create("second")
	if status, _ := call("decision.respond", rt, rs.CSRF, "stale", body); status != 409 {
		t.Fatal("stale decision approved", status)
	}
	_, _, _, v, _ = db.Access(ctx, w, owner)
	body["decision_id"] = next
	body["subject_versions"] = []ref{{Kind: "workspace", ID: w, Revision: formatRevision(v)}, {Kind: "membership", ID: member, Revision: "1"}}
	if status, b := call("decision.respond", rt, rs.CSRF, "accept-owner", body); status != 202 {
		t.Fatalf("accept %d %s", status, b)
	}
	oldRole, _, _, _, _ := db.Access(ctx, w, owner)
	newRole, _, _, _, _ := db.Access(ctx, w, user)
	if oldRole != "admin" || newRole != "owner" {
		t.Fatalf("roles %s %s", oldRole, newRole)
	}
	var count int
	db.Pool.QueryRow(ctx, "SELECT count(*) FROM memberships WHERE workspace_id=$1 AND state='active' AND access_role='owner'", w).Scan(&count)
	if count != 1 {
		t.Fatal("owner invariant", count)
	}
	if status, _ := call("decision.respond", rt, rs.CSRF, "reroll", body); status != 409 {
		t.Fatal("decision rerolled", status)
	}
}
