package handler

import (
	"archive/zip"
	"bytes"
	"io"
	"maps"
	"testing"
	"time"

	"github.com/google/uuid"
	"lykeion/server/internal/testutil"
)

// codexTeamLab installs a synthetic Codex-only team; no CLI is launched.
func codexTeamLab(t *testing.T) (*teamLab, teamRun) {
	t.Helper()
	return codexTeamLabOnServer(t, newServer(t), "codex-team@lab.org")
}

func codexTeamLabOnServer(t *testing.T, s *server, email string) (*teamLab, teamRun) {
	t.Helper()
	l := &teamLab{s: s, jwt: s.signIn(t, email)}
	l.ws = s.workspace(t, l.jwt, "codex-team-"+uuid.NewString()[:8]).ID
	z := testutil.Pack(t, testutil.PackOpts{Name: "codex-env", Stage: "grounding", Members: 1})
	zr, err := zip.NewReader(bytes.NewReader(z), int64(len(z)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, file := range zr.File {
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if file.Name == "pack.json" {
			b = bytes.ReplaceAll(b, []byte(`"harness":"claude"`), []byte(`"harness":"codex"`))
			b = bytes.ReplaceAll(b, []byte(`"instructions":"You audit.","model":""`), []byte(`"instructions":"You audit.","model":"gpt-5.1-codex"`))
		}
		files[file.Name] = string(b)
	}
	var up struct{ ID string }
	s.installPackFixture(t, l.jwt, l.ws, testutil.Zip(t, files), &up)
	if code := s.call(t, "POST", "/api/teams/"+up.ID+"/add", l.jwt, l.ws, nil, nil); code != 200 {
		t.Fatalf("add team: %d", code)
	}
	l.team, l.personal = up.ID, s.personalToken(t, l.jwt)
	var rts []struct{ ID string }
	if code := s.call(t, "POST", "/api/daemon/register", l.personal, "", map[string]any{"workspace_id": l.ws, "daemon_id": "codex-test", "harnesses": []string{"codex"}}, &rts); code != 200 || len(rts) != 1 {
		t.Fatalf("register: %d %+v", code, rts)
	}
	l.rts = rts[0].ID
	if code := s.call(t, "POST", "/api/cards", l.jwt, l.ws, map[string]any{"question": "Can we analyse the evidence?", "assignee": teamAssignee(map[string]string{"grounding": l.team})}, &l.card); code != 201 {
		t.Fatalf("card: %d", code)
	}
	return l, l.claim(t)
}

func TestTeamLeaderUsesAttestedMemberEnvironmentFailure(t *testing.T) {
	l, leader := codexTeamLab(t)
	code, ask := l.ask(t, leader.token, "member-1", "Run a Python analysis.")
	if code != 201 || ask.Ticket == "" {
		t.Fatalf("delegate: %d %+v", code, ask)
	}
	member := l.claim(t)
	if member.id != ask.Ticket {
		t.Fatalf("claimed wrong member: %+v", member)
	}
	memberHand := grounding()
	memberHand["status"], memberHand["reason"], memberHand["blocked_by_environment"] = "could_not_do", "Python environment unavailable", "python"
	in := maps.Clone(member.ref)
	in["hand_in"], in["environment_failures"] = memberHand, []string{"python"}
	var out handInOut
	if code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+member.id+"/hand-in", l.personal, "", in, &out); code != 200 || !out.Accepted {
		t.Fatalf("member hand-in: %d %+v", code, out)
	}
	l.complete(t, member)
	if code, result := l.result(t, leader.token, ask.Ticket); code != 200 || result.Status != "done" {
		t.Fatalf("member result: %d %+v", code, result)
	}
	leaderHand := grounding()
	leaderHand["status"], leaderHand["reason"], leaderHand["blocked_by_environment"], leaderHand["blocked_by_member_run"] = "could_not_do", "The delegated Python environment is unavailable", "python", ask.Ticket
	in = maps.Clone(leader.ref)
	in["hand_in"] = leaderHand
	out = handInOut{}
	if code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+leader.id+"/hand-in", l.personal, "", in, &out); code != 200 || !out.Accepted {
		t.Fatalf("leader should accept exact delegated attestation: status=%d %+v", code, out)
	}
	l.complete(t, leader)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		if l.s.count(t, `SELECT count(*) FROM card WHERE id=$1 AND state='blocked'`, l.card.ID) == 1 {
			break
		}
	}
	if n := l.s.count(t, `SELECT count(*) FROM card WHERE id=$1 AND state='blocked'`, l.card.ID); n != 1 {
		t.Fatal("study did not remain blocked")
	}
	if n := l.s.count(t, `SELECT count(*) FROM inbox_item WHERE card_id=$1 AND resolved_at IS NULL AND (jsonb_array_length(options)>0 OR question_set IS NOT NULL OR expires_at IS NOT NULL)`, l.card.ID); n != 0 {
		t.Fatalf("operational failure created %d decisions or timers", n)
	}
}

func TestTeamLeaderCannotForgeMemberEnvironmentEvidence(t *testing.T) {
	l, leader := codexTeamLab(t)
	for _, ticket := range []string{uuid.NewString(), leader.id} {
		hand := grounding()
		hand["status"], hand["reason"], hand["blocked_by_environment"], hand["blocked_by_member_run"] = "could_not_do", "Python unavailable", "python", ticket
		in := maps.Clone(leader.ref)
		in["hand_in"] = hand
		var out handInOut
		code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+leader.id+"/hand-in", l.personal, "", in, &out)
		if code == 200 && out.Accepted {
			t.Fatalf("forged ticket %s was accepted", ticket)
		}
		if n := l.kinds(t, "hand_in"); n != 0 {
			t.Fatalf("forged ticket wrote %d hand-ins", n)
		}
		if code == 200 && len(out.Problems) == 0 {
			t.Fatalf("ticket %s rejected without an explanation", ticket)
		}
	}
}

