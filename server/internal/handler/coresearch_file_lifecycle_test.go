package handler

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"lykeion/server/internal/coresearch/doc"
	db "lykeion/server/pkg/db/generated"
)

func TestCoresearchFileLifecycle(t *testing.T) {
	s, a, b, ws, p, f := paperFixture(t)
	root := csProjects + "/" + p.ID
	fileURL, docURL := root+"/files/"+f, root+"/docs/"+f
	must := func(method, url, who string, body any, want int, out any) {
		t.Helper()
		if c := s.call(t, method, url, who, ws, body, out); c != want {
			t.Fatalf("%s %s = %d, want %d", method, url, c, want)
		}
	}
	must("GET", docURL, a, nil, 200, nil)
	var renamed coresearchFileView
	must("PATCH", fileURL, b, map[string]string{"path": "sections/intro.tex"}, 200, &renamed)
	if renamed.ID.String() != f || renamed.Path != "sections/intro.tex" || renamed.Head.N != 1 {
		t.Fatalf("rename identity %+v", renamed)
	}
	must("PATCH", fileURL, a, map[string]string{"path": "sections/intro.tex"}, 200, nil)
	for _, path := range []string{"../x", "/x", ".LYK/config.json", "nested/.CoDeX/config.toml", "nested/.AGENTS/x", "x\x00.tex"} {
		must("PATCH", fileURL, a, map[string]string{"path": path}, 400, nil)
	}
	c, other := csUpload(t, s, a, ws, p.ID, "other.tex", 0, false, []byte("other"))
	if c != 201 {
		t.Fatal(c)
	}
	must("PATCH", fileURL, a, map[string]string{"path": "other.tex"}, 409, nil)
	must("DELETE", root+"/files/"+other.FileID.String(), b, nil, 204, nil)
	must("PATCH", fileURL, a, map[string]string{"path": "other.tex"}, 409, nil)
	var proposal, id string
	if e := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO coresearch_proposal(workspace_id,project_id,created_by) VALUES($1,$2,$3) RETURNING id`, ws, p.ID, s.userID(t, a)).Scan(&proposal); e != nil {
		t.Fatal(e)
	}
	if e := s.Env.Owner.QueryRow(t.Context(), `INSERT INTO coresearch_change(workspace_id,proposal_id,file_id,from_pos,to_pos,removed_text,status) VALUES($1,$2,$3,1,3,'old','pending') RETURNING id`, ws, proposal, f).Scan(&id); e != nil {
		t.Fatal(e)
	}
	must("DELETE", fileURL, b, nil, 409, nil)
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE coresearch_change SET status='stale' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	must("DELETE", fileURL, a, nil, 409, nil)
	must("POST", root+"/changes/"+id+"/undo", b, nil, 200, nil)
	must("DELETE", fileURL, b, nil, 204, nil)
	must("DELETE", fileURL, a, nil, 404, nil)
	must("PATCH", fileURL, a, map[string]string{"path": "new.tex"}, 404, nil)
	for _, suffix := range []string{"/keep", "/undo"} {
		must("POST", root+"/changes/"+id+suffix, a, nil, 404, nil)
	}
	must("GET", docURL, a, nil, 404, nil)
	must("POST", docURL+"/steps", b, map[string]any{"base": 1, "client_id": "late", "steps": []any{[]any{7, []any{0, "!"}}}}, 404, nil)
	if c, _, _ := csUploadResponse(t, s, a, ws, p.ID, "sections/intro.tex", 0, false, []byte("revive")); c != 409 {
		t.Fatalf("hidden upload %d", c)
	}
	var text, author, initial string
	var version int
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT d.initial_text,d.version,s.author_user_id,s.changes::text FROM coresearch_doc d JOIN coresearch_doc_step s ON s.file_id=d.file_id AND s.version=d.version WHERE d.file_id=$1`, f).Scan(&initial, &version, &author, &text); e != nil {
		t.Fatal(e)
	}
	changes, e := doc.Parse(json.RawMessage(text))
	if e != nil {
		t.Fatal(e)
	}
	cleared, e := changes.Apply("aold\r\nb")
	if e != nil || cleared != "" || initial != "a😀\r\nb" || version != 2 || author != s.userID(t, b) {
		t.Fatalf("clear/history %q %d %s %v", cleared, version, initial, e)
	}
	var size, n int
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT n,size FROM coresearch_file_version WHERE file_id=$1 ORDER BY n DESC LIMIT 1`, f).Scan(&n, &size); e != nil || n != 2 || size != 0 {
		t.Fatalf("snapshot %d %d %v", n, size, e)
	}
}

func TestCoresearchFileLifecycleAccess(t *testing.T) {
	s, a, b, ws, p, f := paperFixture(t)
	root := csProjects + "/" + p.ID
	url := root + "/files/" + f
	outsider := s.signIn(t, "lifecycle-outsider@example.com")
	// Workspace membership alone must not grant project access.
	if _, e := s.Env.Owner.Exec(t.Context(), `INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member')`, ws, s.userID(t, outsider)); e != nil {
		t.Fatal(e)
	}
	for _, who := range []string{outsider} {
		for _, method := range []string{"PATCH", "DELETE"} {
			if c := s.call(t, method, url, who, ws, map[string]string{"path": "renamed.tex"}, nil); c != 404 {
				t.Fatalf("outsider %s %d", method, c)
			}
		}
	}
	if c := s.call(t, "DELETE", root+"/members/"+s.userID(t, b), a, ws, nil, nil); c != 204 {
		t.Fatal(c)
	}
	for _, method := range []string{"PATCH", "DELETE"} {
		if c := s.call(t, method, url, b, ws, map[string]string{"path": "renamed.tex"}, nil); c != 404 {
			t.Fatalf("removed %s %d", method, c)
		}
	}
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE coresearch_project SET archived_at=now() WHERE id=$1`, p.ID); e != nil {
		t.Fatal(e)
	}
	for _, method := range []string{"PATCH", "DELETE"} {
		if c := s.call(t, method, url, a, ws, map[string]string{"path": "renamed.tex"}, nil); c != 409 {
			t.Fatalf("archived %s %d", method, c)
		}
	}
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE coresearch_project SET archived_at=NULL,mode='experiment' WHERE id=$1`, p.ID); e != nil {
		t.Fatal(e)
	}
	for _, method := range []string{"PATCH", "DELETE"} {
		if c := s.call(t, method, url, a, ws, map[string]string{"path": "renamed.tex"}, nil); c != 404 {
			t.Fatalf("experiment %s %d", method, c)
		}
	}
}

