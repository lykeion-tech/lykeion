package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutoresearchCommands(t *testing.T) {
	type seen struct {
		Method, Path string
		Body         map[string]any
	}
	var got []seen
	last := func() seen { return got[len(got)-1] }
	const id = "0b5e2c1a-0000-4000-8000-000000000001"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", 400)
			return
		}
		got = append(got, seen{r.Method, r.URL.Path, body})
		switch {
		case r.URL.Path == "/api/me":
			writeResponse(t, w, `{"id":"u-1"}`)
		case r.URL.Path == "/api/autoresearch":
			if r.Method == "GET" {
				writeResponsef(t, w, `[{"id":%q,"title":"Digest","execution_mode":"create_card","status":"active"}]`, id)
				return
			}
			writeResponsef(t, w, `{"id":%q,"title":"Digest","execution_mode":"create_card","status":"active"}`, id)
		case strings.HasSuffix(r.URL.Path, "/triggers"):
			writeResponse(t, w, `{"trigger":{"id":"t-1","kind":"webhook"},"token":"lwh_abc","url":"/api/webhooks/autoresearch/lwh_abc"}`)
		case strings.HasSuffix(r.URL.Path, "/runs"):
			writeResponse(t, w, `[{"id":"r-1","status":"skipped","reason":"runtime_offline","source":"schedule","planned_at":"2026-10-13T06:00:00Z"}]`)
		case strings.HasSuffix(r.URL.Path, "/run"):
			writeResponse(t, w, `{"id":"r-2","status":"running","source":"manual","card_id":"c-1"}`)
		default:
			writeResponse(t, w, `{}`)
		}
	}))
	defer srv.Close()
	cfg := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("LYK_CONFIG", cfg)
	if err := SaveConfig(cfg, Config{Server: srv.URL, Token: "lyk_t", Workspace: "ws1"}); err != nil {
		t.Fatal(err)
	}

	out := lyk(t, "", "autoresearch", "create", "--mode", "create_card", "--description", "What changed?", "--team", "grounding=p1", "--team", "reporting=p2", "--agent", "a1", "Weekly", "digest")
	if b := last().Body; last().Path != "/api/autoresearch" || b["title"] != "Weekly digest" || b["execution_mode"] != "create_card" ||
		b["description"] != "What changed?" || !strings.Contains(out, id) ||
		fmt.Sprint(b["assignee"]) != "map[agent_id:a1 teams:map[grounding:p1 reporting:p2] type:team]" {
		t.Fatalf("create: %+v %q", last(), out)
	}
	out = lyk(t, "whsec\n", "autoresearch", "webhook", id[:8], "--provider", "github", "--secret-stdin", "--filter", "pull_request:opened,closed", "--filter", "push")
	b := last().Body
	filters, err := json.Marshal(b["filters"])
	if err != nil {
		t.Fatal(err)
	}
	if last().Path != "/api/autoresearch/"+id+"/triggers" || b["kind"] != "webhook" || b["provider"] != "github" || b["signing_secret"] != "whsec" ||
		string(filters) != `[{"actions":["opened","closed"],"event":"pull_request"},{"event":"push"}]` ||
		!strings.Contains(out, srv.URL+"/api/webhooks/autoresearch/lwh_abc") || strings.Contains(out, "whsec") {
		t.Fatalf("webhook: %+v %s %q", last(), filters, out)
	}
	lyk(t, "", "autoresearch", "schedule", id[:8], "--cron", "0 9 * * 1", "--tz", "Europe/Copenhagen")
	if b := last().Body; b["kind"] != "schedule" || b["cron"] != "0 9 * * 1" || b["tz"] != "Europe/Copenhagen" {
		t.Fatalf("schedule: %+v", last())
	}
	if out := lyk(t, "", "autoresearch", "run", id[:8]); last().Path != "/api/autoresearch/"+id+"/run" || !strings.Contains(out, "running") {
		t.Fatalf("run: %+v %q", last(), out)
	}
	if out := lyk(t, "", "autoresearch", "runs", id[:8]); !strings.Contains(out, "skipped") || !strings.Contains(out, "runtime_offline") {
		t.Fatalf("runs: %q", out)
	}
	lyk(t, "", "autoresearch", "pause", id[:8], "--reason", "holiday")
	if last().Path != "/api/autoresearch/"+id+"/pause" || last().Body["reason"] != "holiday" {
		t.Fatalf("pause: %+v", last())
	}
	lyk(t, "", "autoresearch", "subscribe", id[:8])
	if last().Method != "PUT" || last().Path != "/api/autoresearch/"+id+"/subscribers/u-1" {
		t.Fatalf("subscribe: %+v", last())
	}

	lyk(t, "", "autoresearch", "unsubscribe", id[:8])
	if last().Method != "DELETE" || last().Path != "/api/autoresearch/"+id+"/subscribers/u-1" {
		t.Fatalf("unsubscribe: %+v", last())
	}
	lyk(t, "", "autoresearch", "resume", id)
	if last().Path != "/api/autoresearch/"+id+"/resume" {
		t.Fatalf("resume: %+v", last())
	}
	lyk(t, "", "autoresearch", "show", id)
	if last().Path != "/api/autoresearch/"+id {
		t.Fatalf("show: %+v", last())
	}
	if out := lyk(t, "", "autoresearch", "list"); !strings.Contains(out, "Digest") {
		t.Fatalf("list: %q", out)
	}

}

