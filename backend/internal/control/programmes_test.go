package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/artifacts"
	"github.com/lykeion-ia/lykeion-redesign/backend/internal/store"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSealedProgrammeAndNarrowResourceGrants(t *testing.T) {
	api, db, user, w, token, csrf := fixture(t)
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	ctx := context.Background()
	base := server.URL + "/v1/workspaces/" + w
	command := func(name string, payload any, versions ...ref) ref {
		_, _, _, v, e := db.Access(ctx, w, user)
		if e != nil {
			t.Fatal(e)
		}
		body := map[string]any{"schema_version": "1", "target": map[string]string{"kind": "workspace", "id": w}, "expected_versions": append([]ref{{Kind: "workspace", ID: w, Revision: formatRevision(v)}}, versions...), "payload": payload}
		status, raw := request(t, server.Client(), "POST", base+"/commands/"+name, token, csrf, store.ID("key"), body)
		if status != 202 {
			t.Fatalf("%s %d %s", name, status, raw)
		}
		var op struct {
			Refs []ref `json:"result_refs"`
		}
		json.Unmarshal(raw, &op)
		return op.Refs[0]
	}
	seal := func(kind, content string) ref {
		hash := artifacts.Hash([]byte(content))
		status, raw := request(t, server.Client(), "POST", base+"/uploads", token, csrf, store.ID("key"), map[string]any{"sha256": hash, "byte_length": len(content)})
		if status != 200 {
			t.Fatalf("reserve %d %s", status, raw)
		}
		var upload struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		json.Unmarshal(raw, &upload)
		status, raw = request(t, server.Client(), "PUT", base+"/uploads/"+upload.Data.ID+"/parts/1", token, csrf, "part", map[string]string{"content_base64": base64.StdEncoding.EncodeToString([]byte(content))})
		if status != 200 {
			t.Fatalf("part %d %s", status, raw)
		}
		status, raw = request(t, server.Client(), "POST", base+"/uploads/"+upload.Data.ID+"/complete", token, csrf, "complete", map[string]any{})
		if status != 200 {
			t.Fatalf("complete %d %s", status, raw)
		}
		var result struct {
			Data ref `json:"data"`
		}
		json.Unmarshal(raw, &result)
		return command("artifact.submit", map[string]any{"upload_receipt_refs": []ref{result.Data}, "manifest_candidate": map[string]any{"files": []any{map[string]any{"path": "configuration.json", "upload_ref": result.Data}}}, "authored_kind": kind})
	}
	profile := seal("domain-profile", `{"name":"Computational","domain":"computational","description":"Synthetic test profile"}`)
	policy := seal("gate-policy", `{"version":"1","required":["review-passed"],"rules":[{"id":"advance","require":["review-passed"],"outcome":"advance"}]}`)
	programme := command("programme.create", map[string]any{"name": "Private research", "objective": "Test sealed input handling", "profile_ref": profile, "initial_policy_refs": []ref{policy}, "budget_ceiling": map[string]string{"currency": "EUR", "model_microunits": "1000000", "cpu_milliseconds": "1000", "gpu_milliseconds": "0", "elapsed_milliseconds": "1000"}})
	member, _, code, e := db.Provision(ctx, "Other scientist", "Other workspace")
	if e != nil {
		t.Fatal(e)
	}
	mt, ms, e := db.Exchange(ctx, code)
	if e != nil {
		t.Fatal(e)
	}
	db.Pool.Exec(ctx, "INSERT INTO memberships(id,workspace_id,user_id,access_role) VALUES($1,$2,$3,'member')", store.ID("mem"), w, member)
	status, raw := request(t, server.Client(), "GET", base+"/programmes", mt, "", "", nil)
	if status != 200 || strings.Contains(string(raw), "Private research") {
		t.Fatal("hidden programme leaked", status, string(raw))
	}
	if status, _ = request(t, server.Client(), "GET", base+"/programmes/"+programme.ID, mt, "", "", nil); status != 404 {
		t.Fatal("hidden detail exposed", status)
	}
	grants, _ := json.Marshal(map[string]any{"subject_ref": programme, "changes": []any{map[string]any{"user_id": member, "capabilities": []string{"read"}}}})
	grantRef := seal("grant-changes", string(grants))
	programme = command("access.change", map[string]any{"subject_ref": programme, "grant_changes_ref": grantRef}, programme)
	status, raw = request(t, server.Client(), "GET", base+"/programmes/"+programme.ID, mt, "", "", nil)
	if status != 200 || !strings.Contains(string(raw), "Private research") {
		t.Fatal("authorized detail missing", status, string(raw))
	}
	if status, _ = request(t, server.Client(), "GET", base+"/artifacts/"+profile.ID, mt, "", "", nil); status != 404 {
		t.Fatal("grant silently exposed configuration bytes")
	}
	_, _, _, v, _ := db.Access(ctx, w, member)
	if status, _ = request(t, server.Client(), "POST", base+"/commands/programme.create", mt, ms.CSRF, "forbidden", map[string]any{"schema_version": "1", "target": map[string]string{"kind": "workspace", "id": w}, "expected_versions": []ref{{Kind: "workspace", ID: w, Revision: formatRevision(v)}}, "payload": map[string]any{}}); status != 403 {
		t.Fatal("member created programme", status)
	}
	// Leaving revokes grants permanently: a later rejoin is not a grant restore.
	_, _, _, version, _ := db.Access(ctx, w, member)
	var memberID string
	var memberVersion int64
	if err := db.Pool.QueryRow(ctx, "SELECT id,revision FROM memberships WHERE workspace_id=$1 AND user_id=$2", w, member).Scan(&memberID, &memberVersion); err != nil {
		t.Fatal(err)
	}
	status, raw = request(t, server.Client(), "POST", base+"/commands/member.leave", mt, ms.CSRF, "leave-resource", map[string]any{"schema_version": "1", "target": map[string]string{"kind": "workspace", "id": w}, "expected_versions": []ref{{Kind: "workspace", ID: w, Revision: formatRevision(version)}, {Kind: "membership", ID: memberID, Revision: formatRevision(memberVersion)}}, "payload": map[string]any{}})
	if status != 202 {
		t.Fatalf("leave %d %s", status, raw)
	}
	if _, err := db.Pool.Exec(ctx, "UPDATE memberships SET state='active' WHERE workspace_id=$1 AND user_id=$2", w, member); err != nil {
		t.Fatal(err)
	}
	if status, _ = request(t, server.Client(), "GET", base+"/programmes/"+programme.ID, mt, "", "", nil); status != 404 {
		t.Fatal("rejoin restored revoked grant", status)
	}

	var ceilings int
	db.Pool.QueryRow(ctx, "SELECT count(*) FROM programme_ceilings WHERE programme_id=$1", programme.ID).Scan(&ceilings)
	if ceilings != 1 {
		t.Fatal("missing initial budget ceiling")
	}
}
