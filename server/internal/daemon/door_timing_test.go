package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDoorTimingKeepsResultsAndLogsNoPayloads(t *testing.T) {
	for _, outcome := range []string{"ok", "tool_error", "rpc_error"} {
		t.Run(outcome, func(t *testing.T) {
			var logs bytes.Buffer
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(old) })
			d := &Door{RunID: uuid.New(), Attempt: 2, Token: "private-door-token", RunToken: "private-run-token"}
			req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: &mcp.CallToolParamsRaw{Name: "read_output", Arguments: json.RawMessage(`{"path":"private-filename"}`)}}
			expected := &mcp.CallToolResult{IsError: outcome == "tool_error", Content: []mcp.Content{&mcp.TextContent{Text: "private-result"}}}
			var expectedErr error
			if outcome == "rpc_error" {
				expectedErr = errors.New("private-error")
			}
			called := false
			result, err := d.timeTools(func(ctx context.Context, method string, r mcp.Request) (mcp.Result, error) {
				called = true
				if method != "tools/call" || r != req {
					t.Fatal("request changed")
				}
				if !strings.Contains(logs.String(), "door tool started") {
					t.Fatal("start missing while tool runs")
				}
				return expected, expectedErr
			})(t.Context(), "tools/call", req)
			if !called || result != expected || err != expectedErr {
				t.Fatal("timing changed execution/result")
			}
			lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
			if len(lines) != 2 {
				t.Fatalf("logs: %s", logs.String())
			}
			var start, end map[string]any
			if err := json.Unmarshal([]byte(lines[0]), &start); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(lines[1]), &end); err != nil {
				t.Fatal(err)
			}
			if start["door_call_id"] == "" || start["door_call_id"] != end["door_call_id"] || end["run_id"] != d.RunID.String() || end["attempt"] != float64(2) || end["tool"] != "read_output" || end["outcome"] != outcome || end["duration_ms"] == nil {
				t.Fatalf("logs: %s", logs.String())
			}
			if strings.Contains(logs.String(), "private-") {
				t.Fatalf("payload leaked: %s", logs.String())
			}
		})
	}
}

func TestDoorTimingIgnoresOtherMethods(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	called := false
	_, err := (&Door{}).timeTools(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		called = true
		return nil, nil
	})(t.Context(), "tools/list", nil)
	if !called || err != nil || logs.Len() != 0 {
		t.Fatalf("called=%v error=%v logs=%s", called, err, logs.String())
	}
}
