package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListModelsParses(t *testing.T) {
	for _, h := range []string{"claude", "codex"} {
		t.Run(h, func(t *testing.T) {
			fixture, e := os.ReadFile(filepath.Join("testdata", h+"-models.json"))
			if e != nil {
				t.Fatal(e)
			}
			script := "#!/bin/sh\nread request\n"
			if h == "codex" {
				script += "echo '{\"id\":1,\"result\":{}}'\nread initialized\nread request\n"
			}
			script += "cat <<'JSON'\n" + string(fixture) + "JSON\nread wait\n"
			bin := filepath.Join(t.TempDir(), h)
			os.WriteFile(bin, []byte(script), 0700)
			called := false
			run := func(ctx context.Context, p CLIPaths, name string, args ...string) (*exec.Cmd, error) {
				called = true
				if p.Scratch == "" {
					t.Fatal("no scratch")
				}
				if h == "codex" && p.Keychain {
					t.Fatal("catalog must not access keychain")
				}
				return exec.CommandContext(ctx, name, args...), nil
			}
			models, e := ListModels(t.Context(), h, bin, run)
			if e != nil || !called || len(models) == 0 {
				t.Fatalf("%v %v", models, e)
			}
			var want string
			if h == "claude" {
				want = "default"
			} else {
				want = "gpt-6.1-sol"
			}
			if models[0].Value != want || models[0].Label == "" {
				t.Fatalf("order/labels %v", models)
			}
		})
	}
}
func TestListModelsUnknownAndBounds(t *testing.T) {
	for _, response := range []string{`{"id":2,"result":{"changed":[]}}`, `{"id":2,"result":{"data":[{"id":"x"}],"nextCursor":null}}`, `{"id":2,"result":{"data":[],"nextCursor":"loop"}}`, strings.Repeat("x", 2<<20)} {
		t.Run(string(rune(len(response))), func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "codex")
			script := "#!/bin/sh\nread init\necho '{\"id\":1,\"result\":{}}'\nwhile read request; do\ncat <<'JSON'\n" + response + "\nJSON\ndone\n"
			os.WriteFile(bin, []byte(script), 0700)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			got, _ := ListModels(ctx, "codex", bin, func(ctx context.Context, _ CLIPaths, n string, a ...string) (*exec.Cmd, error) {
				return exec.CommandContext(ctx, n, a...), nil
			})
			if len(got) != 0 {
				t.Fatalf("unknown/excessive response %v", got)
			}
		})
	}
}

func TestListModelsPagination(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "codex")
	script := `#!/bin/sh
read init
echo '{"id":1,"result":{}}'
read initialized
read request
echo '{"id":2,"result":{"data":[{"model":"second","displayName":"Second","description":""}],"nextCursor":"next"}}'
read request
case "$request" in *'"cursor":"next"'*) ;; *) exit 3;; esac
echo '{"id":3,"result":{"data":[{"model":"first","displayName":"First","isDefault":true},{"model":"secret","displayName":"Hidden","hidden":true}],"nextCursor":null}}'
read wait
`
	os.WriteFile(bin, []byte(script), 0700)
	got, e := ListModels(t.Context(), "codex", bin, func(ctx context.Context, _ CLIPaths, n string, a ...string) (*exec.Cmd, error) {
		return exec.CommandContext(ctx, n, a...), nil
	})
	if e != nil || len(got) != 2 || got[0].Value != "first" || got[1].Value != "second" {
		t.Fatalf("%v %v", got, e)
	}
}

func TestModelCatalogLimits(t *testing.T) {
	for _, catalog := range [][]Model{{{Value: "x", Label: "X"}, {Value: "x", Label: "Duplicate"}}, {{Value: "x", Label: strings.Repeat("x", 257)}}, {{Value: "x", Label: "X", Description: strings.Repeat("x", 2049)}}, make([]Model, 257)} {
		if ValidModels(catalog) {
			t.Fatalf("accepted invalid catalog %d", len(catalog))
		}
	}
	if !ValidModels([]Model{}) {
		t.Fatal("empty catalog is valid")
	}
}

func TestListModelsPageFailureDiscardsPartialCatalog(t *testing.T) {
	for _, last := range []string{`{"id":3,"error":{"code":-1,"message":"unavailable"}}`, `not-json`, ""} {
		bin := filepath.Join(t.TempDir(), "codex")
		script := `#!/bin/sh
read init
echo '{"id":1,"result":{}}'
read initialized
read request
echo '{"id":2,"result":{"data":[{"model":"partial","displayName":"Partial"}],"nextCursor":"next"}}'
read request
printf '%s\n' '` + last + `'
read wait
`
		if last == "" {
			script = strings.Replace(script, "printf '%s\\n' ''\nread wait", "exit 0", 1)
		}
		os.WriteFile(bin, []byte(script), 0700)
		got, e := ListModels(t.Context(), "codex", bin, func(ctx context.Context, _ CLIPaths, n string, a ...string) (*exec.Cmd, error) {
			return exec.CommandContext(ctx, n, a...), nil
		})
		if (last != "" && e != nil) || len(got) != 0 {
			t.Fatalf("failed catalog retained partial page: %v %v", got, e)
		}
	}
}
