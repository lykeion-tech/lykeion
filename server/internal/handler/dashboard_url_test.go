package handler

import "testing"

func TestDashboardURLFollowsPublicURL(t *testing.T) {
	for publicURL, want := range map[string]string{
		"":                            "/",
		"https://lykeion.example/app": "https://lykeion.example/app/",
	} {
		if got := (&H{PublicURL: publicURL}).dashboardURL(); got != want {
			t.Errorf("PublicURL %q: dashboardURL() = %q, want %q", publicURL, got, want)
		}
	}
}