func completedEnvironmentMember(t *testing.T, l *teamLab, leader teamRun) teamRun {
	t.Helper()
	code, ask := l.ask(t, leader.token, "member-1", "Run a Python analysis.")
	if code != 201 || ask.Ticket == "" {
		t.Fatalf("delegate: %d %+v", code, ask)
	}
	member := l.claim(t)
	if member.id != ask.Ticket {
		t.Fatalf("claimed wrong member: %+v", member)
	}
	hand := grounding()
	hand["status"], hand["reason"], hand["blocked_by_environment"] = "could_not_do", "Python unavailable", "python"
	in := maps.Clone(member.ref)
	in["hand_in"], in["environment_failures"] = hand, []string{"python"}
	var out handInOut
	if code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+member.id+"/hand-in", l.personal, "", in, &out); code != 200 || !out.Accepted {
		t.Fatalf("member hand-in: %d %+v", code, out)
	}
	l.complete(t, member)
	return member
}

func TestTeamLeaderRejectsMismatchedMemberEnvironmentEvidence(t *testing.T) {
	cases := []struct {
		name, query, language string
	}{
		{"different language", "", "r"},
		{"wrong column", `UPDATE agent_run SET board_column='hypotheses' WHERE id=$1`, "python"},
		{"wrong round", `UPDATE agent_run SET round=round+1 WHERE id=$1`, "python"},
		{"stale attempt", `UPDATE agent_run SET attempt=attempt+1 WHERE id=$1`, "python"},
		{"superseded hand-in", `UPDATE record SET superseded_at=now() WHERE agent_run_id=$1 AND kind='hand_in'`, "python"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, leader := codexTeamLab(t)
			member := completedEnvironmentMember(t, l, leader)
			if tc.query != "" {
				if _, err := l.s.Env.Owner.Exec(t.Context(), tc.query, member.id); err != nil {
					t.Fatal(err)
				}
			}
			hand := grounding()
			hand["status"], hand["reason"], hand["blocked_by_environment"], hand["blocked_by_member_run"] = "could_not_do", "member environment unavailable", tc.language, member.id
			in := maps.Clone(leader.ref)
			in["hand_in"] = hand
			var out handInOut
			code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+leader.id+"/hand-in", l.personal, "", in, &out)
			if code == 200 && out.Accepted {
				t.Fatalf("mismatched evidence accepted: %+v", out)
			}
			if n := l.s.count(t, `SELECT count(*) FROM record WHERE agent_run_id=$1 AND kind='hand_in'`, leader.id); n != 0 {
				t.Fatalf("leader wrote %d hand-ins", n)
			}
		})
	}
}

func TestTeamLeaderRejectsOtherWorkspaceMemberEvidence(t *testing.T) {
	l, leader := codexTeamLab(t)
	other, otherLeader := codexTeamLabOnServer(t, l.s, "other-team@lab.org")
	member := completedEnvironmentMember(t, other, otherLeader)
	hand := grounding()
	hand["status"], hand["reason"], hand["blocked_by_environment"], hand["blocked_by_member_run"] = "could_not_do", "Python unavailable", "python", member.id
	in := maps.Clone(leader.ref)
	in["hand_in"] = hand
	var out handInOut
	code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+leader.id+"/hand-in", l.personal, "", in, &out)
	if code == 200 && out.Accepted {
		t.Fatalf("cross-workspace member evidence accepted: %+v", out)
	}
	if n := l.s.count(t, `SELECT count(*) FROM record WHERE agent_run_id=$1 AND kind='hand_in'`, leader.id); n != 0 {
		t.Fatalf("leader wrote %d hand-ins", n)
	}
}

func TestTeamLeaderRejectsOtherStudyMemberEvidence(t *testing.T) {
	l, leader := codexTeamLab(t)
	other := *l
	if code := l.s.call(t, "POST", "/api/cards", l.jwt, l.ws, map[string]any{"question": "Other study", "assignee": teamAssignee(map[string]string{"grounding": l.team})}, &other.card); code != 201 {
		t.Fatalf("other card: %d", code)
	}
	var rts []struct{ ID string }
	if code := l.s.call(t, "POST", "/api/daemon/register", l.personal, "", map[string]any{"workspace_id": l.ws, "daemon_id": "codex-test-2", "harnesses": []string{"codex"}}, &rts); code != 200 || len(rts) != 1 {
		t.Fatalf("second runtime: %d %+v", code, rts)
	}
	other.rts = rts[0].ID
	otherLeader := other.claim(t)
	if otherLeader.role != "leader" || otherLeader.id == leader.id {
		t.Fatalf("wrong other leader: %+v", otherLeader)
	}
	member := completedEnvironmentMember(t, &other, otherLeader)
	hand := grounding()
	hand["status"], hand["reason"], hand["blocked_by_environment"], hand["blocked_by_member_run"] = "could_not_do", "Python unavailable", "python", member.id
	in := maps.Clone(leader.ref)
	in["hand_in"] = hand
	var out handInOut
	code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+leader.id+"/hand-in", l.personal, "", in, &out)
	if code == 200 && out.Accepted {
		t.Fatalf("other-study evidence accepted: %+v", out)
	}
	if n := l.s.count(t, `SELECT count(*) FROM record WHERE agent_run_id=$1 AND kind='hand_in'`, leader.id); n != 0 {
		t.Fatalf("leader wrote %d hand-ins", n)
	}
}
