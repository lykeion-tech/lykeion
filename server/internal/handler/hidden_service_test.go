package handler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

func serviceTar(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	if err := tw.WriteHeader(&tar.Header{Name: "server.py", Mode: 0600, Size: 8}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte("print(1)"))
	tw.Close()
	return b.Bytes()
}
func (s *server) serviceUpload(t *testing.T, tok, ws, name string, data []byte, after bool) (int, map[string]any) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	if !after {
		mw.WriteField("kind", "service")
	}
	p, _ := mw.CreateFormFile("file", name)
	p.Write(data)
	if after {
		mw.WriteField("kind", "service")
	}
	mw.Close()
	req, _ := http.NewRequest("POST", s.URL+"/api/attachments", &b)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Workspace-ID", ws)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}
func TestAPendingServiceGetsMetadataWhenLinked(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	tok := s.signIn(t, "a@lab.org")
	ws := s.workspace(t, tok, "lab").ID
	for _, after := range []bool{false, true} {
		code, up := s.serviceUpload(t, tok, ws, "s.tar", serviceTar(t), after)
		if code != 201 {
			t.Fatalf("upload: %d %v", code, up)
		}
		var kind, text string
		var name *string
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT kind,text_sha256,service_name FROM attachment WHERE id=$1`, up["id"]).Scan(&kind, &text, &name); err != nil {
			t.Fatal(err)
		}
		if kind != "service" || text != "" || name != nil {
			t.Fatalf("pending: %s %q %v", kind, text, name)
		}
		if _, err := s.H.Blobs.Get(t.Context(), records.TextKey(ws+"/attachments/"+up["id"].(string))); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("service text companion: %v", err)
		}
		in := map[string]any{"question": uuid.NewString(), "env": map[string]string{"DATA": "x"}, "services": []any{map[string]any{"name": "evaluator", "attachment_id": up["id"], "start": "python server.py", "client": "server.py", "env": []string{"DATA"}}}}
		var c card
		if got := s.call(t, "POST", "/api/cards", tok, ws, in, &c); got != 201 {
			t.Fatalf("create %d", got)
		}
		var cid, start, client string
		var vars []string
		if err := s.Env.Owner.QueryRow(t.Context(), `SELECT card_id,service_start,service_client,service_env FROM attachment WHERE id=$1`, up["id"]).Scan(&cid, &start, &client, &vars); err != nil {
			t.Fatal(err)
		}
		if cid != c.ID || start != "python server.py" || client != "server.py" || len(vars) != 1 || vars[0] != "DATA" {
			t.Fatalf("linked %s %q %q %v", cid, start, client, vars)
		}
		in["question"] = uuid.NewString()
		if got := s.call(t, "POST", "/api/cards", tok, ws, in, nil); got != 400 {
			t.Fatalf("relink %d", got)
		}
	}
}
func TestServicesAreChecked(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	tok := s.signIn(t, "a@lab.org")
	ws := s.workspace(t, tok, "lab").ID
	_, up := s.serviceUpload(t, tok, ws, "s.tar", serviceTar(t), false)
	file := s.uploadData(t, tok, ws, "file.txt", []byte("public"))
	valid := func() map[string]any {
		return map[string]any{"name": "evaluator", "attachment_id": up["id"], "start": "python server.py"}
	}
	for _, key := range []string{"name", "missing-name", "start", "client", "env", "file", "duplicate", "count"} {
		t.Run(key, func(t *testing.T) {
			v := valid()
			arr := []any{v}
			switch key {
			case "name":
				v["name"] = "Bad"
			case "missing-name":
				delete(v, "name")
			case "start":
				delete(v, "start")
			case "client":
				v["client"] = "../secret"
			case "env":
				v["env"] = []string{"HOME"}
			case "file":
				v["attachment_id"] = file
			case "duplicate":
				arr = append(arr, valid())
			case "count":
				for i := 0; i < 4; i++ {
					arr = append(arr, valid())
				}
			}
			if got := s.call(t, "POST", "/api/cards", tok, ws, map[string]any{"question": uuid.NewString(), "services": arr}, nil); got != 400 {
				t.Fatalf("invalid %s: %d", key, got)
			}
		})
	}
	if code, _ := s.serviceUpload(t, tok, ws, "service.zip", archiveBytes(t, "a", 0), false); code != 400 {
		t.Fatalf("zip %d", code)
	}
	if code, _ := s.serviceUpload(t, tok, ws, "fake.tar", []byte("not a tar"), false); code != 400 {
		t.Fatalf("fake %d", code)
	}
}
func TestStudyPageListsServiceNamesOnly(t *testing.T) { testServiceVisibility(t, false) }
func TestAServiceIsNeverAnAttachment(t *testing.T)    { testServiceVisibility(t, true) }
func testServiceVisibility(t *testing.T, download bool) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	tok := s.signIn(t, "a@lab.org")
	ws := s.workspace(t, tok, "lab").ID
	_, up := s.serviceUpload(t, tok, ws, "s.tar", serviceTar(t), false)
	var c card
	if got := s.call(t, "POST", "/api/cards", tok, ws, map[string]any{"question": "q", "services": []any{map[string]any{"name": "evaluator", "attachment_id": up["id"], "start": "python server.py"}}}, &c); got != 201 {
		t.Fatalf("create %d", got)
	}
	var view map[string]json.RawMessage
	s.call(t, "GET", "/api/cards/"+c.ID, tok, ws, nil, &view)
	if string(view["services"]) != `[{"name":"evaluator"}]` || string(view["attachments"]) != "[]" {
		t.Fatalf("view %s", view)
	}
	if download {
		resp, _ := s.download(t, tok, ws, c.ID, up["id"].(string))
		if resp.StatusCode != 404 {
			t.Fatalf("member download %d", resp.StatusCode)
		}
	}
	var n int
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT count(*) FROM record WHERE card_id=$1 AND kind='call'`, c.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d source records", n)
	}
}
func TestOnlyExperimentationRunsClaimServices(t *testing.T)  { testServiceRun(t, "grounding") }
func TestTheDaemonRouteServesOnlyItsRunningRun(t *testing.T) { testServiceRun(t, "experimentation") }
func TestHiddenServicesFollowSharing(t *testing.T)           { testServiceRun(t, "experimentation", true) }
func testServiceRun(t *testing.T, col string, shared ...bool) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	jwt := s.signIn(t, "a@lab.org")
	tok := s.personalToken(t, jwt)
	ws := s.workspace(t, jwt, "lab").ID
	_, up := s.serviceUpload(t, jwt, ws, "s.tar", serviceTar(t), false)
	var c card
	if got := s.call(t, "POST", "/api/cards", jwt, ws, map[string]any{"question": "q", "services": []any{map[string]any{"name": "evaluator", "attachment_id": up["id"], "start": "python server.py"}}}, &c); got != 201 {
		t.Fatalf("create %d", got)
	}
	var rts []struct{ ID string }
	s.call(t, "POST", "/api/daemon/register", tok, "", map[string]any{"workspace_id": ws, "daemon_id": "d", "platform": map[string]string{"os": "linux"}, "harnesses": []string{"claude"}}, &rts)
	err := s.H.Store.InWorkspace(t.Context(), uuid.MustParse(ws), uuid.MustParse(s.userID(t, jwt)), func(tx *store.Tx) error {
		_, err := s.H.AgentRuns.Enqueue(t.Context(), tx, uuid.MustParse(s.userID(t, jwt)), service.NewAgentRun{Card: uuid.MustParse(c.ID), Column: col, Round: 1, Role: "scoper", Harness: "claude", Prompt: "q"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := map[string]any{"workspace_id": ws, "runtime_id": rts[0].ID}
	var claims []map[string]json.RawMessage
	if got := s.call(t, "POST", "/api/daemon/agent-runs/claim", tok, "", map[string]any{"runtimes": []any{ref}, "slots": 1}, &claims); got != 200 || len(claims) != 1 {
		t.Fatalf("claim %d %v", got, claims)
	}
	if b := claims[0]["attachments"]; len(b) > 0 && string(b) != "[]" {
		t.Fatalf("attachments %s", b)
	}
	if col == "grounding" {
		if b := claims[0]["services"]; len(b) > 0 && string(b) != "[]" {
			t.Fatalf("grounding services %s", b)
		}
		return
	}
	if !strings.Contains(string(claims[0]["services"]), `"name":"evaluator"`) {
		t.Fatalf("missing service %s", claims[0]["services"])
	}
	var serviceClaims []map[string]any
	if err := json.Unmarshal(claims[0]["services"], &serviceClaims); err != nil {
		t.Fatal(err)
	}
	sc := serviceClaims[0]
	if len(serviceClaims) != 1 || len(sc) != 7 || sc["id"] != up["id"] || sc["start"] != "python server.py" || sc["client"] != "" || sc["sha256"] != up["sha256"] || sc["size_bytes"] != up["size_bytes"] {
		t.Fatalf("service metadata %v", serviceClaims)
	}
	if vars, ok := sc["env"].([]any); !ok || len(vars) != 0 {
		t.Fatalf("service env %v", sc["env"])
	}
	var run struct {
		ID      string
		Attempt int
	}
	json.Unmarshal(claims[0]["agent_run"], &run)
	ref["attempt"] = run.Attempt
	path := "/api/daemon/runs/" + run.ID + "/services/" + up["id"].(string)
	get := func(token, p string) int {
		req, _ := http.NewRequest("GET", s.URL+p, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Workspace-ID", ws)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == 200 && !bytes.Equal(b, serviceTar(t)) {
			t.Fatalf("blob mismatch")
		}
		return resp.StatusCode
	}
	if got := get(tok, path); got != 404 {
		t.Fatalf("dispatched %d", got)
	}
	for _, step := range []string{"prepare-lease", "start"} {
		if got := s.call(t, "POST", "/api/daemon/agent-runs/"+run.ID+"/"+step, tok, "", ref, nil); got != 204 {
			t.Fatalf("%s %d", step, got)
		}
	}
	if got := get(tok, path); got != 200 {
		t.Fatalf("own %d", got)
	}
	if len(shared) > 0 {
		colleagueJWT := s.signIn(t, "colleague@lab.org")
		colleague := uuid.MustParse(s.userID(t, colleagueJWT))
		if err := s.H.Store.InWorkspace(t.Context(), uuid.MustParse(ws), uuid.MustParse(s.userID(t, jwt)), func(tx *store.Tx) error {
			return tx.Q.AddMember(t.Context(), db.AddMemberParams{WorkspaceID: uuid.MustParse(ws), UserID: colleague, Role: "member"})
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE card SET owner_id=$1 WHERE id=$2", colleague, c.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Env.Owner.Exec(t.Context(), "INSERT INTO runtime_share(workspace_id,runtime_id,user_id) VALUES($1,$2,$3)", ws, rts[0].ID, colleague); err != nil {
			t.Fatal(err)
		}
		if got := get(tok, path); got != 200 {
			t.Fatalf("shared %d", got)
		}
		if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE runtime SET os='darwin' WHERE id=$1", rts[0].ID); err != nil {
			t.Fatal(err)
		}
		if got := get(tok, path); got != 404 {
			t.Fatalf("unsupported shared service %d", got)
		}
		if _, err := s.Env.Owner.Exec(t.Context(), "UPDATE runtime SET os='linux' WHERE id=$1", rts[0].ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Env.Owner.Exec(t.Context(), "DELETE FROM runtime_share WHERE runtime_id=$1", rts[0].ID); err != nil {
			t.Fatal(err)
		}
		if got := get(tok, path); got != 404 {
			t.Fatalf("revoked %d", got)
		}
	}
	var ltk string
	json.Unmarshal(claims[0]["token"], &ltk)
	other := s.personalToken(t, s.signIn(t, "b@lab.org"))
	for _, token := range []string{ltk, jwt, other, "ltk_invalid", "lyk_invalid"} {
		if got := get(token, path); got != 404 {
			t.Fatalf("wrong credential %q %d", token[:5], got)
		}
	}
	if got := get(ltk, "/api/role/attachments/"+up["id"].(string)); got != 404 {
		t.Fatalf("role attachment %d", got)
	}
	for _, sql := range []string{`UPDATE agent_run SET board_column='hypotheses' WHERE id=$1`, `UPDATE agent_run SET board_column='experimentation', status='completed' WHERE id=$1`} {
		if _, err := s.Env.Owner.Exec(t.Context(), sql, run.ID); err != nil {
			t.Fatal(err)
		}
		if got := get(tok, path); got != 404 {
			t.Fatalf("ineligible %d", got)
		}
	}
}

func TestServiceLinkFailureRollsBackFilesAndStudy(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	tok := s.signIn(t, "a@lab.org")
	ws := s.workspace(t, tok, "lab").ID
	file := s.uploadData(t, tok, ws, "data.csv", []byte("x\n"))
	_, up := s.serviceUpload(t, tok, ws, "s.tar", serviceTar(t), false)
	_, second := s.serviceUpload(t, tok, ws, "second.tar", serviceTar(t), false)
	local := s.H.Blobs.(storage.Local)
	gate := gatedBlobs{local, make(chan struct{}, 2), make(chan struct{})}
	s.H.Blobs = gate
	code := make(chan int)
	go func() {
		code <- s.call(t, "POST", "/api/cards", tok, ws, map[string]any{"question": "q", "attachment_ids": []string{file}, "services": []any{map[string]any{"name": "evaluator", "attachment_id": up["id"], "start": "python server.py"}, map[string]any{"name": "second", "attachment_id": second["id"], "start": "python server.py"}}}, nil)
	}()
	<-gate.waiting
	if _, err := s.Env.Owner.Exec(t.Context(), `DELETE FROM attachment WHERE id=$1`, second["id"]); err != nil {
		t.Fatal(err)
	}
	close(gate.release)
	if got := <-code; got != 409 {
		t.Fatalf("link failure %d", got)
	}
	if n := s.count(t, `SELECT count(*) FROM card`) + s.count(t, `SELECT count(*) FROM record`); n != 0 {
		t.Fatalf("partial study/source: %d", n)
	}
	if n := s.count(t, `SELECT count(*) FROM attachment WHERE id=$1 AND card_id IS NULL AND record_key=''`, file); n != 1 {
		t.Fatal("file was partially linked")
	}
	if n := s.count(t, `SELECT count(*) FROM attachment WHERE id=$1 AND card_id IS NULL AND service_name IS NULL`, up["id"]); n != 1 {
		t.Fatal("service was partially linked")
	}
}
func TestForeignServiceUploadsAndCombinedLimits(t *testing.T) {
	s := newServer(t)
	s.builtIns(t)
	s.H.Engine = nil
	a := s.signIn(t, "a@lab.org")
	b := s.signIn(t, "b@lab.org")
	ws := s.workspace(t, a, "lab").ID
	other := s.workspace(t, a, "other").ID
	// The same upload owner is necessary even when the caller can see both workspaces.
	_, foreign := s.serviceUpload(t, a, other, "s.tar", serviceTar(t), false)
	_, mine := s.serviceUpload(t, a, ws, "s.tar", serviceTar(t), false)
	create := func(id any) int {
		return s.call(t, "POST", "/api/cards", a, ws, map[string]any{"question": uuid.NewString(), "services": []any{map[string]any{"name": "evaluator", "attachment_id": id, "start": "python server.py"}}}, nil)
	}
	if got := create(foreign["id"]); got != 400 {
		t.Fatalf("foreign workspace %d", got)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE attachment SET uploader_id=$1 WHERE id=$2`, s.userID(t, b), mine["id"]); err != nil {
		t.Fatal(err)
	}
	if got := create(mine["id"]); got != 400 {
		t.Fatalf("foreign uploader %d", got)
	}
	if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE attachment SET uploader_id=$1 WHERE id=$2`, s.userID(t, a), mine["id"]); err != nil {
		t.Fatal(err)
	}
	file := s.uploadData(t, a, ws, "file.txt", []byte("x"))
	for _, column := range []string{"unpacked_bytes", "inner_files"} {
		value := int64(10<<30) - 1024
		if column == "inner_files" {
			value = 20000
		}
		if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE attachment SET `+column+`=$1 WHERE id=$2`, value, file); err != nil {
			t.Fatal(err)
		}
		got := s.call(t, "POST", "/api/cards", a, ws, map[string]any{"question": uuid.NewString(), "attachment_ids": []string{file}, "services": []any{map[string]any{"name": "evaluator", "attachment_id": mine["id"], "start": "python server.py"}}}, nil)
		if got != 400 {
			t.Fatalf("combined %s limit %d", column, got)
		}
		if _, err := s.Env.Owner.Exec(t.Context(), `UPDATE attachment SET `+column+`=0 WHERE id=$1`, file); err != nil {
			t.Fatal(err)
		}
	}
	if n := s.count(t, `SELECT count(*) FROM card`); n != 0 {
		t.Fatalf("refusals created %d studies", n)
	}
	if got := s.call(t, "POST", "/api/cards", a, ws, map[string]any{"question": "wrong-kind", "attachment_ids": []any{mine["id"]}}, nil); got != 400 {
		t.Fatalf("service as file %d", got)
	}

	files := []string{file}
	for i := 1; i < 97; i++ {
		files = append(files, s.uploadData(t, a, ws, "file.txt", []byte("x")))
	}
	services := []any{map[string]any{"name": "evaluator", "attachment_id": mine["id"], "start": "python server.py"}}
	for i := 1; i < 4; i++ {
		code, up := s.serviceUpload(t, a, ws, "service.tar", serviceTar(t), false)
		if code != 201 {
			t.Fatalf("upload %d %v", code, up)
		}
		services = append(services, map[string]any{"name": fmt.Sprintf("evaluator-%d", i), "attachment_id": up["id"], "start": "python server.py"})
	}
	if got := s.call(t, "POST", "/api/cards", a, ws, map[string]any{"question": "over count", "attachment_ids": files, "services": services}, nil); got != 400 {
		t.Fatalf("97 files plus 4 services: %d", got)
	}
	if n := s.count(t, `SELECT count(*) FROM card`) + s.count(t, `SELECT count(*) FROM record`) + s.count(t, `SELECT count(*) FROM attachment WHERE card_id IS NOT NULL`); n != 0 {
		t.Fatalf("count refusal left %d study/source/link rows", n)
	}
	// Repeating an ordinary id must not consume an additional upload slot.
	exact := append([]string{}, files[:96]...)
	exact = append(exact, files[0])
	var c card
	if got := s.call(t, "POST", "/api/cards", a, ws, map[string]any{"question": "exact count", "attachment_ids": exact, "services": services}, &c); got != 201 {
		t.Fatalf("96 files plus 4 services with repeated file id: %d", got)
	}
	if n := s.count(t, `SELECT count(*) FROM attachment WHERE card_id=$1`, c.ID); n != 100 {
		t.Fatalf("exact boundary linked %d uploads", n)
	}
	if n := s.count(t, `SELECT count(*) FROM attachment WHERE id=$1 AND card_id IS NULL`, files[96]); n != 1 {
		t.Fatal("unselected file was linked")
	}
}

func TestServiceTarGzipUsesContentAndSkipsText(t *testing.T) {
	s := newServer(t)
	tok := s.signIn(t, "a@lab.org")
	ws := s.workspace(t, tok, "lab").ID
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	if _, err := gz.Write(serviceTar(t)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	code, up := s.serviceUpload(t, tok, ws, "opaque.bin", b.Bytes(), false)
	if code != 201 {
		t.Fatalf("gzip upload %d %v", code, up)
	}
	var entries int
	var unpacked int64
	if err := s.Env.Owner.QueryRow(t.Context(), `SELECT inner_files,unpacked_bytes FROM attachment WHERE id=$1`, up["id"]).Scan(&entries, &unpacked); err != nil {
		t.Fatal(err)
	}
	if entries != 1 || unpacked != 8 {
		t.Fatalf("bounds %d %d", entries, unpacked)
	}
	if _, err := s.H.Blobs.Get(t.Context(), records.TextKey(ws+"/attachments/"+up["id"].(string))); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("text companion %v", err)
	}
}
