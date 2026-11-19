package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// timeTools measures only work received by the door, after CLI approval.
func (d *Door) timeTools(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method != "tools/call" {
			return next(ctx, method, req)
		}
		tool := "unknown"
		if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok {
			switch p.Name {
			case "call_tool", "list_connectors", "run_code", "run_script", "set_up_environment", "read_output", "list_outputs", "find_tools", "list_source_tools", "search_literature", "fetch_text", "ask_member", "member_result":
				tool = p.Name
			}
		}
		log := slog.With("run_id", d.RunID, "attempt", d.Attempt, "door_call_id", uuid.New(), "tool", tool)
		started := time.Now()
		log.Info("door tool started")
		result, err := next(ctx, method, req)
		outcome := "ok"
		if err != nil {
			outcome = "rpc_error"
		} else if r, ok := result.(*mcp.CallToolResult); ok && r.IsError {
			outcome = "tool_error"
		}
		log.Info("door tool completed", "duration_ms", float64(time.Since(started))/float64(time.Millisecond), "outcome", outcome)
		return result, err
	}
}
