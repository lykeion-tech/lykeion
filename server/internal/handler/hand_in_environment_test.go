package handler

import (
	"maps"
	"testing"
)

func TestDaemonHandInAttestsEnvironmentFailure(t *testing.T) {
	l, leader := groundingLab(t)
	hand := grounding()
	hand["status"] = "could_not_do"
	hand["reason"] = "Python could not start"
	hand["blocked_by_environment"] = "python"
	in := maps.Clone(leader.ref)
	in["hand_in"] = hand
	in["environment_failures"] = []string{"python"}
	var out handInOut
	path := "/api/daemon/agent-runs/" + leader.id + "/hand-in"
	if code := l.s.call(t, "POST", path, l.personal, "", in, &out); code != 200 || !out.Accepted {
		t.Fatalf("matching daemon evidence should be accepted: status=%d result=%+v", code, out)
	}
	if n := l.s.count(t, `SELECT count(*) FROM record WHERE agent_run_id=$1 AND kind='hand_in' AND body->'environment_failures' @> '["python"]'::jsonb`, leader.id); n != 1 {
		t.Fatalf("trusted attempt-scoped failure evidence was not recorded: %d", n)
	}
	if code := l.s.call(t, "POST", path, l.personal, "", in, &out); code != 200 || !out.Accepted {
		t.Fatalf("replay should be idempotent: status=%d result=%+v", code, out)
	}
	if n := l.s.count(t, `SELECT count(*) FROM record WHERE agent_run_id=$1 AND kind='hand_in'`, leader.id); n != 1 {
		t.Fatalf("replay wrote %d hand-ins", n)
	}
}

func TestDaemonHandInRejectsUncorroboratedEnvironmentFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		evidence []string
	}{
		{"no evidence", nil},
		{"different language", []string{"r"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, leader := groundingLab(t)
			hand := grounding()
			hand["status"] = "could_not_do"
			hand["reason"] = "Python could not start"
			hand["blocked_by_environment"] = "python"
			in := maps.Clone(leader.ref)
			in["hand_in"] = hand
			in["environment_failures"] = tc.evidence
			var out handInOut
			path := "/api/daemon/agent-runs/" + leader.id + "/hand-in"
			if code := l.s.call(t, "POST", path, l.personal, "", in, &out); code != 200 || out.Accepted || len(out.Problems) == 0 {
				t.Fatalf("unverified blocker should be rejected for correction: status=%d result=%+v", code, out)
			}
			if n := l.kinds(t, "hand_in"); n != 0 {
				t.Fatalf("uncorroborated blocker wrote %d hand-ins", n)
			}
		})
	}
}

func TestDaemonHandInAcceptsLegacyResearchBlocker(t *testing.T) {
	l, leader := groundingLab(t)
	hand := grounding()
	hand["status"] = "could_not_do"
	hand["reason"] = "No qualifying research dataset was available"
	if out := l.handIn(t, leader, hand); !out.Accepted {
		t.Fatalf("legacy scientific blocker should remain valid: %+v", out)
	}
}

func TestDaemonHandInRejectsInvalidEnvironmentClaim(t *testing.T) {
	for _, tc := range []struct {
		name, status, blocker string
		evidence              []string
	}{
		{"unknown language", "could_not_do", "ruby", []string{"ruby"}},
		{"done is not blocked", "done", "python", []string{"python"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, leader := groundingLab(t)
			hand := grounding()
			hand["status"] = tc.status
			hand["reason"] = "blocked"
			hand["blocked_by_environment"] = tc.blocker
			in := maps.Clone(leader.ref)
			in["hand_in"] = hand
			in["environment_failures"] = tc.evidence
			var out handInOut
			path := "/api/daemon/agent-runs/" + leader.id + "/hand-in"
			code := l.s.call(t, "POST", path, l.personal, "", in, &out)
			if code == 200 && out.Accepted {
				t.Fatalf("invalid environment claim accepted: %+v", out)
			}
			if n := l.kinds(t, "hand_in"); n != 0 {
				t.Fatalf("invalid claim wrote %d hand-ins", n)
			}
		})
	}
}

func TestDaemonHandInCannotAttestAnotherAttempt(t *testing.T) {
	l, leader := groundingLab(t)
	hand := grounding()
	hand["status"] = "could_not_do"
	hand["reason"] = "Python could not start"
	hand["blocked_by_environment"] = "python"
	in := maps.Clone(leader.ref)
	in["attempt"] = 2
	in["hand_in"] = hand
	in["environment_failures"] = []string{"python"}
	var out handInOut
	path := "/api/daemon/agent-runs/" + leader.id + "/hand-in"
	if code := l.s.call(t, "POST", path, l.personal, "", in, &out); code == 200 && out.Accepted {
		t.Fatalf("evidence from another attempt was accepted: %+v", out)
	}
	if n := l.kinds(t, "hand_in"); n != 0 {
		t.Fatalf("wrong attempt wrote %d hand-ins", n)
	}
}
