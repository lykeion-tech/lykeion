package handler

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"lykeion/server/internal/jev"
	"lykeion/server/internal/pack"
	"lykeion/server/internal/skill"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func importRoutingSkill(t *testing.T, l *teamLab, name string, binary []byte) skill.Detail {
	t.Helper()
	d, err := l.s.H.skillService().Create(context.Background(), uuid.MustParse(l.ws), uuid.MustParse(l.s.userID(t, l.jwt)), skill.Bundle{Files: map[string][]byte{"SKILL.md": []byte("---\nname: " + name + "\ndescription: Imported method\n---\n## Method\nUse the original method.\n"), "assets/reference.bin": binary}}, skill.Source{Kind: "local", Location: "method.zip"})
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func routedFolder(t *testing.T, l *teamLab, token, name string) []byte {
	t.Helper()
	var out toolsOut
	if code := l.s.call(t, "POST", "/api/role/tools", token, "", map[string]any{"need": "use the imported method"}, &out); code != 200 {
		t.Fatalf("tools %d: %s", code, out.Error)
	}
	for _, s := range out.Skills {
		if s.Name == name {
			return s.Folder
		}
	}
	t.Fatalf("imported skill %q was not routed", name)
	return nil
}
func folderFile(t *testing.T, data []byte, name string) []byte {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			t.Fatalf("missing %s", name)
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == name {
			b, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
	}
}
func TestImportedSkillRoutesUnderWorkspaceDomain(t *testing.T) {
	l := newAgentLab(t)
	j, asked := noulJev(t, 0.9)
	l.s.H.Engine.Jev = j
	binary := []byte{0, 255, 128, 1}
	importRoutingSkill(t, l, "imported-method", binary)
	folder := routedFolder(t, l, l.claim(t).token, "imported-method")
	if sha256.Sum256(folderFile(t, folder, "assets/reference.bin")) != sha256.Sum256(binary) {
		t.Fatal("binary asset changed")
	}
	found := false
	for _, q := range asked() {
		if strings.Contains(q, "The field") && strings.Contains(q, "Workspace") {
			found = true
		}
	}
	if !found {
		t.Fatal("imported domain was not Workspace")
	}
	if n := l.s.count(t, "SELECT count(*) FROM workspace_skill_pin WHERE scope_kind='card' AND scope_id=$1", l.card.ID); n != 1 {
		t.Fatalf("%d scope pins", n)
	}
}
func TestPinnedSkillSurvivesEditRenameDeleteAndReimport(t *testing.T) {
	l := newAgentLab(t)
	j, _ := noulJev(t, 0.9)
	l.s.H.Engine.Jev = j
	d := importRoutingSkill(t, l, "imported-method", []byte{0, 255, 128, 1})
	run := l.claim(t)
	first := sha256.Sum256(routedFolder(t, l, run.token, "imported-method"))
	content := "---\nname: renamed-method\ndescription: Changed method\n---\n## Method\nUse the revised method.\n"
	if code := l.s.call(t, "PATCH", "/api/skills/"+d.ID, l.jwt, l.ws, map[string]any{"revision": 1, "content": content}, nil); code != 200 {
		t.Fatalf("edit %d", code)
	}
	if sha256.Sum256(routedFolder(t, l, run.token, "imported-method")) != first {
		t.Fatal("old scope version drift")
	}
	next := *l
	next.card = card{}
	a := l.s.newAgent(t, l.jwt, l.ws, "second-agent", "claude")
	if code := l.s.call(t, "POST", "/api/cards", l.jwt, l.ws, map[string]any{"question": "A fresh study", "scope": map[string]string{"from": "grounding", "to": "grounding"}, "assignee": map[string]any{"type": "agent", "id": a}}, &next.card); code != 201 {
		t.Fatalf("new scope %d", code)
	}
	newer := next.claim(t)
	fresh := routedFolder(t, &next, newer.token, "renamed-method")
	if sha256.Sum256(fresh) == first || string(folderFile(t, fresh, "SKILL.md")) != content {
		t.Fatal("new scope did not use latest version")
	}
	if code := l.s.call(t, "DELETE", "/api/skills/"+d.ID, l.jwt, l.ws, map[string]any{"revision": 2}, nil); code != 204 {
		t.Fatalf("delete %d", code)
	}
	importRoutingSkill(t, l, "imported-method", []byte{5, 4, 3, 2})
	if sha256.Sum256(routedFolder(t, l, run.token, "imported-method")) != first {
		t.Fatal("reimport replaced old scope pin")
	}
	if sha256.Sum256(routedFolder(t, &next, newer.token, "renamed-method")) != sha256.Sum256(fresh) {
		t.Fatal("deleted version disappeared")
	}

	l.complete(t, run)
	resumed := l.claim(t)
	if resumed.id == run.id {
		t.Fatal("fixture did not advance the agent run")
	}
	if sha256.Sum256(routedFolder(t, l, resumed.token, "imported-method")) != first {
		t.Fatal("new attempt lost the study pin")
	}
}
func TestNewTeamContainsSelectedImportedFolder(t *testing.T) {
	s := newServer(t)
	jwt := s.signIn(t, "pi@lab.org")
	ws := s.workspace(t, jwt, "lab").ID
	l := &teamLab{s: s, jwt: jwt, ws: ws}
	binary := []byte{0, 255, 128, 1}
	d := importRoutingSkill(t, l, "imported-method", binary)
	lead, aud, mem := s.newAgent(t, jwt, ws, "lead", "claude"), s.newAgent(t, jwt, ws, "aud", "codex"), s.newAgent(t, jwt, ws, "mem", "claude")
	form := formFor(lead, aud, mem)
	form["leader"].(map[string]any)["skills"] = []string{"imported-method"}
	var made struct{ ID, Error string }
	if code := s.call(t, "POST", "/api/teams", jwt, ws, form, &made); code != 201 {
		t.Fatalf("create %d: %s", code, made.Error)
	}
	if code := s.call(t, "DELETE", "/api/skills/"+d.ID, jwt, ws, map[string]any{"revision": 1}, nil); code != 204 {
		t.Fatalf("delete %d", code)
	}
	var row db.FieldPack
	err := s.H.Store.InWorkspace(context.Background(), uuid.MustParse(ws), uuid.MustParse(s.userID(t, jwt)), func(tx *store.Tx) error {
		var err error
		row, err = tx.Q.GetFieldPack(context.Background(), db.GetFieldPackParams{WorkspaceID: tx.WS, ID: uuid.MustParse(made.ID)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := pack.Load(context.Background(), s.H.Blobs, row)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.SkillFiles("pi", "imported-method")["assets/reference.bin"]; !bytes.Equal(got, binary) {
		t.Fatalf("Team folder bytes %v", got)
	}
	if len(p.SkillFiles("scout", "imported-method")) != 0 {
		t.Fatal("undeclared member inherited imported folder")
	}

	l.personal = s.personalToken(t, jwt)
	var runtimes []struct{ ID string }
	if code := s.call(t, "POST", "/api/daemon/register", l.personal, "", map[string]any{"workspace_id": ws, "daemon_id": "d-team-import", "harnesses": []string{"claude"}}, &runtimes); code != 200 || len(runtimes) != 1 {
		t.Fatalf("register %d", code)
	}
	l.rts = runtimes[0].ID
	if code := s.call(t, "POST", "/api/cards", jwt, ws, map[string]any{"question": "Use the frozen Team", "assignee": teamAssignee(map[string]string{"grounding": made.ID})}, &l.card); code != 201 {
		t.Fatalf("card %d", code)
	}
	j, _ := noulJev(t, .9)
	s.H.Engine.Jev = j
	if got := folderFile(t, routedFolder(t, l, l.claim(t).token, "imported-method"), "assets/reference.bin"); !bytes.Equal(got, binary) {
		t.Fatal("Team routing lost frozen binary")
	}
}
func TestTeamCannotRouteUndeclaredWorkspaceSkill(t *testing.T) {
	l := newTeamLab(t, 0)
	j, asked := noulJev(t, 0.9)
	l.s.H.Engine.Jev = j
	importRoutingSkill(t, l, "undeclared-import", []byte{0, 255})
	var out toolsOut
	if code := l.s.call(t, "POST", "/api/role/tools", l.claim(t).token, "", map[string]any{"need": "use undeclared-import"}, &out); code != 200 {
		t.Fatalf("tools %d", code)
	}
	for _, s := range out.Skills {
		if s.Name == "undeclared-import" {
			t.Fatal("Team escaped its declared library")
		}
	}
	for _, q := range asked() {
		if strings.Contains(q, `skill "undeclared-import"`) {
			t.Fatal("Jev considered an undeclared import")
		}
	}
}

func TestImportedSkillRoutesOnlySelectedSectionsAndPins(t *testing.T) {
	l := newAgentLab(t)
	d := importRoutingSkill(t, l, "selected-import", []byte{0, 255})
	importRoutingSkill(t, l, "rejected-import", []byte{3, 4})
	content := "---\nname: selected-import\ndescription: A method\n---\n## Keep\nKeep this instruction.\n## Reject\nDo not route this instruction.\n"
	if code := l.s.call(t, "PATCH", "/api/skills/"+d.ID, l.jwt, l.ws, map[string]any{"revision": 1, "content": content}, nil); code != 200 {
		t.Fatalf("edit %d", code)
	}
	l.s.H.Engine.Jev = routingJev(t, func(q string) float64 {
		if strings.HasPrefix(q, "Is this field") && strings.Contains(q, "Workspace") {
			return .9
		}
		if strings.HasPrefix(q, "Could the skill") && strings.Contains(q, `"selected-import"`) {
			return .9
		}
		if strings.HasPrefix(q, "Does this section") && strings.Contains(q, "## Keep") {
			return .9
		}
		return .1
	}, nil)
	var out toolsOut
	if code := l.s.call(t, "POST", "/api/role/tools", l.claim(t).token, "", map[string]any{"need": "Keep"}, &out); code != 200 {
		t.Fatalf("route %d", code)
	}
	if len(out.Skills) != 1 || out.Skills[0].Name != "selected-import" || len(out.Skills[0].Sections) != 1 || out.Skills[0].Sections[0].Heading != "Keep" {
		t.Fatalf("selection %+v", out.Skills)
	}
	if string(folderFile(t, out.Skills[0].Folder, "SKILL.md")) != content {
		t.Fatal("full folder lost non-routed sections")
	}
	if n := l.s.count(t, "SELECT count(*) FROM workspace_skill_pin WHERE scope_id=$1", l.card.ID); n != 1 {
		t.Fatalf("pinned %d skills", n)
	}
}

func routingJev(t *testing.T, probability func(string) float64, hook func()) *jev.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Questions map[string]struct{ Instructions string }
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
			return
		}
		if hook != nil {
			hook()
		}
		out := map[string]any{}
		for id, q := range in.Questions {
			out[id] = map[string]any{"type": "noul", "noul": probability(q.Instructions)}
		}
		json.NewEncoder(w).Encode(map[string]any{"model": jev.Model, "answers": out})
	}))
	t.Cleanup(srv.Close)
	return &jev.Client{BaseURL: srv.URL, Key: "k", Model: jev.Model}
}
func TestPinnedSkillRejectsEditDuringJevAndFencesFinishedRun(t *testing.T) {
	for _, change := range []string{"edit", "finish"} {
		t.Run(change, func(t *testing.T) {
			l := newAgentLab(t)
			d := importRoutingSkill(t, l, "race-import", []byte{0, 255})
			run := l.claim(t)
			var once sync.Once
			l.s.H.Engine.Jev = routingJev(t, func(string) float64 { return .9 }, func() {
				once.Do(func() {
					if change == "finish" {
						l.complete(t, run)
						return
					}
					if code := l.s.call(t, "PATCH", "/api/skills/"+d.ID, l.jwt, l.ws, map[string]any{"revision": 1, "content": "---\nname: race-import\n---\nChanged instructions"}, nil); code != 200 {
						t.Errorf("edit during Jev %d", code)
					}
				})
			})
			want := 409
			if change == "finish" {
				want = 401
			}
			var out toolsOut
			if code := l.s.call(t, "POST", "/api/role/tools", run.token, "", map[string]any{"need": "method"}, &out); code != want {
				t.Fatalf("changed %s: %d want %d", change, code, want)
			}
			if n := l.s.count(t, "SELECT count(*) FROM workspace_skill_pin WHERE scope_id=$1", l.card.ID); n != 0 {
				t.Fatalf("failed routing published %d pins", n)
			}
		})
	}
}
func TestPinnedSkillAutoresearchUsesRunIdentity(t *testing.T) {
	s := newServer(t)
	jwt := s.signIn(t, "pi@lab.org")
	ws := s.workspace(t, jwt, "lab").ID
	l := &teamLab{s: s, jwt: jwt, ws: ws}
	l.personal = s.personalToken(t, jwt)
	var rts []struct{ ID string }
	if code := s.call(t, "POST", "/api/daemon/register", l.personal, "", map[string]any{"workspace_id": ws, "daemon_id": "d-ar", "harnesses": []string{"claude"}}, &rts); code != 200 || len(rts) != 1 {
		t.Fatalf("register %d", code)
	}
	l.rts = rts[0].ID
	agent := s.newAgent(t, jwt, ws, "digest", "claude")
	ar := s.assignedRun(t, jwt, ws, map[string]any{"title": "Digest", "execution_mode": "run_only", "assignee": map[string]any{"type": "agent", "id": agent}})
	importRoutingSkill(t, l, "ar-import", []byte{0, 255})
	j, _ := noulJev(t, .9)
	s.H.Engine.Jev = j
	run := l.claim(t)
	routedFolder(t, l, run.token, "ar-import")
	if n := s.count(t, "SELECT count(*) FROM workspace_skill_pin WHERE scope_kind='autoresearch_run' AND scope_id=$1", ar.ID); n != 1 {
		t.Fatalf("%d Autoresearch pins", n)
	}
	if n := s.count(t, "SELECT count(*) FROM workspace_skill_pin WHERE scope_id=$1", run.id); n != 0 {
		t.Fatal("pin used agent attempt identity")
	}
}
func TestNewTeamContainsNoPublicationAfterLibraryChangesDuringBlobWrite(t *testing.T) {
	s := newServer(t)
	jwt := s.signIn(t, "pi@lab.org")
	ws := s.workspace(t, jwt, "lab").ID
	l := &teamLab{s: s, jwt: jwt, ws: ws}
	d := importRoutingSkill(t, l, "team-race", []byte{0, 255})
	lead, aud, mem := s.newAgent(t, jwt, ws, "lead", "claude"), s.newAgent(t, jwt, ws, "aud", "codex"), s.newAgent(t, jwt, ws, "mem", "claude")
	f := formFor(lead, aud, mem)
	f["leader"].(map[string]any)["skills"] = []string{"team-race"}
	s.H.Blobs = skillBlobHook{Storage: s.H.Blobs, hook: func() error {
		ctx := context.Background()
		tx, err := s.Env.Owner.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, "SELECT id FROM workspace WHERE id=$1 FOR UPDATE NOWAIT", ws); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE workspace_skill SET deleted_at=now() WHERE workspace_id=$1 AND id=$2", ws, d.ID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}}
	if code := s.call(t, "POST", "/api/teams", jwt, ws, f, nil); code != 409 {
		t.Fatalf("Team publication after deletion %d", code)
	}
	if n := s.count(t, "SELECT count(*) FROM field_pack"); n != 0 {
		t.Fatal("stale Team published")
	}
}

func TestNewTeamContainsNoTruncatedOversizedImportedFolder(t *testing.T) {
	s := newServer(t)
	jwt := s.signIn(t, "pi@lab.org")
	ws := s.workspace(t, jwt, "lab").ID
	files := map[string][]byte{"SKILL.md": []byte("---\nname: large-import\n---\nA large complete folder.")}
	for i := 0; i < 199; i++ {
		files[fmt.Sprintf("assets/file-%03d.bin", i)] = []byte{0, 255}
	}
	if _, err := s.H.skillService().Create(context.Background(), uuid.MustParse(ws), uuid.MustParse(s.userID(t, jwt)), skill.Bundle{Files: files}, skill.Source{Kind: "local"}); err != nil {
		t.Fatal(err)
	}
	lead, aud, mem := s.newAgent(t, jwt, ws, "lead", "claude"), s.newAgent(t, jwt, ws, "aud", "codex"), s.newAgent(t, jwt, ws, "mem", "claude")
	f := formFor(lead, aud, mem)
	f["leader"].(map[string]any)["skills"] = []string{"large-import"}
	var out struct{ Error string }
	if code := s.call(t, "POST", "/api/teams", jwt, ws, f, &out); code != 400 || !strings.Contains(out.Error, "entries") {
		t.Fatalf("oversized Team %d: %s", code, out.Error)
	}
	if n := s.count(t, "SELECT count(*) FROM field_pack"); n != 0 {
		t.Fatal("oversized Team metadata published")
	}
	if n := s.count(t, "SELECT count(*) FROM event WHERE type='team.created' OR type='team.added'"); n != 0 {
		t.Fatal("oversized Team event published")
	}
}

func TestNewTeamContainsNoPublicationAfterMembershipRevocation(t *testing.T) {
	s := newServer(t)
	owner := s.signIn(t, "owner@lab.org")
	ws := s.workspace(t, owner, "lab").ID
	jwt := s.member(t, owner, ws, "importer@lab.org", "member")
	who := s.userID(t, jwt)
	l := &teamLab{s: s, jwt: jwt, ws: ws}
	importRoutingSkill(t, l, "team-revoked", []byte{0, 255})
	lead, aud, mem := s.newAgent(t, jwt, ws, "lead", "claude"), s.newAgent(t, jwt, ws, "aud", "codex"), s.newAgent(t, jwt, ws, "mem", "claude")
	f := formFor(lead, aud, mem)
	f["leader"].(map[string]any)["skills"] = []string{"team-revoked"}
	s.H.Blobs = skillBlobHook{Storage: s.H.Blobs, hook: func() error {
		_, err := s.Env.Owner.Exec(context.Background(), "DELETE FROM member WHERE workspace_id=$1 AND user_id=$2", ws, who)
		return err
	}}
	if code := s.call(t, "POST", "/api/teams", jwt, ws, f, nil); code != 404 {
		t.Fatalf("revoked Team creation %d", code)
	}
	if n := s.count(t, "SELECT count(*) FROM field_pack"); n != 0 {
		t.Fatal("revoked Team metadata published")
	}
	if n := s.count(t, "SELECT count(*) FROM event WHERE type='team.created' OR type='team.added'"); n != 0 {
		t.Fatal("revoked Team event published")
	}
}
