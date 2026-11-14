package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"lykeion/server/internal/coresearch/doc"
	db "lykeion/server/pkg/db/generated"
)

func TestCommentsAnchorAndMap(t *testing.T) {
	s, a, b, ws, p, f := paperFixture(t)
	root := csProjects + "/" + p.ID
	url := root + "/docs/" + f
	if c := s.call(t, "GET", url, a, ws, nil, nil); c != 200 {
		t.Fatal(c)
	}
	// At version 2, foo occupies UTF-16 [3,6), after an emoji and a space.
	first, _ := json.Marshal(doc.Replace(0, 6, "foo", 6))
	second, _ := json.Marshal(doc.Replace(0, 0, "😀 ", 3))
	third, _ := json.Marshal(doc.Replace(0, 0, "x", 6))
	if c := s.call(t, "POST", url+"/steps", a, ws, map[string]any{"base": 0, "client_id": "a", "steps": []json.RawMessage{first, second, third}}, nil); c != 200 {
		t.Fatal(c)
	}
	input := map[string]any{"base": 2, "from": 3, "to": 6, "quote": "foo", "line": 1, "body": "please cite", "mentions": []string{s.userID(t, b)}}
	var created db.CoresearchComment
	if c := s.call(t, "POST", url+"/comments", a, ws, input, &created); c != 201 {
		t.Fatalf("create %d", c)
	}
	if created.FromPos != 4 || created.ToPos != 7 || created.AuthorUserID.String() != s.userID(t, a) || created.Body != "please cite" || len(created.Mentions) != 1 {
		t.Fatalf("comment %+v", created)
	}
	commentURL := root + "/comments/" + created.ID.String()
	if c := s.call(t, "DELETE", commentURL, b, ws, nil, nil); c != 403 {
		t.Fatalf("non-author delete %d", c)
	}
	var resolved db.CoresearchComment
	if c := s.call(t, "POST", commentURL+"/resolve", b, ws, nil, &resolved); c != 200 || resolved.ResolvedBy == nil || resolved.ResolvedBy.String() != s.userID(t, b) || resolved.ResolvedAt == nil {
		t.Fatalf("resolve %d %+v", c, resolved)
	}
	var again db.CoresearchComment
	if c := s.call(t, "POST", commentURL+"/resolve", a, ws, map[string]bool{"resolved": true}, &again); c != 200 || again.ResolvedBy == nil || *again.ResolvedBy != *resolved.ResolvedBy || !again.ResolvedAt.Equal(*resolved.ResolvedAt) {
		t.Fatalf("idempotent resolve %d %+v", c, again)
	}
	fourth, _ := json.Marshal(doc.Replace(0, 0, "pre", 7))
	if c := s.call(t, "POST", url+"/steps", b, ws, map[string]any{"base": 3, "client_id": "b", "steps": []json.RawMessage{fourth}}, nil); c != 200 {
		t.Fatal(c)
	}
	var comments []db.CoresearchComment
	if c := s.call(t, "GET", url+"/comments", b, ws, nil, &comments); c != 200 || len(comments) != 1 || comments[0].FromPos != 7 || comments[0].ToPos != 10 || comments[0].Quote != "foo" || comments[0].ResolvedBy == nil {
		t.Fatalf("mapped resolved %d %+v", c, comments)
	}
	var payload []byte
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT payload FROM event WHERE workspace_id=$1 AND type='coresearch.comment.changed' ORDER BY id DESC LIMIT 1`, ws).Scan(&payload); e != nil {
		t.Fatal(e)
	}
	var fields map[string]any
	if e := json.Unmarshal(payload, &fields); e != nil || len(fields) != 2 || fields["project_id"] != p.ID || fields["file_id"] != f {
		t.Fatalf("ids-only event %s %v", payload, e)
	}
	if c := s.call(t, "POST", commentURL+"/resolve", a, ws, map[string]bool{"resolved": false}, &again); c != 200 || again.ResolvedBy != nil || again.ResolvedAt != nil {
		t.Fatalf("reopen %d %+v", c, again)
	}
	input["mentions"] = []string{uuid.NewString()}
	if c := s.call(t, "POST", url+"/comments", a, ws, input, nil); c != 422 {
		t.Fatalf("nonmember mention %d", c)
	}
	if c := s.call(t, "DELETE", commentURL, a, ws, nil, nil); c != 204 {
		t.Fatalf("author delete %d", c)
	}
	if c := s.call(t, "GET", url+"/comments", a, ws, nil, &comments); c != 200 || len(comments) != 0 {
		t.Fatalf("deleted list %d %+v", c, comments)
	}
	rows, e := s.Env.Owner.Query(t.Context(), `SELECT payload FROM event WHERE workspace_id=$1 AND type='coresearch.comment.changed' ORDER BY id`, ws)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var payload []byte
		if e := rows.Scan(&payload); e != nil {
			t.Fatal(e)
		}
		var fields map[string]any
		if e := json.Unmarshal(payload, &fields); e != nil || len(fields) != 2 || fields["project_id"] != p.ID || fields["file_id"] != f {
			t.Fatalf("comment event leaked content: %s %v", payload, e)
		}
		count++
	}
	if e := rows.Err(); e != nil {
		t.Fatal(e)
	}
	if count != 4 {
		t.Fatalf("want create, resolve, reopen, delete events only; got %d", count)
	}
}

func TestCommentsValidateAnchorAndIdentity(t *testing.T) {
	s, a, _, ws, p, f := paperFixture(t)
	url := csProjects + "/" + p.ID + "/docs/" + f
	s.call(t, "GET", url, a, ws, nil, nil)
	for _, tc := range []struct {
		name   string
		fields map[string]any
	}{
		{"missing base", map[string]any{"base": nil}},
		{"negative base", map[string]any{"base": -1}},
		{"future base", map[string]any{"base": 1}},
		{"negative range", map[string]any{"from": -1}},
		{"reversed range", map[string]any{"from": 3, "to": 1}},
		{"outside document", map[string]any{"to": 7}},
		{"split surrogate start", map[string]any{"from": 2}},
		{"split surrogate end", map[string]any{"to": 2}},
		{"missing from", map[string]any{"from": nil}},
		{"missing to", map[string]any{"to": nil}},
		{"invalid line", map[string]any{"line": 0}},
		{"empty body", map[string]any{"body": " \n"}},
		{"body cap", map[string]any{"body": strings.Repeat("x", 16385)}},
		{"quote cap", map[string]any{"quote": strings.Repeat("x", 4097)}},
		{"nul body", map[string]any{"body": "x\x00"}},
		{"spoof author", map[string]any{"author_user_id": uuid.NewString()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := map[string]any{"base": 0, "from": 1, "to": 3, "quote": "😀", "line": 1, "body": "note"}
			for k, v := range tc.fields {
				in[k] = v
			}
			if c := s.call(t, "POST", url+"/comments", a, ws, in, nil); c != 422 {
				t.Fatalf("invalid input %d", c)
			}
		})
	}
	var comments []db.CoresearchComment
	if c := s.call(t, "GET", url+"/comments", a, ws, nil, &comments); c != 200 || len(comments) != 0 {
		t.Fatalf("invalid inputs persisted %d %+v", c, comments)
	}
	// Valid byte limits and a whole emoji anchor are accepted.
	in := map[string]any{"base": 0, "from": 1, "to": 3, "quote": strings.Repeat("x", 4096), "line": 1, "body": strings.Repeat("x", 16384)}
	if c := s.call(t, "POST", url+"/comments", a, ws, in, nil); c != 201 {
		t.Fatalf("boundary sizes %d", c)
	}
}

func TestCommentsHistoricalMappingAcrossFoldAndPages(t *testing.T) {
	s, a, _, ws, p, f := paperFixture(t)
	url := csProjects + "/" + p.ID + "/docs/" + f
	s.call(t, "GET", url, a, ws, nil, nil)
	first, _ := json.Marshal(doc.Replace(0, 6, "foo", 6))
	second, _ := json.Marshal(doc.Replace(0, 0, "😀 ", 3))
	steps := []json.RawMessage{first, second}
	for i := 0; i < 498; i++ {
		raw, _ := json.Marshal(doc.Replace(0, 0, "x", 6+i))
		steps = append(steps, raw)
	}
	if c := s.call(t, "POST", url+"/steps", a, ws, map[string]any{"base": 0, "client_id": "a", "steps": steps}, nil); c != 200 {
		t.Fatal(c)
	}
	steps = nil
	for i := 0; i < 3; i++ {
		raw, _ := json.Marshal(doc.Replace(0, 0, "x", 504+i))
		steps = append(steps, raw)
	}
	if c := s.call(t, "POST", url+"/steps", a, ws, map[string]any{"base": 500, "client_id": "a", "steps": steps}, nil); c != 200 {
		t.Fatal(c)
	}
	var comment db.CoresearchComment
	if c := s.call(t, "POST", url+"/comments", a, ws, map[string]any{"base": 2, "from": 3, "to": 6, "quote": "foo", "line": 1, "body": "old anchor"}, &comment); c != 201 || comment.FromPos != 504 || comment.ToPos != 507 {
		t.Fatalf("old anchor %d %+v", c, comment)
	}
	var checkpoint int64
	if e := s.Env.Owner.QueryRow(t.Context(), `SELECT checkpoint_version FROM coresearch_doc WHERE file_id=$1`, f).Scan(&checkpoint); e != nil || checkpoint != 400 {
		t.Fatalf("fold %d %v", checkpoint, e)
	}
}

func TestCommentsNonmemberAndProjectScope(t *testing.T) {
	s, a, _, ws, p, f := paperFixture(t)
	root := csProjects + "/" + p.ID
	url := root + "/docs/" + f
	if c := s.call(t, "GET", url, a, ws, nil, nil); c != 200 {
		t.Fatal(c)
	}
	input := map[string]any{"base": 0, "from": 1, "to": 3, "quote": "😀", "line": 1, "body": "note"}
	var comment db.CoresearchComment
	if c := s.call(t, "POST", url+"/comments", a, ws, input, &comment); c != 201 {
		t.Fatal(c)
	}
	nonmember := s.signIn(t, "nonmember@lab.org")
	if _, e := s.Env.Owner.Exec(t.Context(), `INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'member')`, ws, s.userID(t, nonmember)); e != nil {
		t.Fatal(e)
	}
	commentURL := root + "/comments/" + comment.ID.String()
	for _, route := range []struct {
		method, url string
		body        any
	}{
		{"GET", url + "/comments", nil},
		{"POST", url + "/comments", input},
		{"POST", commentURL + "/resolve", nil},
		{"DELETE", commentURL, nil},
	} {
		if c := s.call(t, route.method, route.url, nonmember, ws, route.body, nil); c != 404 {
			t.Fatalf("nonmember %s %s = %d", route.method, route.url, c)
		}
	}
	input["mentions"] = []string{s.userID(t, nonmember)}
	if c := s.call(t, "POST", url+"/comments", a, ws, input, nil); c != 422 {
		t.Fatalf("workspace-only mention %d", c)
	}
	var other csProject
	if c := s.call(t, "POST", csProjects, a, ws, map[string]string{"name": "other paper"}, &other); c != 201 {
		t.Fatal(c)
	}
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE coresearch_project SET mode='cowrite' WHERE id=$1`, other.ID); e != nil {
		t.Fatal(e)
	}
	for _, route := range []struct {
		method, suffix string
		body           any
	}{
		{"GET", "/docs/" + f + "/comments", nil},
		{"POST", "/docs/" + f + "/comments", input},
		{"POST", "/comments/" + comment.ID.String() + "/resolve", nil},
		{"DELETE", "/comments/" + comment.ID.String(), nil},
	} {
		if c := s.call(t, route.method, csProjects+"/"+other.ID+route.suffix, a, ws, route.body, nil); c != 404 {
			t.Fatalf("foreign project %s = %d", route.suffix, c)
		}
	}
	if _, e := s.Env.Owner.Exec(t.Context(), `UPDATE coresearch_file SET deleted_at=now() WHERE id=$1`, f); e != nil {
		t.Fatal(e)
	}
	for _, route := range []struct {
		method, url string
		body        any
	}{
		{"GET", url + "/comments", nil},
		{"POST", url + "/comments", input},
		{"POST", commentURL + "/resolve", nil},
		{"DELETE", commentURL, nil},
	} {
		if c := s.call(t, route.method, route.url, a, ws, route.body, nil); c != 404 {
			t.Fatalf("hidden file %s %s = %d", route.method, route.url, c)
		}
	}
}

