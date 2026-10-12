package control

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestInvitationLifecycleAndMembershipAuthority(t *testing.T) {
	api, db, owner, w, token, csrf := fixture(t)
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	ctx := context.Background()
	recipient, _, code, err := db.Provision(ctx, "Recipient", "Separate workspace")
	if err != nil {
		t.Fatal(err)
	}
	rt, rs, err := db.Exchange(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	cmd := func(name, key string, payload any, extra ...ref) (int, []byte) {
		_, _, _, rev, e := db.Access(ctx, w, owner)
		if e != nil {
			t.Fatal(e)
		}
		refs := append([]ref{{Kind: "workspace", ID: w, Revision: formatRevision(rev)}}, extra...)
		return request(t, server.Client(), "POST", server.URL+"/v1/workspaces/"+w+"/commands/"+name, token, csrf, key, map[string]any{"schema_version": "1", "target": map[string]string{"kind": "workspace", "id": w}, "expected_versions": refs, "payload": payload})
	}
	invite := func(key string) (string, string) {
		status, b := cmd("member.invite", key, map[string]any{"destination": recipient, "requested_role": "member", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
		if status != 202 {
			t.Fatalf("invite %d %s", status, b)
		}
		var op struct {
			Refs []ref `json:"result_refs"`
		}
		json.Unmarshal(b, &op)
		id := op.Refs[0].ID
		status, b = request(t, server.Client(), "GET", server.URL+"/v1/workspaces/"+w+"/invitations/"+id+"/proof", token, "", "", nil)
		if status != 200 {
			t.Fatalf("proof %d %s", status, b)
		}
		var proof struct {
			Data struct {
				Proof string `json:"proof"`
			} `json:"data"`
		}
		json.Unmarshal(b, &proof)
		return id, proof.Data.Proof
	}
	id, proof := invite("invite-first")
	accept := server.URL + "/v1/invitations/" + id + "/accept"
	body := map[string]string{"proof": proof, "revision": "1"}
	if status, _ := request(t, server.Client(), "POST", accept, token, csrf, "accept", body); status != 404 {
		t.Fatal("wrong recipient accepted", status)
	}
	if status, _ := request(t, server.Client(), "GET", server.URL+"/v1/workspaces/"+w+"/members", rt, "", "", nil); status != 404 {
		t.Fatal("workspace leaked before acceptance", status)
	}
	status, b := request(t, server.Client(), "POST", accept, rt, rs.CSRF, "accept", body)
	if status != 202 {
		t.Fatalf("accept %d %s", status, b)
	}
	_, repeat := request(t, server.Client(), "POST", accept, rt, rs.CSRF, "accept", body)
	var firstResult, repeatedResult any
	json.Unmarshal(b, &firstResult)
	json.Unmarshal(repeat, &repeatedResult)
	if !reflect.DeepEqual(firstResult, repeatedResult) {
		t.Fatal("acceptance replay changed")
	}
	if status, _ := request(t, server.Client(), "POST", accept, rt, rs.CSRF, "different", body); status != 409 {
		t.Fatal("proof reused", status)
	}
	if status, _ := request(t, server.Client(), "GET", server.URL+"/v1/workspaces/"+w+"/invitations", rt, "", "", nil); status != 403 {
		t.Fatal("member read invitation data", status)
	}
	var membership string
	var rev int64
	db.Pool.QueryRow(ctx, "SELECT id,revision FROM memberships WHERE workspace_id=$1 AND user_id=$2", w, recipient).Scan(&membership, &rev)
	if status, b := cmd("member.leave", "owner-leave", map[string]any{}); status != 409 {
		t.Fatalf("owner left %d %s", status, b)
	}
	if status, b := cmd("member.remove", "remove", map[string]any{"member_id": membership, "reason": "Test removal"}, ref{Kind: "membership", ID: membership, Revision: formatRevision(rev)}); status != 202 {
		t.Fatalf("remove %d %s", status, b)
	}
	if status, _ := request(t, server.Client(), "GET", server.URL+"/v1/workspaces/"+w+"/members", rt, "", "", nil); status != 404 {
		t.Fatal("removed member retained access", status)
	}
	id, proof = invite("invite-again")
	if status, b := cmd("invitation.revoke", "revoke", map[string]string{"invitation_id": id, "reason": "Cancel invitation"}, ref{Kind: "invitation", ID: id, Revision: "1"}); status != 202 {
		t.Fatalf("revoke %d %s", status, b)
	}
	if status, _ := request(t, server.Client(), "POST", server.URL+"/v1/invitations/"+id+"/accept", rt, rs.CSRF, "revoked", map[string]string{"proof": proof, "revision": "1"}); status != 409 {
		t.Fatal("revoked accepted", status)
	}
	id, proof = invite("invite-expire")
	db.Pool.Exec(ctx, "UPDATE invitations SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", id)
	if status, _ := request(t, server.Client(), "POST", server.URL+"/v1/invitations/"+id+"/accept", rt, rs.CSRF, "expired", map[string]string{"proof": proof, "revision": "1"}); status != 409 {
		t.Fatal("expired accepted", status)
	}
	var leaks int
	db.Pool.QueryRow(ctx, "SELECT count(*) FROM events WHERE payload::text LIKE '%' || $1 || '%'", proof).Scan(&leaks)
	if leaks != 0 {
		t.Fatal("proof leaked to event")
	}
	// Membership and original author attribution are retained after removal.
	var exists bool
	db.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)", recipient).Scan(&exists)
	if !exists {
		t.Fatal("author removed")
	}
	// Rejoin, then lose the leave response: recovery must not require access
	// that the successful command itself removed.
	id, proof = invite("invite-for-leave")
	if status, b := request(t, server.Client(), "POST", server.URL+"/v1/invitations/"+id+"/accept", rt, rs.CSRF, "accept-for-leave", map[string]string{"proof": proof, "revision": "1"}); status != 202 {
		t.Fatalf("rejoin %d %s", status, b)
	}
	_, _, _, workspaceRev, err := db.Access(ctx, w, recipient)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, "SELECT revision FROM memberships WHERE id=$1", membership).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	leaveBody := map[string]any{"schema_version": "1", "target": map[string]string{"kind": "workspace", "id": w}, "expected_versions": []ref{{Kind: "workspace", ID: w, Revision: formatRevision(workspaceRev)}, {Kind: "membership", ID: membership, Revision: formatRevision(rev)}}, "payload": map[string]any{}}
	leaveURL := server.URL + "/v1/workspaces/" + w + "/commands/member.leave"
	status, first := request(t, server.Client(), "POST", leaveURL, rt, rs.CSRF, "leave-once", leaveBody)
	if status != 202 {
		t.Fatalf("leave %d %s", status, first)
	}
	status, repeated := request(t, server.Client(), "POST", leaveURL, rt, rs.CSRF, "leave-once", leaveBody)
	json.Unmarshal(first, &firstResult)
	json.Unmarshal(repeated, &repeatedResult)
	if status != 202 || !reflect.DeepEqual(firstResult, repeatedResult) {
		t.Fatalf("leave recovery %d %s", status, repeated)
	}
	if status, _ := request(t, server.Client(), "POST", leaveURL, rt, rs.CSRF, "new-leave", leaveBody); status != 404 {
		t.Fatal("new leave allowed after departure", status)
	}
	leaveBody["payload"] = map[string]any{"reason": "changed"}
	if status, _ := request(t, server.Client(), "POST", leaveURL, rt, rs.CSRF, "leave-once", leaveBody); status != 409 {
		t.Fatal("changed leave recovered original receipt", status)
	}
}
