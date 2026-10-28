package handler

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"lykeion/server/internal/coresearch"
	"lykeion/server/internal/coresearch/doc"
	db "lykeion/server/pkg/db/generated"
	"strings"
	"testing"
)

func TestKeepUndoMemberRoutes(t *testing.T) {
	s, a, b, ws, p, f := paperFixture(t)
	root := csProjects + "/" + p.ID
	url := root + "/docs/" + f
	s.call(t, "GET", url, a, ws, nil, nil)
	var proposal, id string
	if e := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO coresearch_proposal(workspace_id,project_id,created_by) VALUES($1,$2,$3) RETURNING id`, ws, p.ID, s.userID(t, a)).Scan(&proposal); e != nil {
		t.Fatal(e)
	}
	if e := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO coresearch_change(workspace_id,proposal_id,file_id,from_pos,to_pos,removed_text,status) VALUES($1,$2,$3,1,3,'old','pending') RETURNING id`, ws, proposal, f).Scan(&id); e != nil {
		t.Fatal(e)
	}
	if c := s.call(t, "POST", root+"/changes/"+id+"/undo", b, ws, nil, nil); c != 200 {
		t.Fatalf("undo %d", c)
	}
	var d doc.Doc
	s.call(t, "GET", url, a, ws, nil, &d)
	if d.Text != "aold\r\nb" || d.Version != 1 {
		t.Fatalf("undo %+v", d)
	}
	var author string
	s.Env.Owner.QueryRow(t.Context(), `SELECT author_user_id FROM coresearch_doc_step WHERE file_id=$1 AND version=1`, f).Scan(&author)
	if author != s.userID(t, b) {
		t.Fatal("wrong undo author")
	}
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE coresearch_change SET status='pending' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if c := s.call(t, "POST", root+"/changes/"+id+"/keep", b, ws, nil, nil); c != 200 {
		t.Fatal(c)
	}
	s.call(t, "DELETE", root+"/members/"+s.userID(t, b), a, ws, nil, nil)
	for _, suffix := range []string{"/changes/" + id + "/keep", "/changes/" + id + "/undo"} {
		if c := s.call(t, "POST", root+suffix, b, ws, nil, nil); c != 404 {
			t.Fatalf("removed %s %d", suffix, c)
		}
	}
	if c := s.call(t, "GET", url+"/changes?status=pending", b, ws, nil, nil); c != 404 {
		t.Fatal(c)
	}
}
func TestRestoreIsProposal(t *testing.T) {
	s, a, b, ws, p, f := paperFixture(t)
	root := csProjects + "/" + p.ID
	var v db.CoresearchFileVersion
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT n,sha256,size FROM coresearch_file_version WHERE file_id=$1`, f).Scan(&v.N, &v.Sha256, &v.Size); e != nil {
		t.Fatal(e)
	}
	code, notes := csUpload(t, s, a, ws, p.ID, "notes.txt", 0, false, []byte("original notes"))
	if code != 201 {
		t.Fatal(code)
	}
	refs, _ := json.Marshal([]coresearch.FileRef{{Path: "main.tex", FileID: uuid.MustParse(f), N: v.N, SHA256: v.Sha256, Size: v.Size}, {Path: "notes.txt", FileID: notes.FileID, N: notes.N, SHA256: notes.Sha256, Size: notes.Size}})
	if code, _ = csUpload(t, s, a, ws, p.ID, "notes.txt", 1, false, []byte("changed notes")); code != 201 {
		t.Fatal(code)
	}
	if code, _ = csUpload(t, s, a, ws, p.ID, "extra.sty", 0, false, []byte("style")); code != 201 {
		t.Fatal(code)
	}
	if _, e := s.Env.Owner.Exec(t.Context(), `INSERT INTO coresearch_version(workspace_id,project_id,seq,title,files) VALUES($1,$2,1,'one',$3)`, ws, p.ID, refs); e != nil {
		t.Fatal(e)
	}
	c, extra := csUpload(t, s, a, ws, p.ID, "later.tex", 0, false, []byte("later"))
	if c != 201 {
		t.Fatal(c)
	}

	var snapshotRefs []coresearch.FileRef
	json.Unmarshal(refs, &snapshotRefs)
	snapshotRefs = append(snapshotRefs, coresearch.FileRef{Path: "later.tex", FileID: extra.FileID, N: extra.N, SHA256: extra.Sha256, Size: extra.Size})
	laterRefs, _ := json.Marshal(snapshotRefs)
	if _, e := s.Env.Owner.Exec(t.Context(), `INSERT INTO coresearch_version(workspace_id,project_id,seq,title,files) VALUES($1,$2,2,'later',$3)`, ws, p.ID, laterRefs); e != nil {
		t.Fatal(e)
	}
	c, png := csUpload(t, s, a, ws, p.ID, "figure.png", 0, false, []byte{137, 80, 78, 71, 0})
	if c != 201 {
		t.Fatal(c)
	}
	s.call(t, "GET", root+"/docs/"+f, a, ws, nil, nil)
	s.call(t, "POST", root+"/docs/"+f+"/steps", a, ws, map[string]any{"base": 0, "client_id": "a", "steps": []any{[]any{[]any{6, "changed"}}}}, nil)
	var proposal map[string]any
	if c := s.call(t, "POST", root+"/versions/1/restore", b, ws, nil, &proposal); c != 201 {
		t.Fatalf("restore %d %+v", c, proposal)
	}
	var ids []string
	rows, e := s.Env.Owner.Query(t.Context(), `SELECT id FROM coresearch_change WHERE proposal_id=$1 AND status='pending'`, fmt.Sprint(proposal["id"]))
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != 4 {
		t.Fatalf("changes %v", ids)
	}
	for _, id := range ids {
		if c := s.call(t, "POST", root+"/changes/"+id+"/keep", b, ws, nil, nil); c != 200 {
			t.Fatal(c)
		}
	}
	var d doc.Doc
	s.call(t, "GET", root+"/docs/"+f, a, ws, nil, &d)
	if d.Text != "a😀\r\nb" {
		t.Fatalf("restore %+v", d)
	}
	var restoredNotes doc.Doc
	if code := s.call(t, "GET", root+"/docs/"+notes.FileID.String(), a, ws, nil, &restoredNotes); code != 200 || restoredNotes.Text != "original notes" {
		t.Fatalf("text restore %d %+v", code, restoredNotes)
	}
	var files []struct {
		ID uuid.UUID `json:"id"`
	}
	s.call(t, "GET", root+"/files", a, ws, nil, &files)
	foundPNG := false
	for _, file := range files {
		if file.ID == extra.FileID {
			t.Fatal("removed file remains active")
		}
		if file.ID == png.FileID {
			foundPNG = true
		}
	}
	if !foundPNG {
		t.Fatal("binary removed")
	}
	var versions []db.CoresearchVersion
	if c := s.call(t, "GET", root+"/versions", b, ws, nil, &versions); c != 200 || len(versions) != 2 || versions[0].Seq != 2 || versions[1].Seq != 1 {
		t.Fatalf("versions %d %+v", c, versions)
	}

	for _, keep := range []bool{false, true} {
		var restored db.CoresearchProposal
		if c := s.call(t, "POST", root+"/versions/2/restore", b, ws, nil, &restored); c != 201 {
			t.Fatalf("restore hidden %d", c)
		}
		var id, action string
		if e := s.Env.Owner.QueryRow(t.Context(), `SELECT id,file_action FROM coresearch_change WHERE proposal_id=$1 AND file_id=$2`, restored.ID, extra.FileID).Scan(&id, &action); e != nil || action != "restore" {
			t.Fatalf("restore action %s %v", action, e)
		}
		var live doc.Doc
		if c := s.call(t, "GET", root+"/docs/"+extra.FileID.String(), a, ws, nil, &live); c != 200 || live.Text != "later" {
			t.Fatalf("revival %d %+v", c, live)
		}
		op := "undo"
		if keep {
			op = "keep"
		}
		if c := s.call(t, "POST", root+"/changes/"+id+"/"+op, b, ws, nil, nil); c != 200 {
			t.Fatal(c)
		}
		want := 404
		if keep {
			want = 200
		}
		if c := s.call(t, "GET", root+"/docs/"+extra.FileID.String(), a, ws, nil, nil); c != want {
			t.Fatalf("lifecycle %s %d", op, c)
		}
	}
	s.call(t, "DELETE", root+"/members/"+s.userID(t, b), a, ws, nil, nil)
	if c := s.call(t, "GET", root+"/versions", b, ws, nil, nil); c != 404 {
		t.Fatal(c)
	}
}

func TestSelectionAcceptanceReceiptAndClaim(t *testing.T) {
	s, a, _, _, ws, otherProject := csFixture(t)
	ref := csRegister(t, s, a, ws, "selection", "claude")
	var accepted struct {
		Conversation db.CoresearchConversation
		Run          db.AgentRun
	}
	if c := s.call(t, "POST", "/api/coresearch/conversations", a, ws, map[string]any{"body": "start", "mode": "cowrite", "request_id": uuid.NewString()}, &accepted); c != 201 {
		t.Fatal(c)
	}
	s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='cancelled' WHERE id=$1`, accepted.Run.ID)
	root := csProjects + "/" + accepted.Conversation.ProjectID.String()
	var file string
	s.Env.Owner.QueryRow(t.Context(), `SELECT id FROM coresearch_file WHERE project_id=$1 AND path='main.tex'`, accepted.Conversation.ProjectID).Scan(&file)
	var d doc.Doc
	s.call(t, "GET", root+"/docs/"+file, a, ws, nil, &d)
	send := root + "/chats/" + accepted.Conversation.ChatID.String() + "/turns"
	selection := map[string]any{"file_id": file, "version": 0, "from": 0, "to": 1}
	input := map[string]any{"body": "edit selection", "request_id": uuid.NewString(), "selection": selection}
	var result struct{ Run db.AgentRun }
	if c := s.call(t, "POST", send, a, ws, input, &result); c != 201 {
		t.Fatal(c)
	}
	if !strings.Contains(result.Run.Prompt, "Selection") || !strings.Contains(result.Run.Prompt, "main.tex") {
		t.Fatalf("selection absent %s", result.Run.Prompt)
	}
	claims := csClaim(t, s, a, ref)
	if len(claims) != 1 || claims[0].AgentRun.Prompt != result.Run.Prompt {
		t.Fatalf("claim selection %+v", claims)
	}
	if c := s.call(t, "POST", send, a, ws, input, nil); c != 201 {
		t.Fatal(c)
	}
	selection["to"] = 2
	if c := s.call(t, "POST", send, a, ws, input, nil); c != 409 {
		t.Fatalf("changed selection replay %d", c)
	}
	s.Env.Owner.Exec(t.Context(), `UPDATE agent_run SET status='cancelled' WHERE id=$1`, result.Run.ID)
	input["request_id"] = uuid.NewString()
	selection["to"] = 9999999
	if c := s.call(t, "POST", send, a, ws, input, nil); c != 422 {
		t.Fatalf("invalid selection %d", c)
	}
	input["request_id"] = uuid.NewString()
	code, otherFile := csUpload(t, s, a, ws, otherProject.ID, "foreign.tex", 0, false, []byte("foreign"))
	if code != 201 {
		t.Fatal(code)
	}
	selection["file_id"] = otherFile.FileID.String()
	selection["to"] = 1
	if c := s.call(t, "POST", send, a, ws, input, nil); c != 404 {
		t.Fatalf("foreign selection %d", c)
	}

	raw, _ := json.Marshal(doc.Replace(0, doc.Length(d.Text), "a😀b", doc.Length(d.Text)))
	if code := s.call(t, "POST", root+"/docs/"+file+"/steps", a, ws, map[string]any{"base": 0, "client_id": "a", "steps": []json.RawMessage{raw}}, nil); code != 200 {
		t.Fatal(code)
	}
	selection["file_id"] = file
	selection["version"] = 1
	selection["from"] = 1
	selection["to"] = 2
	input["request_id"] = uuid.NewString()
	if code := s.call(t, "POST", send, a, ws, input, nil); code != 422 {
		t.Fatalf("split surrogate selection %d", code)
	}
	selection["version"] = 0
	selection["from"] = 0
	selection["to"] = 1
	input["request_id"] = uuid.NewString()
	if code := s.call(t, "POST", send, a, ws, input, nil); code != 201 {
		t.Fatalf("historical selection %d", code)
	}
}

