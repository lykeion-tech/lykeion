package handler

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	db "lykeion/server/pkg/db/generated"
)

type resolvedAutoresearchRun struct {
	AutoresearchID    string             `json:"autoresearch_id"`
	AutoresearchTitle string             `json:"autoresearch_title"`
	ExecutionMode     string             `json:"execution_mode"`
	Run               db.AutoresearchRun `json:"run"`
}

func (s *server) resolvedRun(t *testing.T, token, workspace, id string) resolvedAutoresearchRun {
	t.Helper()
	path := "/api/autoresearch-runs/" + id
	if code := s.call(t, "GET", path, token, workspace, nil, nil); code != 200 {
		t.Fatalf("resolve Autoresearch run: got %d, want 200", code)
	}
	var out resolvedAutoresearchRun
	if code := s.call(t, "GET", path, token, workspace, nil, &out); code != 200 {
		t.Fatalf("read Autoresearch run: %d", code)
	}
	return out
}

func TestAutoresearchInboxRunWithoutCardResolves(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "inbox-run@lab.org")
	workspace := s.workspace(t, owner, "lab")
	ap, run, token, _ := s.runOnly(t, owner, workspace.ID)
	fullReport := "# Findings\n\n" + strings.Repeat("界 **evidence**\n", 2100) + "\n<script>raw text</script>"
	if code := s.call(t, "POST", "/api/role/report", token, "", map[string]string{"text": fullReport}, nil); code != 201 {
		t.Fatalf("post report: %d", code)
	}
	var items []struct {
		Kind              string  `json:"kind"`
		CardID            *string `json:"card_id"`
		AutoresearchRunID *string `json:"autoresearch_run_id"`
		Text              string  `json:"text"`
	}
	if code := s.call(t, "GET", "/api/inbox", owner, workspace.ID, nil, &items); code != 200 || len(items) != 1 {
		t.Fatalf("Inbox: %d %+v", code, items)
	}
	item := items[0]
	if item.Kind != "autoresearch_report" || item.CardID != nil || item.AutoresearchRunID == nil || *item.AutoresearchRunID != run.ID || item.Text == fullReport {
		t.Fatalf("cardless report Inbox identity: %+v", item)
	}
	got := s.resolvedRun(t, owner, workspace.ID, *item.AutoresearchRunID)
	if got.AutoresearchID != ap.ID || got.AutoresearchTitle != "Digest" || got.ExecutionMode != "run_only" || got.Run.ID.String() != run.ID || got.Run.AutoresearchID.String() != ap.ID {
		t.Fatalf("resolved identity: %+v", got)
	}
	if got.Run.CardID != nil || got.Run.Report != fullReport || got.Run.Status != "running" || got.Run.Source != "manual" {
		t.Fatal("report/status conflated or raw report changed")
	}
	var history []db.AutoresearchRun
	if code := s.call(t, "GET", "/api/autoresearch/"+ap.ID+"/runs", owner, workspace.ID, nil, &history); code != 200 || len(history) != 1 {
		t.Fatalf("run history: %d %+v", code, history)
	}
	if got.Run.PlannedAt.IsZero() || !reflect.DeepEqual(got.Run, history[0]) {
		t.Fatal("resolver changed the existing run projection or planned time")
	}
}

