package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/apiclient"
	"lykeion/server/internal/skill"
)

func TestRuntimeSkillDaemonCopiesOnlySelectedAndRetries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	selected := filepath.Join(home, ".claude", "skills", "selected")
	localSkillFixture(t, selected, "selected-method")
	raw := []byte{0, 255, 128, 42}
	if err := os.WriteFile(filepath.Join(selected, ".hidden"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	// Importing a selection must not enumerate another broken skill.
	bad := filepath.Join(home, ".claude", "skills", "unselected")
	localSkillFixture(t, bad, "unselected-method")
	if err := os.Symlink("/missing-secret", filepath.Join(bad, "escape")); err != nil {
		t.Fatal(err)
	}
	ws, rt, job, acquisition := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	prefix := "/api/daemon/runtimes/" + rt.String() + "/local-skills"
	claimed := false
	uploads, failures := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer lyk_test" || r.Header.Get("X-Workspace-ID") != ws.String() {
			t.Error("missing authenticated workspace transport")
		}
		switch r.URL.Path {
		case prefix:
			json.NewEncoder(w).Encode([]any{})
		case prefix + "/import":
			if claimed {
				json.NewEncoder(w).Encode([]any{})
				return
			}
			claimed = true
			json.NewEncoder(w).Encode([]any{map[string]any{"id": job, "workspace_id": ws, "runtime_id": rt, "daemon_id": "daemon-test", "operation": "import", "status": "running", "harness": "claude", "keys": []string{"selected", "missing"}, "acquisition_id": acquisition, "expires_at": time.Now().Add(time.Minute)}})
		case prefix + "/import/" + job.String() + "/result":
			if r.URL.Query().Get("acquisition_id") != acquisition.String() || r.URL.Query().Get("daemon_id") != "daemon-test" {
				t.Error("missing claim fence")
			}
			if r.URL.Query().Get("key") == "selected" {
				uploads++
				if r.Header.Get("Content-Type") != "application/zip" {
					t.Error("bundle sent as JSON")
				}
				data, _ := io.ReadAll(r.Body)
				b, err := skill.ParseArchive(data)
				if err != nil || !bytes.Equal(b.Files[".hidden"], raw) {
					t.Errorf("bytes changed: %v", err)
				}
				if uploads == 1 {
					http.Error(w, `{"error":"transient"}`, 503)
					return
				}
			} else {
				failures++
				var in map[string]string
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in["error"] == "" || len(in) != 1 {
					t.Error("failed selection must report metadata only")
				}
			}
			w.WriteHeader(200)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	d := daemon{id: "daemon-test", api: &apiclient.Client{Server: server.URL, Token: "lyk_test"}, refs: []runtimeRef{{ws, rt}}}
	d.runtimeSkills(context.Background())
	if uploads != 2 || failures != 1 {
		t.Fatalf("selected results: uploads=%d failures=%d", uploads, failures)
	}
	d.runtimeSkills(context.Background())
	if uploads != 2 || failures != 1 {
		t.Fatal("empty claims read home again")
	}
}
func TestRuntimeSkillDaemonInventoryIsExplicit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	localSkillFixture(t, filepath.Join(home, ".cursor", "skills", "method"), "cursor-method")
	ws, rt, job, acquisition := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	prefix := "/api/daemon/runtimes/" + rt.String() + "/local-skills"
	received := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case prefix:
			json.NewEncoder(w).Encode([]any{map[string]any{"id": job, "workspace_id": ws, "runtime_id": rt, "daemon_id": "d", "operation": "list", "status": "running", "harness": "cursor", "acquisition_id": acquisition, "expires_at": time.Now().Add(time.Minute)}})
		case prefix + "/import":
			json.NewEncoder(w).Encode([]any{})
		case prefix + "/" + job.String() + "/result":
			data, _ := io.ReadAll(r.Body)
			if !bytes.Contains(data, []byte("cursor-method")) || bytes.Contains(data, []byte(`"files"`)) || bytes.Contains(data, []byte("Local research\\n---")) {
				t.Errorf("bad metadata %s", data)
			}
			received = true
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer srv.Close()
	d := daemon{id: "d", api: &apiclient.Client{Server: srv.URL, Token: "lyk_test"}, refs: []runtimeRef{{ws, rt}}}
	d.runtimeSkills(context.Background())
	if !received {
		t.Fatal("no inventory result")
	}
}
func TestRuntimeSkillReportStopsOnPermanentFailure(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, `{"error":"stale"}`, 409) }))
	defer srv.Close()
	api := &apiclient.Client{Server: srv.URL}
	err := reportRuntimeSkill(context.Background(), api, "/result", map[string]string{"error": "failed"})
	if err == nil || calls != 1 || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("permanent retry: %d %v", calls, err)
	}
}

func TestRuntimeSkillDaemonRealServerRoundTrip(t *testing.T) {
	f := fixture(t)
	ctx := context.Background()
	home, err := os.UserHomeDir()
	must(t, err)
	dir := filepath.Join(home, ".claude", "skills", "copied")
	localSkillFixture(t, dir, "real-copy-method")
	binary := []byte{0, 255, 128, 42}
	must(t, os.WriteFile(filepath.Join(dir, ".hidden"), binary, 0600))
	api := &apiclient.Client{Server: f.cfg.Server, Token: f.cfg.Token, Workspace: f.ws.String()}
	d := daemon{id: "real-skills", cfg: f.cfg, api: api, knownWorkspaces: map[uuid.UUID]bool{}}
	must(t, d.register(ctx, d.id))
	if len(d.refs) != 1 {
		t.Fatal("runtime not registered")
	}
	route := "/api/runtimes/" + d.refs[0].RuntimeID.String() + "/local-skills"
	var list struct {
		ID     string
		Status string
		Result struct{ Skills []localSkillSummary }
	}
	must(t, api.Do(ctx, "POST", route, nil, &list))
	d.runtimeSkills(ctx)
	must(t, api.Do(ctx, "GET", route+"/"+list.ID, nil, &list))
	if list.Status != "completed" || len(list.Result.Skills) != 1 || list.Result.Skills[0].FileCount != 2 {
		t.Fatalf("real inventory: %+v", list)
	}
	var job struct {
		ID     string
		Status string
		Result struct {
			Items []struct {
				Status  string
				SkillID string `json:"skill_id"`
			}
		}
	}
	must(t, api.Do(ctx, "POST", route+"/import", map[string]any{"keys": []string{list.Result.Skills[0].Key}}, &job))
	d.runtimeSkills(ctx)
	must(t, api.Do(ctx, "GET", route+"/import/"+job.ID, nil, &job))
	if job.Status != "completed" || len(job.Result.Items) != 1 || job.Result.Items[0].Status != "imported" {
		t.Fatalf("real import: %+v", job)
	}
	req, _ := http.NewRequest("GET", f.cfg.Server+"/api/skills/"+job.Result.Items[0].SkillID+"/files?path=.hidden", nil)
	req.Header.Set("Authorization", "Bearer "+f.cfg.Token)
	req.Header.Set("X-Workspace-ID", f.ws.String())
	resp, err := http.DefaultClient.Do(req)
	must(t, err)
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	must(t, err)
	if resp.StatusCode != 200 || !bytes.Equal(got, binary) {
		t.Fatalf("import changed bytes: %d %v", resp.StatusCode, got)
	}
}