func TestFillLiveDocInitialCowrite(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	runtime := csRegister(t, s, a, ws, "fill-paper", "claude")
	var accepted struct {
		Conversation db.CoresearchConversation
		Run          db.AgentRun
	}
	if c := s.call(t, "POST", "/api/coresearch/conversations", a, ws, map[string]any{"body": "start", "mode": "cowrite", "request_id": uuid.NewString()}, &accepted); c != 201 {
		t.Fatal(c)
	}
	claims := csClaim(t, s, a, runtime)
	if len(claims) != 1 {
		t.Fatal(claims)
	}
	endpoint := "/api/daemon/coresearch/chats/" + accepted.Conversation.ChatID.String() + "/files?daemon_id=fill-paper"
	var refs []coresearch.FileRef
	if c := s.call(t, "GET", endpoint, s.personalToken(t, a), ws, nil, &refs); c != 200 {
		t.Fatal(c)
	}
	var main coresearch.FileRef
	for _, ref := range refs {
		if ref.Path == "main.tex" {
			main = ref
		}
	}
	if main.DocVersion == nil || *main.DocVersion != 0 {
		t.Fatalf("first fill must initialize live text: %+v", main)
	}
	root := csProjects + "/" + accepted.Conversation.ProjectID.String() + "/docs/" + main.FileID.String()
	var d doc.Doc
	s.call(t, "GET", root, a, ws, nil, &d)
	raw, _ := json.Marshal(doc.Replace(0, 0, "new text", doc.Length(d.Text)))
	if c := s.call(t, "POST", root+"/steps", a, ws, map[string]any{"base": 0, "client_id": "a", "steps": []json.RawMessage{raw}}, nil); c != 200 {
		t.Fatal(c)
	}
	if c := s.call(t, "GET", endpoint, s.personalToken(t, a), ws, nil, &refs); c != 200 {
		t.Fatal(c)
	}
	for _, ref := range refs {
		if ref.Path == "main.tex" {
			if ref.DocVersion == nil || *ref.DocVersion != 1 || ref.SHA256 == main.SHA256 || ref.N <= main.N {
				t.Fatalf("stale fill %+v", ref)
			}
		}
	}
}

