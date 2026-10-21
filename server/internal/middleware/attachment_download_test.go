package middleware

import (
	"net/http/httptest"
	"testing"
)

func TestOnlyAStudyFileDownloadGetsTheLongBudget(t *testing.T) {
	id := "11111111-1111-1111-1111-111111111111"
	for _, c := range []struct {
		method, path string
		want         bool
	}{
		{"GET", "/api/cards/" + id + "/attachments/" + id, true},
		{"POST", "/api/cards/" + id + "/attachments/" + id, false},
		{"GET", "/api/cards/" + id + "/comments", false},
		{"GET", "/api/cards/" + id, false},
	} {
		if got := attachmentDownload(httptest.NewRequest(c.method, c.path, nil)); got != c.want {
			t.Errorf("%s %s: %v, want %v", c.method, c.path, got, c.want)
		}
	}
}
