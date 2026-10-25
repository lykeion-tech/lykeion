package handler

import (
	"fmt"
	"strings"
	"testing"
)

func TestPythonRouteSetsOnce(t *testing.T) {
	l := newTeamLab(t, 0)
	r := l.claim(t)
	for _, version := range []string{"3.11", "3.11"} {
		if c := l.s.call(t, "PUT", "/api/role/python", r.callToken, "", map[string]string{"version": version}, nil); c != 200 {
			t.Fatalf("set %s: %d", version, c)
		}
	}
	var out map[string]any
	if c := l.s.call(t, "PUT", "/api/role/python", r.callToken, "", map[string]string{"version": "3.12"}, &out); c != 409 || !strings.Contains(strings.TrimSpace(fmt.Sprint(out)), "This study uses Python 3.11; a study keeps one Python version.") {
		t.Fatalf("change %d %v", c, out)
	}
	if c := l.s.call(t, "PUT", "/api/role/python", r.token, "", map[string]string{"version": "3.11"}, nil); c != 403 {
		t.Fatalf("agent token %d", c)
	}
	for _, v := range []string{"3.8", "3.15", "3.11.1", "--help", ""} {
		if c := l.s.call(t, "PUT", "/api/role/python", r.callToken, "", map[string]string{"version": v}, nil); c != 400 {
			t.Fatalf("invalid %q: %d", v, c)
		}
	}
}

func TestConcurrentPythonChoicesKeepOneVersion(t *testing.T) {
	l := newTeamLab(t, 0)
	r := l.claim(t)
	start := make(chan struct{})
	results := make(chan int, 2)
	for _, v := range []string{"3.11", "3.12"} {
		go func(v string) {
			<-start
			results <- l.s.call(t, "PUT", "/api/role/python", r.callToken, "", map[string]string{"version": v}, nil)
		}(v)
	}
	close(start)
	a, b := <-results, <-results
	if !((a == 200 && b == 409) || (a == 409 && b == 200)) {
		t.Fatalf("concurrent choices: %d %d", a, b)
	}
	var n int
	if err := l.s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM record WHERE card_id=$1 AND kind='python_version'`, l.card.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("records %d: %v", n, err)
	}
}