func TestPaperHunkRunTokenScopeAndEmptyFind(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	runtime := csRegister(t, s, a, ws, "paper-cli", "claude")
	var accepted struct {
		Conversation db.CoresearchConversation
		Run          db.AgentRun
	}
	if c := s.call(t, "POST", "/api/coresearch/conversations", a, ws, map[string]any{"body": "start", "mode": "cowrite", "request_id": uuid.NewString()}, &accepted); c != 201 {
		t.Fatal(c)
	}
	claims := csClaim(t, s, a, runtime)
	if len(claims) != 1 {
		t.Fatal(claims)
	}
	runtime.Attempt = claims[0].AgentRun.Attempt
	if e := s.H.AgentRuns.Start(t.Context(), uuid.MustParse(s.userID(t, a)), runtime, claims[0].AgentRun.ID); e != nil {
		t.Fatal(e)
	}
	token := claims[0].Token
	var d doc.Doc
	if c := s.call(t, "GET", "/api/run/coresearch/paper/main.tex", token, "", nil, &d); c != 200 || d.Version != 0 || d.Text == "" {
		t.Fatalf("get %d %+v", c, d)
	}
	if c := s.call(t, "POST", "/api/run/coresearch/paper/edit", token, "", map[string]any{"path": "main.tex", "base": 0, "hunks": []doc.Hunk{{Find: "", Replace: "bad"}}}, nil); c != 422 {
		t.Fatalf("empty find %d", c)
	}
	var out struct {
		Version int64
		Results []doc.HunkResult
	}
	if c := s.call(t, "POST", "/api/run/coresearch/paper/edit", token, "", map[string]any{"path": "main.tex", "base": 0, "hunks": []doc.Hunk{{Find: d.Text, Replace: "paper"}}}, &out); c != 200 || out.Version != 1 || len(out.Results) != 1 || !out.Results[0].Applied {
		t.Fatalf("edit %d %+v", c, out)
	}
	if c := s.call(t, "GET", "/api/run/coresearch/paper/../main.tex", token, "", nil, nil); c == 200 {
		t.Fatal("invalid path accepted")
	}
	if _, e := s.Env.Owner.Exec(t.Context(), `DELETE FROM coresearch_member WHERE project_id=$1 AND user_id=$2`, accepted.Conversation.ProjectID, s.userID(t, a)); e != nil {
		t.Fatal(e)
	}
	if c := s.call(t, "GET", "/api/run/coresearch/paper/main.tex", token, "", nil, nil); c != 404 {
		t.Fatalf("removed get %d", c)
	}
	if c := s.call(t, "POST", "/api/run/coresearch/paper/edit", token, "", map[string]any{"path": "main.tex", "base": 1, "hunks": []doc.Hunk{{Find: "paper", Replace: "bad"}}}, nil); c != 404 {
		t.Fatalf("removed edit %d", c)
	}
}

