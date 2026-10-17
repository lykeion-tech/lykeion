package daemon

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lykeion/server/internal/pack"
	"lykeion/server/pkg/agent"
)

func skillTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		must(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o644, Size: int64(len(body))}))
		_, err := tw.Write([]byte(body))
		must(t, err)
	}
	must(t, tw.Close())
	return buf.Bytes()
}

func TestRoutedSkillsLandWhereTheCLIReadsThem(t *testing.T) {
	self, err := os.Executable()
	must(t, err)
	folder := skillTar(t, map[string]string{"SKILL.md": "---\nname: rdkit\ndescription: d\n---\n## Usage\nUse it.\n", "scripts/run.py": "print(1)\n"})
	var asked []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/role/tools" || r.Header.Get("Authorization") != "Bearer ltk_run" {
			http.NotFound(w, r)
			return
		}
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		asked = append(asked, in)
		json.NewEncoder(w).Encode(map[string]any{
			"skills": []any{map[string]any{"name": "rdkit", "sections": []any{map[string]string{"heading": "Usage", "text": "Use it.\n"}}, "folder": folder}},
			"tools":  []any{map[string]string{"server": "stub", "tool": "echo"}},
		})
	}))
	t.Cleanup(srv.Close)

	for _, c := range []struct {
		harness string
		inherit bool
		lands   string // the skill's folder relative to the run folder, or "" for none
	}{
		{"claude", true, ".claude/skills/lyk-rdkit"},
		{"codex", true, ".agents/skills/lyk-rdkit"},
		{"claude", false, ""},
		{"cursor", true, ""},
		{"antigravity", true, ""},
	} {
		dir := t.TempDir()
		d := &Door{Server: srv.URL, RunToken: "ltk_run", CallToken: "ltk_call", Harness: c.harness,
			Connectors: []pack.Connector{{Name: "stub", Command: []string{self}, Env: map[string]string{"LYK_DOOR_STUB": "1"}}, {Catalogue: "uniprot"}},
			Child:      func() agent.ExecOptions { return agent.ExecOptions{Dir: dir, Inherit: c.inherit} }}
		must(t, d.Start())
		req, _ := http.NewRequest("POST", strings.TrimSuffix(d.URL, "/mcp")+"/tools", strings.NewReader(`{"need":"draw a molecule"}`))
		req.Header.Set("Authorization", "Bearer "+d.Token)
		resp, err := http.DefaultClient.Do(req)
		must(t, err)
		var body bytes.Buffer
		body.ReadFrom(resp.Body)
		resp.Body.Close()
		d.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d %s", c.harness, resp.StatusCode, body.String())
		}
		if strings.Contains(body.String(), "folder") || !strings.Contains(body.String(), `"Use it.\n"`) || !strings.Contains(body.String(), `"echo"`) {
			t.Fatalf("%s: the door answered %s", c.harness, body.String())
		}
		for _, p := range []string{".claude", ".agents", ".cursor"} {
			if _, err := os.Stat(filepath.Join(dir, p)); err == nil && (c.lands == "" || !strings.HasPrefix(c.lands, p)) {
				t.Fatalf("%s inherit=%v: %s was written", c.harness, c.inherit, p)
			}
		}
		if c.lands == "" {
			continue
		}
		md, err := os.ReadFile(filepath.Join(dir, c.lands, "SKILL.md"))
		if err != nil || !strings.Contains(string(md), "name: lyk-rdkit") {
			t.Fatalf("%s: SKILL.md %q %v", c.harness, md, err)
		}
		if _, err := os.Stat(filepath.Join(dir, c.lands, "scripts", "run.py")); err != nil {
			t.Fatalf("%s: the whole folder did not land: %v", c.harness, err)
		}
	}
	if len(asked) != 5 {
		t.Fatalf("%d requests reached the server", len(asked))
	}
	tools, _ := asked[0]["connector_tools"].(map[string]any)
	if stub, _ := json.Marshal(tools["stub"]); !strings.Contains(string(stub), `"echo"`) || tools["uniprot"] != nil || asked[0]["need"] != "draw a molecule" {
		t.Fatalf("the server was asked %+v", asked[0])
	}
}
