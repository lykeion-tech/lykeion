package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolsPrintsSectionsAndTools(t *testing.T) {
	t.Setenv("LYK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	answer := `{"skills":[{"name":"rdkit","sections":[{"heading":"Usage","text":"Call MolFromSmiles.\n"},{"heading":"","text":"Intro text.\n"}]}],` +
		`"tools":[{"server":"chembl","tool":"search","description":"find molecules"}]}`
	var needs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/tools" || r.Header.Get("Authorization") != "Bearer door-secret" {
			t.Errorf("unexpected request %s %s %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		var in struct{ Need string }
		json.NewDecoder(r.Body).Decode(&in)
		needs = append(needs, in.Need)
		w.Header().Set("Content-Type", "application/json")
		if in.Need == "nothing" {
			w.Write([]byte(`{"skills":[],"tools":[]}`))
			return
		}
		w.Write([]byte(answer))
	}))
	defer srv.Close()
	t.Setenv("LYK_DOOR", srv.URL+"/mcp")
	t.Setenv("LYK_DOOR_TOKEN", "door-secret")

	out := lyk(t, "", "tools", "parse", "a", "SMILES")
	for _, want := range []string{"rdkit", "## Usage", "Call MolFromSmiles.", "Intro text.", `call_tool server "chembl" tool "search"`, "find molecules"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "rdkit") > strings.Index(out, "call_tool") {
		t.Fatalf("tools printed before skills:\n%s", out)
	}
	if got := lyk(t, "", "tools", "nothing"); got != "Nothing in this study's tools fits that need; the researcher has been told.\n" {
		t.Fatalf("empty output %q", got)
	}
	if got := lyk(t, "", "tools", "parse", "--output", "json"); !strings.Contains(got, `"server":"chembl"`) {
		t.Fatalf("json output %q", got)
	}
	if len(needs) != 3 || needs[0] != "parse a SMILES" {
		t.Fatalf("needs %q", needs)
	}
}