func TestRestoreDeletedFileCannotOrdinarySave(t *testing.T) {
	s, a, _, ws, p, _ := paperFixture(t)
	c, v := csUpload(t, s, a, ws, p.ID, "image.png", 0, false, []byte{137, 80, 78, 71, 0})
	if c != 201 {
		t.Fatal(c)
	}
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE coresearch_file SET deleted_at=now() WHERE id=$1`, v.FileID); e != nil {
		t.Fatal(e)
	}
	if c, _ := csUpload(t, s, a, ws, p.ID, "image.png", 1, true, []byte{137, 80, 78, 71, 0, 1}); c != 404 {
		t.Fatalf("hidden source must require explicit revival: %d", c)
	}
}
func TestKeepUndoEmitsDecision(t *testing.T) {
	s, a, _, ws, p, f := paperFixture(t)
	root := csProjects + "/" + p.ID
	s.call(t, "GET", root+"/docs/"+f, a, ws, nil, nil)
	var proposal, id string
	s.Env.Owner.QueryRow(t.Context(), `INSERT INTO coresearch_proposal(workspace_id,project_id,created_by) VALUES($1,$2,$3) RETURNING id`, ws, p.ID, s.userID(t, a)).Scan(&proposal)
	s.Env.Owner.QueryRow(t.Context(), `INSERT INTO coresearch_change(workspace_id,proposal_id,file_id,from_pos,to_pos,removed_text,status) VALUES($1,$2,$3,0,0,'','pending') RETURNING id`, ws, proposal, f).Scan(&id)
	if c := s.call(t, "POST", root+"/changes/"+id+"/keep", a, ws, nil, nil); c != 200 {
		t.Fatal(c)
	}
	var payload []byte
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT payload FROM event WHERE workspace_id=$1 AND type='coresearch.change.decided' ORDER BY id DESC LIMIT 1`, ws).Scan(&payload); e != nil {
		t.Fatalf("decision notification missing: %v", e)
	}
	var fields map[string]any
	json.Unmarshal(payload, &fields)
	if len(fields) != 3 || fields["change_id"] != id || fields["file_id"] != f || fields["project_id"] != p.ID {
		t.Fatalf("event must contain ids only: %s", payload)
	}
}