func TestReportNeedsAnAgentRunToken(t *testing.T) {
	t.Setenv("LYK_TOKEN", "")
	for _, args := range [][]string{{"report"}} {
		cmd := Root(strings.NewReader("text"), &strings.Builder{})
		cmd.SetArgs(args)
		cmd.SetErr(&strings.Builder{})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "LYK_TOKEN") {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func writeResponse(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("write response: %v", err)
	}
}
func writeResponsef(t *testing.T, w http.ResponseWriter, format string, args ...any) {
	t.Helper()
	writeResponse(t, w, fmt.Sprintf(format, args...))
}

func TestAutoresearchIDResolution(t *testing.T) {
	const id = "abcd0000-0000-4000-8000-000000000001"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResponsef(t, w, `[{"id":%q},{"id":"abcd1111-0000-4000-8000-000000000002"}]`, id)
	}))
	defer srv.Close()
	e := &env{cfg: Config{Server: srv.URL}}
	for _, tc := range []struct{ arg, want, err string }{
		{id, id, ""}, {"abcd0", id, ""}, {"abc", "", "no autoresearch"}, {"abcd", "", "matches 2"}, {"ffff", "", "no autoresearch"},
	} {
		t.Run(tc.arg, func(t *testing.T) {
			cmd := Root(strings.NewReader(""), &strings.Builder{})
			cmd.SetContext(context.Background())
			got, err := autoresearchID(cmd, e, tc.arg)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("got %q %v", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q %v", got, err)
			}
		})
	}
}

func TestAutoresearchTokenContexts(t *testing.T) {
	const id = "abcd0000-0000-4000-8000-000000000001"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := strings.HasPrefix(r.URL.Path, "/api/role/")
		want := "Bearer lyk_person"
		if role {
			want = "Bearer role_token"
		}
		if role && r.Header.Get("X-Workspace-ID") != "" {
			t.Error("role workspace leaked")
		}
		if r.Header.Get("Authorization") != want {
			t.Errorf("%s authorization = %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/role/report":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				http.Error(w, "bad request", 400)
				return
			}
			if body["text"] != "report text" {
				t.Errorf("report body: %v", body)
			}
			writeResponse(t, w, `{}`)
		default:
			writeResponsef(t, w, `[{"id":%q}]`, id)
		}
	}))
	defer srv.Close()
	cfg := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("LYK_CONFIG", cfg)
	if err := SaveConfig(cfg, Config{Server: srv.URL, Token: "lyk_person", Workspace: "ws1"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LYK_TOKEN", "role_token")
	t.Setenv("LYK_SERVER", srv.URL)
	if got := lyk(t, "report text", "report"); !strings.Contains(got, "Report posted") {
		t.Fatal(got)
	}
	lyk(t, "", "autoresearch", "list")
}
