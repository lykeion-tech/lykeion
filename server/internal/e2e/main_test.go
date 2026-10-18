package e2e

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("LYK_UI_CONNECTOR_STDIO") == "1" {
		uiConnectorStdio()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func uiConnectorStdio() {
	srv := mcp.NewServer(&mcp.Implementation{Name: "ui-stdio", Version: "1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		// Exact argv and process environment make shell splitting observable.
		raw, _ := json.Marshal(map[string]any{"args": os.Args[1:], "mode": os.Getenv("LAB_MODE"), "q": in["q"]})
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil, nil
	})
	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(2)
	}
}