func TestKeepUndoStaleMember(t *testing.T) {
	s, a, b, ws, p, f := paperFixture(t)
	root := csProjects + "/" + p.ID
	url := root + "/docs/" + f
	s.call(t, "GET", url, a, ws, nil, nil)
	var proposal, id string
	s.Env.Owner.QueryRow(t.Context(), `INSERT INTO coresearch_proposal(workspace_id,project_id,created_by) VALUES($1,$2,$3) RETURNING id`, ws, p.ID, s.userID(t, a)).Scan(&proposal)
	s.Env.Owner.QueryRow(t.Context(), `INSERT INTO coresearch_change(workspace_id,proposal_id,file_id,from_pos,to_pos,removed_text,status) VALUES($1,$2,$3,1,3,'original','pending') RETURNING id`, ws, proposal, f).Scan(&id)
	raw, _ := json.Marshal(doc.Replace(1, 3, "changed", 6))
	prefix, _ := json.Marshal(doc.Replace(0, 0, "prefix😀", 11))
	if c := s.call(t, "POST", url+"/steps", a, ws, map[string]any{"base": 0, "client_id": "a", "steps": []json.RawMessage{raw, prefix}}, nil); c != 200 {
		t.Fatal(c)
	}
	if c := s.call(t, "POST", root+"/changes/"+id+"/keep", b, ws, nil, nil); c != 409 {
		t.Fatalf("stale keep %d", c)
	}
	if c := s.call(t, "POST", root+"/changes/"+id+"/undo", b, ws, nil, nil); c != 200 {
		t.Fatalf("stale undo %d", c)
	}
	var d doc.Doc
	s.call(t, "GET", url, a, ws, nil, &d)
	if d.Text != "prefix😀aoriginal\r\nb" || d.Version != 3 {
		t.Fatalf("stale range %+v", d)
	}
}
func TestKeepUndoRemovalBecomesStaleAfterTyping(t *testing.T) {
	s, a, b, ws, p, f := paperFixture(t)
	root := csProjects + "/" + p.ID
	url := root + "/docs/" + f
	s.call(t, "GET", url, a, ws, nil, nil)
	raw, _ := json.Marshal(doc.Replace(0, 6, "", 6))
	if c := s.call(t, "POST", url+"/steps", a, ws, map[string]any{"base": 0, "client_id": "a", "steps": []json.RawMessage{raw}}, nil); c != 200 {
		t.Fatal(c)
	}
	var proposal, id string
	s.Env.Owner.QueryRow(t.Context(), `INSERT INTO coresearch_proposal(workspace_id,project_id,created_by) VALUES($1,$2,$3) RETURNING id`, ws, p.ID, s.userID(t, a)).Scan(&proposal)
	s.Env.Owner.QueryRow(t.Context(), `INSERT INTO coresearch_change(workspace_id,proposal_id,file_id,from_pos,to_pos,removed_text,status,file_action) VALUES($1,$2,$3,0,0,$4,'pending','remove') RETURNING id`, ws, proposal, f, "a😀\r\nb").Scan(&id)
	raw, _ = json.Marshal(doc.Replace(0, 0, "new words", 0))
	if c := s.call(t, "POST", url+"/steps", b, ws, map[string]any{"base": 1, "client_id": "b", "steps": []json.RawMessage{raw}}, nil); c != 200 {
		t.Fatal(c)
	}
	if c := s.call(t, "POST", root+"/changes/"+id+"/keep", a, ws, nil, nil); c != 409 {
		t.Fatalf("removal must not hide newly typed words: %d", c)
	}
	if c := s.call(t, "POST", root+"/changes/"+id+"/undo", b, ws, nil, nil); c != 200 {
		t.Fatal(c)
	}
	var d doc.Doc
	if c := s.call(t, "GET", url, a, ws, nil, &d); c != 200 || d.Text != "new wordsa😀\r\nb" {
		t.Fatalf("removed edit lost %d %+v", c, d)
	}
}

