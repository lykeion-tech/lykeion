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
		{"GET", "/api/cards/" + id + "/attachments/not-a-uuid", false},
		{"GET", "/api/cards/x/attachments/" + id, false},
		{"GET", "/api/cards/" + id + "/comments", false},
		{"GET", "/api/cards/" + id, false},
		{"GET", "/api/role/attachments/" + id, true},
		{"POST", "/api/role/attachments/" + id, false},
		{"GET", "/api/role/attachments/x", false},
		{"GET", "/api/role/blobs/" + id, false},
		{"GET", "/api/cards/" + id + "/outputs/o1", true},
		{"POST", "/api/cards/" + id + "/outputs/o1", false},
		{"GET", "/api/cards/x/outputs/o1", false},
		{"GET", "/api/cards/" + id + "/outputs/", false},
		{"GET", "/api/role/outputs/o1", true},
		{"GET", "/api/role/outputs/", false},
		{"POST", "/api/role/outputs/o1", false},
	} {
		if got := attachmentDownload(httptest.NewRequest(c.method, c.path, nil)); got != c.want {
			t.Errorf("%s %s: %v, want %v", c.method, c.path, got, c.want)
		}
	}
}
