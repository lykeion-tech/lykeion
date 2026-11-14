package service

import "testing"

func TestTheAuditorWritesOnlyAudits(t *testing.T) {
	for _, tc := range []struct {
		role, kind string
		ok         bool
	}{
		{"auditor", "audit", true}, {"auditor", "note", false}, {"auditor", "score", false},
		{"member-1", "audit", false}, {"member-1", "note", true}, {"leader", "note", true}, {"agent", "summary", false},
	} {
		if err := MayWrite(tc.role, tc.kind); (err == nil) != tc.ok {
			t.Errorf("%s writes %s: %v", tc.role, tc.kind, err)
		}
	}
}