func TestKeepUndoRevivalPreservesOutsideTyping(t *testing.T) {
	s, a, b, ws, p, f := paperFixture(t)
	root := csProjects + "/" + p.ID
	url := root + "/docs/" + f
	s.call(t, "GET", url, a, ws, nil, nil)
	var proposal, id string
	s.Env.Owner.QueryRow(t.Context(), `INSERT INTO coresearch_proposal(workspace_id,project_id,created_by) VALUES($1,$2,$3) RETURNING id`, ws, p.ID, s.userID(t, a)).Scan(&proposal)
	s.Env.Owner.QueryRow(t.Context(), `INSERT INTO coresearch_change(workspace_id,proposal_id,file_id,from_pos,to_pos,removed_text,status,file_action) VALUES($1,$2,$3,0,6,'','pending','restore') RETURNING id`, ws, proposal, f).Scan(&id)
	raw, _ := json.Marshal(doc.Replace(6, 6, "human suffix", 6))
	if c := s.call(t, "POST", url+"/steps", b, ws, map[string]any{"base": 0, "client_id": "b", "steps": []json.RawMessage{raw}}, nil); c != 200 {
		t.Fatal(c)
	}
	if c := s.call(t, "POST", root+"/changes/"+id+"/undo", b, ws, nil, nil); c != 200 {
		t.Fatal(c)
	}
	var d doc.Doc
	if c := s.call(t, "GET", url, a, ws, nil, &d); c != 200 || d.Text != "human suffix" {
		t.Fatalf("revival undo hid outside typing: %d %+v", c, d)
	}
}

