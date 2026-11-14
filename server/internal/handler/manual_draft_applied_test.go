package handler

import (
	"github.com/google/uuid"
	"testing"
)

func TestManualDraftAppliedMarkerContract(t *testing.T) {
	f := draftForCancel(t, "queued")
	body := map[string]any{"title": "edited", "context": "", "deadline": nil, "card_id": nil, "applied_message_id": nil}
	var out map[string]any
	if code := f.s.call(t, "PUT", labChats+"/"+f.chat+"/draft", f.a, f.ws, body, &out); code != 200 {
		t.Fatal(code)
	}
	if _, ok := out["applied_message_id"]; !ok {
		t.Fatal("missing durable application marker", out)
	}
	for _, id := range []string{uuid.NewString(), f.question} {
		body["applied_message_id"] = id
		if code := f.s.call(t, "PUT", labChats+"/"+f.chat+"/draft", f.a, f.ws, body, nil); code != 404 {
			t.Fatal(code)
		}
	}
	body["applied_message_id"] = nil
	for _, x := range []struct{ token, ws string }{{f.b, f.ws}, {f.a, f.s.workspace(t, f.a, "marker-other").ID}} {
		if code := f.s.call(t, "PUT", labChats+"/"+f.chat+"/draft", x.token, x.ws, body, nil); code != 404 {
			t.Fatal(code)
		}
	}
	if code := f.s.call(t, "POST", labChats+"/"+f.chat+"/archive", f.a, f.ws, nil, nil); code != 204 {
		t.Fatal(code)
	}
	if code := f.s.call(t, "PUT", labChats+"/"+f.chat+"/draft", f.a, f.ws, body, nil); code != 400 {
		t.Fatal(code)
	}
}
