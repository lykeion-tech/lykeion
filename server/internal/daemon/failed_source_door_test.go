package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFailedSourceCallIsRecorded(t *testing.T) {
	l := newLykeion(t)
	src := mcp.NewServer(&mcp.Implementation{Name: "sources", Version: "1"}, nil)
	mcp.AddTool(src, &mcp.Tool{Name: "search_literature"}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, map[string]any, error) {
		return nil, nil, errors.New("429 Retry-After: 30")
	})
	upstream := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return src }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer upstream.Close()
	d := openDoor(t, l, t.TempDir(), nil)
	d.Sources = upstream.URL
	_, err := callTool(t, d, "sources", "search_literature", map[string]any{"database": "openalex", "query": "cells"})
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("error = %v", err)
	}
	if len(l.posts) != 1 || !strings.Contains(strings.ToLower(strings.TrimSpace(toString(l.posts[0]["error"]))), "429") {
		t.Fatalf("failed call records = %+v", l.posts)
	}
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func TestFailedSourceCallIsRecordedAfterCallContextCancellation(t *testing.T) {
	l := newLykeion(t)
	d := &Door{Server: l.URL, CallToken: "ltk_call"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := d.recordSourceError(ctx, callIn{Server: "sources", Tool: "search_literature"}, context.DeadlineExceeded)
	if err == nil {
		t.Fatal("lost failure")
	}
	if len(l.posts) != 1 {
		t.Fatalf("failed call records = %+v", l.posts)
	}
}
