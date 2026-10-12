package control

import (
	"net/http/httptest"
	"testing"
)

func TestRequestLimitSharedAndBounded(t *testing.T) {
	api, db, _, _, _, _ := fixture(t)
	other := &Server{DB: db}
	request := httptest.NewRequest("GET", "/", nil)
	for i := 0; i < 3; i++ {
		if !api.allowRequest(httptest.NewRecorder(), request, "synthetic-limit", 3) {
			t.Fatal("early limit")
		}
	}
	response := httptest.NewRecorder()
	if other.allowRequest(response, request, "synthetic-limit", 3) || response.Code != 429 || response.Header().Get("Retry-After") == "" {
		t.Fatal("cross-instance limit missing")
	}
	if !other.allowRequest(httptest.NewRecorder(), request, "different-account", 3) {
		t.Fatal("unrelated account limited")
	}
}