func TestAutoresearchReportAndStatusAreIndependent(t *testing.T) {
	for _, status := range []string{"running", "completed", "failed"} {
		for _, hasReport := range []bool{false, true} {
			name := status + "/without_report"
			if hasReport {
				name = status + "/with_report"
			}
			t.Run(name, func(t *testing.T) {
				s := newServer(t)
				owner := s.signIn(t, "status-run@lab.org")
				workspace := s.workspace(t, owner, "lab")
				_, run, token, complete := s.runOnly(t, owner, workspace.ID)
				report := ""
				if hasReport {
					report = "# Submitted findings\n\nThe report survives execution failure."
					if code := s.call(t, "POST", "/api/role/report", token, "", map[string]string{"text": report}, nil); code != 201 {
						t.Fatalf("post report: %d", code)
					}
				}
				reason := ""
				switch status {
				case "completed":
					complete()
				case "failed":
					var runtime string
					var attempt int32
					if err := s.Env.Owner.QueryRow(t.Context(), `SELECT runtime_id, attempt FROM agent_run WHERE id=$1`, *run.AgentRunID).Scan(&runtime, &attempt); err != nil {
						t.Fatal(err)
					}
					reason = "agent_error"
					body := map[string]any{"workspace_id": workspace.ID, "runtime_id": runtime, "attempt": attempt, "failure_reason": reason, "error": "execution failed after submitting findings"}
					if code := s.call(t, "POST", "/api/daemon/agent-runs/"+*run.AgentRunID+"/fail", s.personalToken(t, owner), "", body, nil); code != 200 {
						t.Fatalf("fail execution: %d", code)
					}
				}
				got := s.resolvedRun(t, owner, workspace.ID, run.ID)
				if got.Run.Status != status || got.Run.Report != report || got.Run.Reason != reason || got.Run.CardID != nil {
					t.Fatalf("report availability and execution status: %+v", got.Run)
				}
			})
		}
	}
}

func TestAutoresearchRunReadIsTenantScoped(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "run-owner@lab.org")
	workspace := s.workspace(t, owner, "lab")
	member := s.member(t, owner, workspace.ID, "run-member@lab.org", "member")
	otherWorkspace := s.workspace(t, owner, "other-lab")
	outsider := s.signIn(t, "run-outsider@lab.org")
	ap, run, roleToken, _ := s.runOnly(t, owner, workspace.ID)
	got := s.resolvedRun(t, member, workspace.ID, run.ID)
	if got.AutoresearchID != ap.ID || got.Run.ID.String() != run.ID {
		t.Fatalf("a member reads the run: %+v", got)
	}
	for _, tc := range []struct {
		name, token, workspace, id string
		want                       int
	}{
		{"foreign workspace", owner, otherWorkspace.ID, run.ID, 404},
		{"nonmember", outsider, workspace.ID, run.ID, 404},
		{"unknown run", owner, workspace.ID, uuid.NewString(), 404},
		{"malformed run", owner, workspace.ID, "invalid", 404},
		{"missing workspace", owner, "", run.ID, 400},
		{"unauthenticated", "", workspace.ID, run.ID, 401},
		{"agent token", roleToken, workspace.ID, run.ID, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code := s.call(t, "GET", "/api/autoresearch-runs/"+tc.id, tc.token, tc.workspace, nil, nil); code != tc.want {
				t.Fatalf("run read: got %d, want %d", code, tc.want)
			}
		})
	}
	if code := s.call(t, "DELETE", "/api/workspaces/"+workspace.ID+"/members/"+s.userID(t, member), owner, "", nil, nil); code != 204 {
		t.Fatalf("remove member: %d", code)
	}
	if code := s.call(t, "GET", "/api/autoresearch-runs/"+run.ID, member, workspace.ID, nil, nil); code != 404 {
		t.Fatalf("removed member read: %d", code)
	}
	linked := s.autoresearch(t, owner, workspace.ID, map[string]any{"title": "Linked study", "execution_mode": "create_card"})
	var linkedRun runView
	if code := s.call(t, "POST", "/api/autoresearch/"+linked.ID+"/run", owner, workspace.ID, nil, &linkedRun); code != 201 || linkedRun.CardID == nil {
		t.Fatalf("create linked study run: %d %+v", code, linkedRun)
	}
	linkedRead := s.resolvedRun(t, owner, workspace.ID, linkedRun.ID)
	if linkedRead.ExecutionMode != "create_card" || linkedRead.AutoresearchTitle != "Linked study" || linkedRead.Run.CardID == nil || linkedRead.Run.CardID.String() != *linkedRun.CardID {
		t.Fatalf("linked card projection: %+v", linkedRead)
	}
}
