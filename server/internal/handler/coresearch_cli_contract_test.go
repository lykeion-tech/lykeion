package handler

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"lykeion/server/internal/coresearch"
	db "lykeion/server/pkg/db/generated"
)

func TestCoresearchRunOutputsKeepOriginalVersions(t *testing.T) {
	s, a, _, _, ws, p := csFixture(t)
	chatPath, chat := csChat(t, s, a, ws, p.ID)
	if code, _ := csUpload(t, s, a, ws, p.ID, "main.py", 0, false, []byte("1")); code != 201 {
		t.Fatal(code)
	}
	ref := csRegister(t, s, a, ws, "work", "claude")
	token := csRunToken(t, s, a, ws, chatPath, ref)
	var run db.CoresearchRun
	projectRuns := csProjects + "/" + p.ID + "/runs"
	if code := s.call(t, "POST", projectRuns, a, ws, map[string]any{"chat_id": chat.ID, "script": "main.py"}, &run); code != 201 {
		t.Fatal(code)
	}
	claimed := csWork(t, s, a, "work", ref)
	if len(claimed.Runs) != 1 {
		t.Fatalf("claimed %+v", claimed)
	}
	old := []byte{0, 1, 2}
	if code := csFinishRun(t, s, s.personalToken(t, a), ws, run.ID.String(), map[string]any{"status": "ok", "env_sha": strings.Repeat("a", 64)}, map[string][]byte{"fig.png": old}); code != 204 {
		t.Fatal(code)
	}
	// Advance the file head with identical bytes too: hashes alone cannot identify
	// the original run's version after another save or restore.
	if code, _ := csUpload(t, s, a, ws, p.ID, "results/fig.png", 1, false, old); code != 201 {
		t.Fatal(code)
	}
	if code, _ := csUpload(t, s, a, ws, p.ID, "results/fig.png", 2, false, []byte{0, 3, 4}); code != 201 {
		t.Fatal(code)
	}
	for _, request := range []struct{ endpoint, token, workspace string }{{projectRuns + "/" + run.Key, a, ws}, {"/api/run/coresearch/runs/" + run.Key, token, ""}} {
		var out struct {
			db.CoresearchRun
			Outputs []coresearch.FileRef `json:"outputs"`
		}
		if code := s.call(t, "GET", request.endpoint, request.token, request.workspace, nil, &out); code != 200 {
			t.Fatal(code)
		}
		if len(out.Outputs) != 1 || out.Outputs[0].N != 1 || out.Outputs[0].Path != "results/fig.png" || out.Outputs[0].SHA256 != fmt.Sprintf("%x", sha256.Sum256(old)) || out.Outputs[0].Size != 3 {
			t.Fatalf("run output refs %+v", out.Outputs)
		}
	}
}
