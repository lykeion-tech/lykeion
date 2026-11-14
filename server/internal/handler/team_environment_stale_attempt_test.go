package handler

import (
	"maps"
	"testing"
)

func TestTeamLeaderCannotReusePriorAttemptDelegation(t *testing.T) {
	l, leader := codexTeamLab(t)
	code, ask := l.ask(t, leader.token, "member-1", "Run a Python analysis.")
	if code != 201 {
		t.Fatalf("delegate: %d", code)
	}
	member := l.claim(t)
	if member.id != ask.Ticket {
		t.Fatalf("wrong member: %s", member.id)
	}
	memberHand := grounding()
	memberHand["status"], memberHand["reason"], memberHand["blocked_by_environment"] = "could_not_do", "Python unavailable", "python"
	in := maps.Clone(member.ref)
	in["hand_in"], in["environment_failures"] = memberHand, []string{"python"}
	var out handInOut
	if code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+member.id+"/hand-in", l.personal, "", in, &out); code != 200 || !out.Accepted {
		t.Fatalf("member hand-in: %d %+v", code, out)
	}
	l.complete(t, member)
	if _, err := l.s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET attempt=attempt+1 WHERE id=$1`, leader.id); err != nil {
		t.Fatal(err)
	}
	leaderHand := grounding()
	leaderHand["status"], leaderHand["reason"], leaderHand["blocked_by_environment"], leaderHand["blocked_by_member_run"] = "could_not_do", "Python unavailable", "python", ask.Ticket
	in = maps.Clone(leader.ref)
	in["attempt"], in["hand_in"] = 2, leaderHand
	out = handInOut{}
	if code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+leader.id+"/hand-in", l.personal, "", in, &out); code != 200 || out.Accepted || len(out.Problems) == 0 {
		t.Fatalf("stale delegation accepted: %d %+v", code, out)
	}
}
