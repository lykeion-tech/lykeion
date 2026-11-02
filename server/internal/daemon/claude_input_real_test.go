package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lykeion/server/pkg/agent"
)

const commentSchema = `{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"],"additionalProperties":false}`

// A comment sent while a sandboxed Claude turn with a schema is working
// reaches that turn: its structured answer follows the comment. The turn
// waits on a door tool, and the comment is sent while the tool runs. It
// spends real tokens, so it runs only on request.
func TestRealClaudeTakesAComment(t *testing.T) {
	if os.Getenv("LYK_REAL_CLI") != "1" {
		t.Skip("set LYK_REAL_CLI=1 to spend real tokens on a Claude turn")
	}
	bin, ok := Detect()["claude"]
	if !ok {
		t.Skip("Claude Code is not installed")
	}
	l := newLykeion(t)
	base, err := os.UserHomeDir()
	must(t, err)
	home, err := os.MkdirTemp(base, ".lyk-comment-")
	must(t, err)
	t.Cleanup(func() { os.RemoveAll(home) })
	d := &daemon{cfg: Config{Home: home, StateDir: filepath.Join(home, "state"), Server: l.URL, Harnesses: map[string]string{"claude": bin}, NewBackend: agent.New}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	input := make(chan string, 1)
	var once sync.Once
	door := mcp.NewServer(&mcp.Implementation{Name: "lyk_door", Version: "0"}, nil)
	mcp.AddTool(door, &mcp.Tool{Name: "wait_for_researcher", Description: "Wait a few seconds for the researcher."},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			once.Do(func() { input <- commentText("the answer must be 7") })
			time.Sleep(8 * time.Second)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Done waiting."}}}, nil, nil
		})
	srv := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return door }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer srv.Close()

	run := t.TempDir()
	path, err := runPath()
	must(t, err)
	opts := agent.ExecOptions{Dir: run, CacheDir: d.runCacheDir(""), Env: []string{"PATH=" + path}, OutputSchema: commentSchema,
		DoorURL: srv.URL, DoorToken: "door", Input: input, OnMessage: func(agent.Message) error { return nil }}
	opts.Sandbox = d.cliSandbox(run, nil, false)
	backend, err := agent.New("claude", bin)
	must(t, err)
	sess, err := backend.Execute(ctx, "Call the wait_for_researcher tool once. When it returns, give your answer: n is the number a researcher's comment gives, or 0 if no comment came.", opts)
	must(t, err)
	res := <-sess.Done
	if res.Err != nil {
		t.Fatalf("turn failed: %v", res.Err)
	}
	var got struct{ N int }
	if err := json.Unmarshal([]byte(res.Output), &got); err != nil || got.N != 7 {
		t.Fatalf("answer %q does not take the comment", res.Output)
	}
	t.Logf("answer %s", res.Output)
}
