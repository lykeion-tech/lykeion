package handler

import (
	"fmt"
	"lykeion/server/internal/testutil"
	"strings"
	"testing"
	"time"
)

func TestStudyDescriptionControls(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	a := s.signIn(t, "controls@lab.org")
	w := s.workspace(t, a, "controls")
	first := s.uploadData(t, a, w.ID, "data.csv", []byte("first"))
	var c card
	if code := s.call(t, "POST", "/api/cards", a, w.ID, map[string]any{"question": "What does this data show?", "attachment_ids": []string{first}}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	second := s.uploadData(t, a, w.ID, "data.csv", []byte("second"))
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/attachments", a, w.ID, map[string]any{"attachment_ids": []string{second}}, nil); code != 200 {
		t.Fatalf("attach: %d", code)
	}
	var disk, key string
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT disk_name, record_key FROM attachment WHERE id=$1 AND card_id=$2`, second, c.ID).Scan(&disk, &key); err != nil {
		t.Fatal(err)
	}
	if disk != "data (2).csv" || key != "c2" {
		t.Fatalf("attachment provenance: %q %q", disk, key)
	}
	if n := s.count(t, `SELECT count(*) FROM record WHERE card_id=$1 AND key='c2' AND kind='call'`, c.ID); n != 1 {
		t.Fatalf("source records: %d", n)
	}
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/attachments", a, w.ID, map[string]any{"attachment_ids": []string{second}}, nil); code == 200 {
		t.Fatal("linked upload reused")
	}
	path := "/api/cards/" + c.ID + "/reactions"
	if code := s.call(t, "PUT", path, a, w.ID, map[string]any{"emoji": "👍"}, nil); code != 200 {
		t.Fatalf("react: %d", code)
	}
	if code := s.call(t, "PUT", path, a, w.ID, map[string]any{"emoji": "👍"}, nil); code != 200 {
		t.Fatalf("repeat react: %d", code)
	}
	var reactions []struct {
		Emoji string `json:"emoji"`
		Count int    `json:"count"`
		Mine  bool   `json:"mine"`
	}
	if code := s.call(t, "GET", path, a, w.ID, nil, &reactions); code != 200 || len(reactions) != 1 || reactions[0].Count != 1 || !reactions[0].Mine {
		t.Fatalf("reactions: %d %+v", code, reactions)
	}
	if code := s.call(t, "DELETE", path, a, w.ID, map[string]any{"emoji": "👍"}, nil); code != 200 {
		t.Fatalf("unreact: %d", code)
	}
	if code := s.call(t, "GET", path, a, w.ID, nil, &reactions); code != 200 || len(reactions) != 0 {
		t.Fatalf("removed: %d %+v", code, reactions)
	}
	other := s.signIn(t, "outside@lab.org")
	otherWS := s.workspace(t, other, "outside")
	if code := s.call(t, "GET", path, other, otherWS.ID, nil, nil); code != 404 {
		t.Fatalf("other workspace read reactions: %d", code)
	}
	if code := s.call(t, "PUT", path, other, otherWS.ID, map[string]any{"emoji": "👍"}, nil); code != 404 {
		t.Fatalf("other workspace reacted: %d", code)
	}
	foreign := s.uploadData(t, other, otherWS.ID, "foreign.txt", []byte("private"))
	if code := s.call(t, "POST", "/api/cards/"+c.ID+"/attachments", other, otherWS.ID, map[string]any{"attachment_ids": []string{foreign}}, nil); code != 404 {
		t.Fatalf("other workspace attached: %d", code)
	}
}

func TestRunReactionsAreScopedToRunAndStudy(t *testing.T) {
	l, run := groundingLab(t)
	path := "/api/cards/" + l.card.ID + "/runs/" + run.id + "/reactions"
	if code := l.s.call(t, "PUT", path, l.jwt, l.ws, map[string]any{"emoji": "👍"}, nil); code != 200 {
		t.Fatalf("add reaction: %d", code)
	}
	var got []struct {
		Emoji string `json:"emoji"`
		Count int    `json:"count"`
		Mine  bool   `json:"mine"`
	}
	if code := l.s.call(t, "GET", path, l.jwt, l.ws, nil, &got); code != 200 || len(got) != 1 || got[0].Emoji != "👍" || got[0].Count != 1 || !got[0].Mine {
		t.Fatalf("list: %d %+v", code, got)
	}
	var study []any
	if code := l.s.call(t, "GET", "/api/cards/"+l.card.ID+"/reactions", l.jwt, l.ws, nil, &study); code != 200 || len(study) != 0 {
		t.Fatalf("study reactions: %d %+v", code, study)
	}
	other := l.s.idleCard(t, l.jwt, l.ws, "Unrelated study")
	if code := l.s.call(t, "GET", "/api/cards/"+other.ID+"/runs/"+run.id+"/reactions", l.jwt, l.ws, nil, nil); code != 404 {
		t.Fatalf("wrong study: %d", code)
	}
	if code := l.s.call(t, "DELETE", path, l.jwt, l.ws, map[string]any{"emoji": "👍"}, nil); code != 200 {
		t.Fatalf("remove: %d", code)
	}
	if code := l.s.call(t, "GET", path, l.jwt, l.ws, nil, &got); code != 200 || len(got) != 0 {
		t.Fatalf("removed: %d %+v", code, got)
	}
}

func TestStudyAttachmentOnlyReachesSubsequentRuns(t *testing.T) {
	l := newTeamLabWith(t, testutil.PackOpts{Name: "snapshot", Stage: "hypotheses", Members: 2})
	// The old run must exist before the attachment, or its snapshot holds it.
	for deadline := time.Now().Add(5 * time.Second); l.queued(t) == 0; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the study's first run was never queued")
		}
	}
	file := l.s.uploadData(t, l.jwt, l.ws, "new.csv", []byte("new input"))
	if code := l.s.call(t, "POST", "/api/cards/"+l.card.ID+"/attachments", l.jwt, l.ws, map[string]any{"attachment_ids": []string{file}}, nil); code != 200 {
		t.Fatalf("attach: %d", code)
	}
	old := l.claim(t)
	var oldRecords []map[string]any
	if code := l.s.call(t, "GET", "/api/role/records?column=hypotheses", old.token, "", nil, &oldRecords); code != 200 || strings.Contains(fmt.Sprint(oldRecords), "new input") {
		t.Fatalf("old run saw new source: %d %+v", code, oldRecords)
	}
	var oldCalls []map[string]any
	if code := l.s.call(t, "GET", "/api/role/calls", old.token, "", nil, &oldCalls); code != 200 || strings.Contains(fmt.Sprint(oldCalls), "new input") {
		t.Fatalf("old run saw new call: %d %+v", code, oldCalls)
	}
	if code, _ := l.s.blobRequest(t, "GET", "/api/role/attachments/"+file, old.token, nil); code != 404 {
		t.Fatalf("old run accessed new file: %d", code)
	}
	code, asked := l.ask(t, old.token, "member-1", "Examine the new input")
	if code != 201 {
		t.Fatalf("ask: %d %+v", code, asked)
	}
	next := l.claim(t)
	var newRecords []map[string]any
	if code := l.s.call(t, "GET", "/api/role/records?column=hypotheses", next.token, "", nil, &newRecords); code != 200 || !strings.Contains(fmt.Sprint(newRecords), "new input") {
		t.Fatalf("new run cannot see source: %d %+v", code, newRecords)
	}
	if code, body := l.s.blobRequest(t, "GET", "/api/role/attachments/"+file, next.token, nil); code != 200 || string(body) != "new input" {
		t.Fatalf("new run file: %d %q", code, body)
	}
}
