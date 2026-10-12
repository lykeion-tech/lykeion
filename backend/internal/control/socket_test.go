package control

import "testing"

func TestStrictFrameRejectsAmbiguousPayloads(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"cursor":"ok","unknown":1}`, `{"cursor":"ok"} {}`, `{"cursor":"ok"} true`} {
		var payload struct {
			Cursor string `json:"cursor"`
		}
		err := strictFrame([]byte(raw), &payload)
		if raw == `{}` { // Required fields are checked by the frame-specific handler.
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	var payload struct {
		Cursor string `json:"cursor"`
	}
	if err := strictFrame([]byte("{\"cursor\":\"ok\"}\n"), &payload); err != nil || payload.Cursor != "ok" {
		t.Fatalf("valid payload rejected: %v", err)
	}
}
