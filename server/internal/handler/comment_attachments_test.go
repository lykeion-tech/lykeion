package handler

import (
	"testing"

	"github.com/google/uuid"
)

func TestCommentAttachmentsAreScopedAndNotStudyInputs(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	jwt := s.signIn(t, "comment-files@lab.org")
	ws := s.workspace(t, jwt, "comment-files")
	study := s.idleCard(t, jwt, ws.ID, "Comment files")
	file := s.uploadData(t, jwt, ws.ID, "note.txt", []byte("comment evidence"))
	var created struct {
		ID string `json:"id"`
	}
	if code := s.call(t, "POST", "/api/cards/"+study.ID+"/comments", jwt, ws.ID, map[string]any{"body": "", "attachment_ids": []string{file}}, &created); code != 201 {
		t.Fatalf("file-only comment: %d", code)
	}
	var comments []struct {
		Attachments []struct {
			ID string `json:"id"`
		} `json:"attachments"`
	}
	if code := s.call(t, "GET", "/api/cards/"+study.ID+"/comments", jwt, ws.ID, nil, &comments); code != 200 || len(comments) != 1 || len(comments[0].Attachments) != 1 || comments[0].Attachments[0].ID != file {
		t.Fatalf("comment attachments: %d %+v", code, comments)
	}
	if n := s.count(t, `SELECT count(*) FROM attachment WHERE id=$1 AND card_id IS NOT NULL`, file); n != 0 {
		t.Fatalf("comment file became study input: %d", n)
	}
	if code := s.call(t, "POST", "/api/cards/"+study.ID+"/attachments", jwt, ws.ID, map[string]any{"attachment_ids": []string{file}}, nil); code == 200 {
		t.Fatal("comment attachment reused as pending upload")
	}
	path := "/api/cards/" + study.ID + "/comments/" + created.ID + "/attachments/" + file
	if code := s.call(t, "GET", path, jwt, ws.ID, nil, nil); code != 200 {
		t.Fatalf("download: %d", code)
	}
	other := s.signIn(t, "outside-comment@lab.org")
	otherWS := s.workspace(t, other, "outside")
	if code := s.call(t, "GET", path, other, otherWS.ID, nil, nil); code != 404 {
		t.Fatalf("foreign download: %d", code)
	}
	otherStudy := s.idleCard(t, jwt, ws.ID, "Other study")
	if code := s.call(t, "GET", "/api/cards/"+otherStudy.ID+"/comments/"+created.ID+"/attachments/"+file, jwt, ws.ID, nil, nil); code != 404 {
		t.Fatalf("wrong study: %d", code)
	}
	if code := s.call(t, "POST", "/api/cards/"+study.ID+"/comments", jwt, ws.ID, map[string]any{"body": "again", "attachment_ids": []string{file}}, nil); code == 201 {
		t.Fatal("reused attachment")
	}

}