func TestCoresearchFileLifecycleRevival(t *testing.T) {
	for _, later := range []bool{false, true} {
		t.Run(fmt.Sprint(later), func(t *testing.T) {
			s, a, b, ws, p, f := paperFixture(t)
			root := csProjects + "/" + p.ID
			if _, e := s.Env.Owner.Exec(t.Context(), `INSERT INTO coresearch_version(workspace_id,project_id,seq,title,files) SELECT $1,$2,1,'original',jsonb_build_array(jsonb_build_object('file_id',file_id,'n',n,'path','main.tex','sha256',sha256,'size',size)) FROM coresearch_file_version WHERE file_id=$3`, ws, p.ID, f); e != nil {
				t.Fatal(e)
			}
			if c := s.call(t, "PATCH", root+"/files/"+f, b, ws, map[string]string{"path": "renamed.tex"}, nil); c != 200 {
				t.Fatal(c)
			}
			if c := s.call(t, "DELETE", root+"/files/"+f, b, ws, nil, nil); c != 204 {
				t.Fatal(c)
			}
			var proposal struct{ ID string }
			if c := s.call(t, "POST", root+"/versions/1/restore", a, ws, nil, &proposal); c != 201 {
				t.Fatal(c)
			}
			var change string
			if e := s.Env.Owner.QueryRow(t.Context(), `SELECT id FROM coresearch_change WHERE proposal_id=$1 AND file_id=$2`, proposal.ID, f).Scan(&change); e != nil {
				t.Fatal(e)
			}
			if later {
				if c := s.call(t, "POST", root+"/docs/"+f+"/steps", b, ws, map[string]any{"base": 2, "client_id": "later", "steps": []any{[]any{6, []any{0, "later"}}}}, nil); c != 200 {
					t.Fatal(c)
				}
			}
			if c := s.call(t, "POST", root+"/changes/"+change+"/undo", b, ws, nil, nil); c != 200 {
				t.Fatal(c)
			}
			var files []coresearchFileView
			s.call(t, "GET", root+"/files", a, ws, nil, &files)
			if !later && len(files) != 0 {
				t.Fatal("revival Undo must rehide")
			}
			if later {
				var d doc.Doc
				s.call(t, "GET", root+"/docs/"+f, a, ws, nil, &d)
				if d.Text != "later" || len(files) != 1 || files[0].Path != "renamed.tex" {
					t.Fatalf("later typing lost: %+v %+v", d, files)
				}
			}
			var history []db.CoresearchVersion
			s.call(t, "GET", root+"/versions", a, ws, nil, &history)
			if len(history) != 1 || !strings.Contains(string(history[0].Files), "main.tex") {
				t.Fatal("immutable snapshot path changed")
			}
		})
	}
}

