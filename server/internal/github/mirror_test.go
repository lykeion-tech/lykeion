package github

import (
	"testing"
	"time"
)

func TestGithubMirrorPRState(t *testing.T) {
	merged := time.Now()
	for _, tc := range []struct {
		p    PullRequest
		want string
	}{
		{PullRequest{State: "open"}, "open"},
		{PullRequest{State: "open", Draft: true}, "draft"},
		{PullRequest{State: "closed", Draft: true}, "closed"},
		{PullRequest{State: "closed", Draft: true, Merged: true}, "merged"},
		{PullRequest{State: "closed", MergedAt: &merged}, "merged"},
	} {
		if got := tc.p.state(); got != tc.want {
			t.Errorf("%+v: got %s, want %s", tc.p, got, tc.want)
		}
	}
}
