package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The release's actual model-facing registry is the boundary under test. The
// loopback provider requests every generic resource operation, including a
// read from a server that is present in hostile personal configuration.
func TestLabCodexNativeInventory(t *testing.T) {
	bin := os.Getenv("LYK_TEST_CODEX_BIN")
	if bin == "" {
		t.Skip("set LYK_TEST_CODEX_BIN to inspect the installed native tool inventory")
	}

	mockDoor := mcp.NewServer(&mcp.Implementation{Name: "lyk_door", Version: "1"}, nil)
	mcp.AddTool(mockDoor, &mcp.Tool{Name: "call_tool"}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, nil, nil
	})
	door := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mockDoor }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer door.Close()
	var personalRequests atomic.Int32
	personal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { personalRequests.Add(1); http.Error(w, "refused", 403) }))
	defer personal.Close()

	home := t.TempDir()
	env := []string{"HOME=" + home, "CODEX_HOME=" + home, "PATH=" + os.Getenv("PATH"), "CODEX_EXEC_SERVER_URL=none"}
	secretConfig := `model_provider="personal"
[model_providers.personal]
name="personal"
wire_api="responses"
base_url=` + string(raw(personal.URL)) + `
[mcp_servers.personal]
url=` + string(raw(personal.URL))
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(secretConfig), 0600); err != nil {
		t.Fatal(err)
	}
	catalogPath := labProbeCatalog(t, bin, home, env)

	calls := []struct {
		name string
		args map[string]string
	}{
		{"list_mcp_resources", map[string]string{"server": "lyk_door"}},
		{"list_mcp_resource_templates", map[string]string{"server": "lyk_door"}},
		{"read_mcp_resource", map[string]string{"server": "lyk_door", "uri": "fixture://probe"}},
		{"list_mcp_resources", map[string]string{"server": "personal"}},
		{"read_mcp_resource", map[string]string{"server": "personal", "uri": "fixture://probe"}},
	}
	var turns atomic.Int32
	fakeModel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Tools []struct {
				Type, Name string
				Tools      []struct{ Name string }
			}
			Input []struct {
				Type   string
				Output json.RawMessage
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		var names []string
		for _, tool := range request.Tools {
			if tool.Type == "namespace" {
				for _, child := range tool.Tools {
					names = append(names, tool.Name+"."+child.Name)
				}
			} else {
				names = append(names, tool.Name)
			}
		}
		slices.Sort(names)
		want := []string{"list_mcp_resource_templates", "list_mcp_resources", "mcp__lyk_door.call_tool", "read_mcp_resource"}
		if !slices.Equal(names, want) {
			t.Errorf("reassess native tool boundary for this release: %v", names)
		}
		n := int(turns.Add(1)) - 1
		if n == len(calls) {
			var results []string
			for _, item := range request.Input {
				if item.Type == "function_call_output" {
					var out string
					if err := json.Unmarshal(item.Output, &out); err != nil {
						t.Error(err)
					}
					results = append(results, out)
				}
			}
			if len(results) != len(calls) || !strings.Contains(results[0], `"resources":[]`) || !strings.Contains(results[1], `"resourceTemplates":[]`) || !strings.Contains(results[2], "failed") || !strings.Contains(results[3], "unknown MCP server") || !strings.Contains(results[4], "unknown MCP server") {
				t.Errorf("resource boundaries: %q", results)
			}
			http.Error(w, "end of synthetic probe", 400)
			return
		}
		if n >= len(calls) {
			http.Error(w, "finished", 400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		event := func(kind string, fields map[string]any) {
			fields["type"] = kind
			fmt.Fprintf(w, "data: %s\n\n", raw(fields))
		}
		id := fmt.Sprintf("resp_%d", n)
		event("response.created", map[string]any{"response": map[string]any{"id": id}})
		item := map[string]any{"type": "function_call", "id": fmt.Sprintf("fc_%d", n), "call_id": fmt.Sprintf("c_%d", n), "name": calls[n].name, "arguments": string(raw(calls[n].args))}
		event("response.output_item.done", map[string]any{"output_index": 0, "item": item})
		event("response.completed", map[string]any{"response": map[string]any{"id": id, "status": "completed", "output": []any{item}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}})
	}))
	defer fakeModel.Close()
	args := []string{"exec", "--json", "--strict-config", "--ignore-user-config", "--ignore-rules", "--skip-git-repo-check", "-s", "read-only", "-m", "gpt-5.5"}
	for _, flag := range []string{"shell_tool", "unified_exec", "skill_search", "image_generation", "view_image", "goals", "sleep_tool", "tool_suggest", "apps", "hooks", "memories", "multi_agent", "plugins", "remote_plugin", "skill_mcp_dependency_install", "code_mode", "code_mode_host", "request_permissions_tool", "browser_use"} {
		args = append(args, "--disable", flag)
	}
	for _, setting := range []string{
		`web_search="disabled"`, "skills.bundled.enabled=false", "skills.include_instructions=false", "project_doc_max_bytes=0", "orchestrator.skills.enabled=false", "orchestrator.mcp.enabled=false", "tools.update_plan.enabled=false", "tools.experimental_request_user_input.enabled=false", "model_catalog_json=" + string(raw(catalogPath)),
		`model_provider="local_fixture"`, `model_providers.local_fixture={name="fixture",base_url=` + string(raw(fakeModel.URL)) + `,wire_api="responses",requires_openai_auth=false,request_max_retries=0,stream_max_retries=0}`,
		`mcp_servers.lyk_door={url=` + string(raw(door.URL)) + `,enabled_tools=["call_tool"]}`,
	} {
		args = append(args, "-c", setting)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append(args, "synthetic lab probe")...)
	cmd.Dir, cmd.Env = home, env
	output, err := cmd.CombinedOutput()
	if turns.Load() != int32(len(calls)+1) || personalRequests.Load() != 0 {
		t.Fatalf("turns=%d personal requests=%d CLI=%v %s", turns.Load(), personalRequests.Load(), err, output)
	}
}

func labProbeCatalog(t *testing.T, bin, home string, env []string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), bin, "debug", "models", "--bundled")
	cmd.Dir, cmd.Env = home, env
	data, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	if err = json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	for _, model := range catalog.Models {
		if string(model["slug"]) != `"gpt-5.5"` {
			continue
		}
		model["apply_patch_tool_type"] = raw(nil)
		model["shell_type"] = raw("disabled")
		model["experimental_supported_tools"] = raw([]string{})
		model["tool_mode"] = raw("direct")
		model["multi_agent_version"] = raw("disabled")
		for _, k := range []string{"supports_parallel_tool_calls", "supports_search_tool", "include_skills_usage_instructions", "include_plugin_usage_instructions", "include_apps_usage_instructions"} {
			model[k] = raw(false)
		}
		catalog.Models = []map[string]json.RawMessage{model}
		path := filepath.Join(home, "sanitized-model.json")
		if err = os.WriteFile(path, raw(catalog), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	t.Fatal("test model absent from installed CLI catalog")
	return ""
}