func TestCoresearchFileLifecycleRollbackAndBinary(t *testing.T) {
	s, a, b, ws, p, f := paperFixture(t)
	root := csProjects + "/" + p.ID
	url := root + "/files/" + f
	s.call(t, "GET", root+"/docs/"+f, a, ws, nil, nil)
	original := s.H.Blobs
	s.H.Blobs = buildFailStorage{original}
	if c := s.call(t, "DELETE", url, b, ws, nil, nil); c != 500 {
		t.Fatalf("staging failure %d", c)
	}
	s.H.Blobs = original
	var d doc.Doc
	if c := s.call(t, "GET", root+"/docs/"+f, a, ws, nil, &d); c != 200 || d.Text != "a😀\r\nb" || d.Version != 0 {
		t.Fatalf("staging mutated document %d %+v", c, d)
	}
	if _, e := s.Env.Owner.Exec(t.Context(), `CREATE FUNCTION fail_lifecycle_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'snapshot refused'; END $$; CREATE TRIGGER fail_lifecycle_snapshot BEFORE INSERT ON coresearch_file_version FOR EACH ROW EXECUTE FUNCTION fail_lifecycle_snapshot()`); e != nil {
		t.Fatal(e)
	}
	if c := s.call(t, "DELETE", url, b, ws, nil, nil); c != 500 {
		t.Fatalf("publication failure %d", c)
	}
	if c := s.call(t, "GET", root+"/docs/"+f, a, ws, nil, &d); c != 200 || d.Text != "a😀\r\nb" || d.Version != 0 {
		t.Fatalf("half-cleared document %d %+v", c, d)
	}
	if _, e := s.Env.Owner.Exec(t.Context(), `DROP TRIGGER fail_lifecycle_snapshot ON coresearch_file_version; DROP FUNCTION fail_lifecycle_snapshot()`); e != nil {
		t.Fatal(e)
	}
	c, v := csUpload(t, s, a, ws, p.ID, "figure.png", 0, false, []byte{137, 80, 78, 71, 0})
	if c != 201 {
		t.Fatal(c)
	}
	if c := s.call(t, "DELETE", root+"/files/"+v.FileID.String(), b, ws, nil, nil); c != 204 {
		t.Fatal(c)
	}
	var journals, versions int
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM coresearch_doc WHERE file_id=$1),(SELECT count(*) FROM coresearch_file_version WHERE file_id=$1)`, v.FileID).Scan(&journals, &versions); e != nil || journals != 0 || versions != 1 {
		t.Fatalf("binary history %d %d %v", journals, versions, e)
	}
}

func TestCoresearchFileLifecycleHydrationRechecks(t *testing.T) {
	s, a, b, ws, p, f := paperFixture(t)
	root := csProjects + "/" + p.ID
	s.H.Blobs = &docHydrationStorage{Storage: s.H.Blobs, before: func() {
		if c := s.call(t, "DELETE", root+"/members/"+s.userID(t, b), a, ws, nil, nil); c != 204 {
			t.Fatal(c)
		}
	}}
	if c := s.call(t, "DELETE", root+"/files/"+f, b, ws, nil, nil); c != 404 {
		t.Fatalf("removed while hydrating %d", c)
	}
	var d doc.Doc
	if c := s.call(t, "GET", root+"/docs/"+f, a, ws, nil, &d); c != 200 || d.Text != "a😀\r\nb" || d.Version != 0 {
		t.Fatalf("removed member wrote %d %+v", c, d)
	}
}

func TestCoresearchFileLifecycleQueuedBuildRetainsPathAndText(t *testing.T) {
	s, a, b, ws, p, f := paperFixture(t)
	root := csProjects + "/" + p.ID
	if c := s.call(t, "GET", root+"/docs/"+f, a, ws, nil, nil); c != 200 {
		t.Fatal(c)
	}
	var build db.CoresearchBuild
	if c := s.call(t, "POST", root+"/builds", b, ws, nil, &build); c != 201 {
		t.Fatal(c)
	}
	if c := s.call(t, "PATCH", root+"/files/"+f, a, ws, map[string]string{"path": "renamed.tex"}, nil); c != 200 {
		t.Fatal(c)
	}
	if c := s.call(t, "DELETE", root+"/files/"+f, a, ws, nil, nil); c != 204 {
		t.Fatal(c)
	}
	runtime := csRegister(t, s, b, ws, "own", "claude")
	work := csWork(t, s, b, "own", runtime)
	if len(work.Builds) != 1 || work.Builds[0].ID != build.ID {
		t.Fatalf("queued claim %+v", work)
	}
	claimed := work.Builds[0]
	if len(claimed.Docs) != 1 || claimed.Docs[0].Path != "main.tex" || claimed.Docs[0].Text != "a😀\r\nb" || claimed.Docs[0].Version != 0 {
		t.Fatalf("immutable queued text %+v", claimed.Docs)
	}
	if len(claimed.Sources) != 1 || claimed.Sources[0].Path != "main.tex" {
		t.Fatalf("immutable queued paths %+v", claimed.Sources)
	}
}