func TestCommentAttachmentRoleGrantRequiresRecipientReservation(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	jwt, ws, study, run := s.roleFixture(t)
	f := s.uploadData(t, jwt, ws, "data.csv", []byte("a,b\n1,2\n"))
	var c struct {
		ID string `json:"id"`
	}
	if code := s.call(t, "POST", "/api/cards/"+study.ID+"/comments", jwt, ws, map[string]any{"body": "see data", "attachment_ids": []string{f}}, &c); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if code, _ := s.blobRequest(t, "GET", "/api/role/attachments/"+f, run.token, nil); code != 404 {
		t.Fatalf("ungranted read: %d", code)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO run_comment_snapshot(workspace_id,agent_run_id,comment_id,ordinal,section_start,section_end) VALUES($1,$2,$3,0,0,0)`, ws, run.id, c.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := s.blobRequest(t, "GET", "/api/role/attachments/"+f, run.token, nil); code != 404 {
		t.Fatalf("snapshot alone granted another recipient's file: %d", code)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `INSERT INTO comment_reservation(workspace_id,agent_run_id,comment_id,attempt) SELECT $1,$2,$3,attempt FROM agent_run WHERE workspace_id=$1 AND id=$2`, ws, run.id, c.ID); err != nil {
		t.Fatal(err)
	}
	if code, body := s.blobRequest(t, "GET", "/api/role/attachments/"+f, run.token, nil); code != 200 || string(body) != "a,b\n1,2\n" {
		t.Fatalf("granted read %d %q", code, body)
	}
}

func TestLiveCommentAttachmentIsDeferred(t *testing.T) {
	l, run := groundingLab(t)
	f := l.s.uploadData(t, l.jwt, l.ws, "sample.txt", []byte("data"))
	var comment struct {
		ID string `json:"id"`
	}
	if code := l.s.call(t, "POST", "/api/cards/"+l.card.ID+"/comments", l.jwt, l.ws, map[string]any{"body": "see file", "attachment_ids": []string{f}}, &comment); code != 201 {
		t.Fatal(code)
	}
	for range 2 {
		if code, got := l.runComments(t, run); code != 200 || len(got) != 0 {
			t.Fatalf("live file must remain queued: %d %+v", code, got)
		}
	}
	if n := l.receipts(t, comment.ID); n != 0 {
		t.Fatalf("deferred comment got receipt: %d", n)
	}
	if code, _ := l.s.blobRequest(t, "GET", "/api/role/attachments/"+f, run.token, nil); code != 404 {
		t.Fatalf("unreserved read: %d", code)
	}
}

func TestMemberResultCommentFileIsDeferred(t *testing.T) {
	l, leader := groundingLab(t)
	_, ask := l.ask(t, leader.token, "member-1", "Find evidence.")
	l.claim(t)
	f := l.s.uploadData(t, l.jwt, l.ws, "sample.txt", []byte("data"))
	var comment struct {
		ID string `json:"id"`
	}
	if code := l.s.call(t, "POST", "/api/cards/"+l.card.ID+"/comments", l.jwt, l.ws, map[string]any{"body": "see file", "attachment_ids": []string{f}}, &comment); code != 201 {
		t.Fatal(code)
	}
	for range 2 {
		if code, out := l.result(t, leader.token, ask.Ticket); code != 200 || len(out.Comments) != 0 {
			t.Fatalf("member result must defer file: %d %+v", code, out)
		}
	}
	if n := l.receipts(t, comment.ID); n != 0 {
		t.Fatalf("deferred comment got receipt: %d", n)
	}
	if code, _ := l.s.blobRequest(t, "GET", "/api/role/attachments/"+f, leader.token, nil); code != 404 {
		t.Fatalf("unreserved read: %d", code)
	}
}

func TestTargetedReplyFileNotGivenToAnotherRun(t *testing.T) {
	l, old := groundingLab(t)
	if _, err := l.s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='completed', agent_id=NULL WHERE id=$1`, old.id); err != nil {
		t.Fatal(err)
	}
	file := l.s.uploadData(t, l.jwt, l.ws, "private.txt", []byte("private"))
	var reply struct {
		ID string `json:"id"`
	}
	if code := l.s.call(t, "POST", "/api/cards/"+l.card.ID+"/comments", l.jwt, l.ws, map[string]any{"body": "private reply", "target_run_id": old.id, "attachment_ids": []string{file}}, &reply); code != 201 {
		t.Fatal(code)
	}
	l.comment(t, "Continue the study")
	if err := l.s.H.Engine.Advance(t.Context(), uuid.MustParse(l.ws), uuid.MustParse(l.card.ID)); err != nil {
		t.Fatal(err)
	}
	next := l.claim(t)
	var launch struct {
		Attachments []struct {
			ID string `json:"id"`
		} `json:"attachments"`
	}
	if code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+next.id+"/comments/launch", l.personal, "", next.ref, &launch); code != 200 {
		t.Fatalf("launch %d", code)
	}
	if len(launch.Attachments) != 0 {
		t.Fatalf("targeted reply leaked: %+v", launch.Attachments)
	}
	if code, _ := l.s.blobRequest(t, "GET", "/api/role/attachments/"+file, next.token, nil); code != 404 {
		t.Fatalf("sibling read %d", code)
	}
	if n := l.receipts(t, reply.ID); n != 0 {
		t.Fatalf("wrong recipient receipt %d", n)
	}
}

func TestTargetedReplyFileFollowsSameAgent(t *testing.T) {
	l, old := groundingLab(t)
	if _, err := l.s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='completed' WHERE id=$1`, old.id); err != nil {
		t.Fatal(err)
	}
	file := l.s.uploadData(t, l.jwt, l.ws, "same-agent.txt", []byte("data"))
	var reply struct {
		ID string `json:"id"`
	}
	if code := l.s.call(t, "POST", "/api/cards/"+l.card.ID+"/comments", l.jwt, l.ws, map[string]any{"body": "private reply", "target_run_id": old.id, "attachment_ids": []string{file}}, &reply); code != 201 {
		t.Fatal(code)
	}
	l.comment(t, "Continue the study")
	if err := l.s.H.Engine.Advance(t.Context(), uuid.MustParse(l.ws), uuid.MustParse(l.card.ID)); err != nil {
		t.Fatal(err)
	}
	next := l.claim(t)
	var launch struct {
		Attachments []struct {
			ID string `json:"id"`
		} `json:"attachments"`
	}
	if code := l.s.call(t, "POST", "/api/daemon/agent-runs/"+next.id+"/comments/launch", l.personal, "", next.ref, &launch); code != 200 {
		t.Fatalf("launch %d", code)
	}
	if len(launch.Attachments) != 1 || launch.Attachments[0].ID != file {
		t.Fatalf("same agent missed attachment: %+v", launch)
	}
	if code, data := l.s.blobRequest(t, "GET", "/api/role/attachments/"+file, next.token, nil); code != 200 || string(data) != "data" {
		t.Fatalf("recipient read %d %q", code, data)
	}
	if n := l.receipts(t, reply.ID); n != 0 {
		t.Fatalf("premature receipt %d", n)
	}
}