func TestCommentsDeletedAnchorCollapses(t *testing.T) {
	s, a, _, ws, p, f := paperFixture(t)
	url := csProjects + "/" + p.ID + "/docs/" + f
	if c := s.call(t, "GET", url, a, ws, nil, nil); c != 200 {
		t.Fatal(c)
	}
	input := map[string]any{"base": 0, "from": 1, "to": 3, "quote": "😀", "line": 1, "body": "note"}
	if c := s.call(t, "POST", url+"/comments", a, ws, input, nil); c != 201 {
		t.Fatal(c)
	}
	raw, _ := json.Marshal(doc.Replace(1, 3, "", 6))
	if c := s.call(t, "POST", url+"/steps", a, ws, map[string]any{"base": 0, "client_id": "a", "steps": []json.RawMessage{raw}}, nil); c != 200 {
		t.Fatal(c)
	}
	if c := s.call(t, "POST", url+"/comments", a, ws, input, nil); c != 201 {
		t.Fatal(c)
	}
	var comments []db.CoresearchComment
	if c := s.call(t, "GET", url+"/comments", a, ws, nil, &comments); c != 200 || len(comments) != 2 {
		t.Fatalf("list %d %+v", c, comments)
	}
	for _, comment := range comments {
		if comment.FromPos != 1 || comment.ToPos != 1 || comment.Quote != "😀" || comment.Line != 1 {
			t.Fatalf("collapsed anchor %+v", comment)
		}
	}
}
