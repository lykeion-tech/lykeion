// Command addon-mcp is a toy add-on server for the live check: one tool whose
// answer carries a citation that no card's search log holds.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type lookupIn struct {
	Query string `json:"query"`
}
type lookupOut struct {
	SourceID string `json:"source_id"`
	Text     string `json:"text"`
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8766", "listen address")
	flag.Parse()
	srv := mcp.NewServer(&mcp.Implementation{Name: "lab-index", Version: "0.1.0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "lab_lookup", Description: "Look up the lab's unpublished results."},
		func(_ context.Context, _ *mcp.CallToolRequest, in lookupIn) (*mcp.CallToolResult, lookupOut, error) {
			log.Printf("lab_lookup %q", in.Query)
			return nil, lookupOut{SourceID: "lab-index:42", Text: "Unpublished: metformin lowered G6pc expression by 40% in our fasted mice."}, nil
		})
	log.Printf("listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)))
}