func TestFillLiveDocRestoredOrdinaryText(t *testing.T) {
	s, a, _, _, ws, _ := csFixture(t)
	runtime := csRegister(t, s, a, ws, "ordinary-text", "claude")
	var accepted struct {
		Conversation db.CoresearchConversation
		Run          db.AgentRun
	}
	if c := s.call(t, "POST", "/api/coresearch/conversations", a, ws, map[string]any{"body": "start", "mode": "cowrite", "request_id": uuid.NewString()}, &accepted); c != 201 {
		t.Fatal(c)
	}
	if claims := csClaim(t, s, a, runtime); len(claims) != 1 {
		t.Fatal(claims)
	}
	project := accepted.Conversation.ProjectID.String()
	root := csProjects + "/" + project
	c, notes := csUpload(t, s, a, ws, project, "notes.txt", 0, false, []byte("original notes"))
	if c != 201 {
		t.Fatal(c)
	}
	endpoint := "/api/daemon/coresearch/chats/" + accepted.Conversation.ChatID.String() + "/files?daemon_id=ordinary-text"
	var refs []coresearch.FileRef
	if c := s.call(t, "GET", endpoint, s.personalToken(t, a), ws, nil, &refs); c != 200 {
		t.Fatal(c)
	}
	for _, ref := range refs {
		if ref.FileID == notes.FileID && ref.DocVersion != nil {
			t.Fatal("ordinary text initialized before restore")
		}
	}
	snapshots, _ := json.Marshal(refs)
	if _, e := s.Env.Owner.Exec(t.Context(), `INSERT INTO coresearch_version(workspace_id,project_id,seq,title,files) VALUES($1,$2,1,'all',$3)`, ws, project, snapshots); e != nil {
		t.Fatal(e)
	}
	if c, _ := csUpload(t, s, a, ws, project, "notes.txt", 1, false, []byte("new notes")); c != 201 {
		t.Fatal(c)
	}
	if c := s.call(t, "POST", root+"/versions/1/restore", a, ws, nil, nil); c != 201 {
		t.Fatal(c)
	}
	if c, _ := csUpload(t, s, a, ws, project, "notes.txt", 2, true, []byte("bypass live")); c != 409 {
		t.Fatalf("restored text must be live: %d", c)
	}
	if c := s.call(t, "GET", endpoint, s.personalToken(t, a), ws, nil, &refs); c != 200 {
		t.Fatal(c)
	}
	found := false
	for _, ref := range refs {
		if ref.FileID == notes.FileID {
			found = true
			if ref.DocVersion == nil || *ref.DocVersion != 1 || ref.SHA256 != notes.Sha256 {
				t.Fatalf("ordinary text fill %+v", ref)
			}
		}
	}
	if !found {
		t.Fatal("ordinary text absent")
	}
}
