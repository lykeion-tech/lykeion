package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeviceCredentialCannotImpersonateBrowserAndRevokes(t *testing.T) {
	api, db, user, w, browser, _ := fixture(t)
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	code, err := db.IssueLoginCode(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	status, raw := request(t, server.Client(), "POST", server.URL+"/v1/auth/device/exchange", "", "", "", map[string]string{"code": code})
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	var value struct {
		Data struct {
			Credential string `json:"credential"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Data.Credential == "" {
		t.Fatal("missing device credential")
	}
	call := func(method, path, credential string, body any) int {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+credential)
		req.Header.Set("Idempotency-Key", "cli-command")
		res, e := server.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		return res.StatusCode
	}
	if call("GET", "/v1/session", browser, nil) != 401 {
		t.Fatal("browser token accepted as bearer")
	}
	if status, _ = request(t, server.Client(), "GET", server.URL+"/v1/session", value.Data.Credential, "", "", nil); status != 401 {
		t.Fatal("CLI token accepted as cookie")
	}
	if call("GET", "/v1/workspaces/"+w+"/members", value.Data.Credential, nil) != 200 {
		t.Fatal("CLI read failed")
	}
	if call("POST", "/v1/workspaces/"+w+"/commands/preference.update", value.Data.Credential, preference(w, "dark")) != 202 {
		t.Fatal("CLI command failed")
	}
	if call("POST", "/v1/auth/logout", value.Data.Credential, nil) != 204 {
		t.Fatal("CLI logout failed")
	}
	if call("GET", "/v1/session", value.Data.Credential, nil) != 401 {
		t.Fatal("CLI retained access")
	}
}
