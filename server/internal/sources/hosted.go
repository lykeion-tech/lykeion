package sources

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lykeion/server/internal/middleware"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

// Hosted serves the sources to roles over MCP. Every call is recorded on the
// role's card, under the role, by the server: agents never report their own
// sources.
type Hosted struct {
	OA    OpenAlex
	EPMC  EuropePMC
	GEO   GEO
	Store *store.Store
	Blobs storage.Storage
}

func (h *Hosted) Handler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return h.server(middleware.RoleFrom(r.Context()))
	}, nil)
}

// runToken is the bearer of the HTTP request carrying an MCP call.
func runToken(req *mcp.CallToolRequest) string {
	if req == nil || req.Extra == nil {
		return ""
	}
	t, _ := strings.CutPrefix(req.Extra.Header.Get("Authorization"), "Bearer ")
	return t
}

// Record stores one search or fetch. Text, when present, goes to blob storage
// under its hash before the transaction, as the role blob upload does; only
// the blob.created event committed with the record makes it readable. The
// run is re-checked under row locks: it may have settled while the source
// was called.
func (h *Hosted) Record(ctx context.Context, role middleware.Role, token, kind, key string, body map[string]any, text string) error {
	blobKey, hexsum := "", ""
	if text != "" {
		sum := sha256.Sum256([]byte(text))
		hexsum = hex.EncodeToString(sum[:])
		blobKey = role.WorkspaceID.String() + "/" + role.CardID.String() + "/" + hexsum
		if err := h.Blobs.Put(ctx, blobKey, bytes.NewReader([]byte(text)), int64(len(text))); err != nil {
			return err
		}
		body["sha256"] = hexsum
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return h.Store.InWorkspace(ctx, role.WorkspaceID, role.AgentRunID, func(tx *store.Tx) error {
		if err := middleware.LockRole(ctx, tx, role, token); err != nil {
			return err
		}
		if blobKey != "" {
			if err := tx.Emit(ctx, "blob.created", &role.CardID, store.Actor{Type: "role", ID: role.AgentRunID},
				map[string]any{"blob_key": blobKey, "sha256": hexsum, "size": int64(len(text))}); err != nil {
				return err
			}
		}
		rec, err := tx.Q.InsertRecord(ctx, db.InsertRecordParams{WorkspaceID: role.WorkspaceID, CardID: role.CardID,
			BoardColumn: role.Column, Round: role.Round, Kind: kind, Role: role.Role, Key: key, Body: b,
			BlobKey: blobKey, AgentRunID: &role.AgentRunID})
		if err != nil {
			return err
		}
		return tx.Emit(ctx, "record.created", &role.CardID, store.Actor{Type: "role", ID: role.AgentRunID},
			map[string]any{"record_id": rec.ID, "kind": kind, "key": key, "role": role.Role})
	})
}

type SearchIn struct {
	Database string `json:"database" jsonschema:"openalex or europepmc"`
	Query    string `json:"query" jsonschema:"search terms"`
}
type HitsOut struct {
	Results []Hit `json:"results"`
}
type FetchIn struct {
	SourceID string `json:"source_id" jsonschema:"an id returned by search_literature"`
}
type FetchOut struct {
	SourceID  string `json:"source_id"`
	FullText  bool   `json:"full_text"`
	Retracted bool   `json:"retracted"`
	Text      string `json:"text"`
}
type DatasetsIn struct {
	Query string `json:"query" jsonschema:"search terms"`
}
type OpenIn struct {
	Accession string `json:"accession" jsonschema:"a GEO accession such as GSE12345"`
}
type OpenOut struct {
	Accession string `json:"accession"`
	Structure string `json:"structure"`
}

func ids(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}

func (h *Hosted) server(role middleware.Role) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "lyk_sources", Version: "0.1.0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "search_literature", Description: "Search OpenAlex or Europe PMC. Every query is recorded on the card."},
		func(ctx context.Context, req *mcp.CallToolRequest, in SearchIn) (*mcp.CallToolResult, HitsOut, error) {
			var hits []Hit
			var err error
			switch in.Database {
			case "openalex":
				hits, err = h.OA.Search(ctx, in.Query)
			case "europepmc":
				hits, err = h.EPMC.Search(ctx, in.Query)
			default:
				return nil, HitsOut{}, fmt.Errorf("database must be openalex or europepmc")
			}
			if err != nil {
				return nil, HitsOut{}, err
			}
			for i := range hits {
				if len(hits[i].Abstract) > 500 {
					hits[i].Abstract = hits[i].Abstract[:500] + "…"
				}
			}
			return nil, HitsOut{Results: hits}, h.Record(ctx, role, runToken(req), "search", "", map[string]any{"database": in.Database, "query": in.Query, "results": ids(hits)}, "")
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "fetch_text", Description: "Fetch a source's open full text, or its abstract when there is none. Only fetched sources can be cited, and quotes must be copied from this text."},
		func(ctx context.Context, req *mcp.CallToolRequest, in FetchIn) (*mcp.CallToolResult, FetchOut, error) {
			dbName, id, _ := strings.Cut(in.SourceID, ":")
			var d Doc
			var err error
			switch dbName {
			case "openalex":
				d, err = h.OA.Get(ctx, id)
			case "europepmc":
				d, err = h.EPMC.Get(ctx, id)
			default:
				return nil, FetchOut{}, fmt.Errorf("source id must start with openalex: or europepmc:")
			}
			if err != nil {
				return nil, FetchOut{}, err
			}
			if d.Text == "" {
				return nil, FetchOut{}, fmt.Errorf("%s has no text to read", in.SourceID)
			}
			body := map[string]any{"source_id": in.SourceID, "database": dbName, "doi": d.DOI, "full_text": d.FullText, "retracted": d.Retracted}
			out := FetchOut{SourceID: in.SourceID, FullText: d.FullText, Retracted: d.Retracted, Text: d.Text}
			return nil, out, h.Record(ctx, role, runToken(req), "fetch", in.SourceID, body, d.Text)
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "search_datasets", Description: "Search GEO datasets."},
		func(ctx context.Context, req *mcp.CallToolRequest, in DatasetsIn) (*mcp.CallToolResult, HitsOut, error) {
			hits, err := h.GEO.Search(ctx, in.Query)
			if err != nil {
				return nil, HitsOut{}, err
			}
			return nil, HitsOut{Results: hits}, h.Record(ctx, role, runToken(req), "search", "", map[string]any{"database": "geo", "query": in.Query, "results": ids(hits)}, "")
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "open_dataset", Description: "Open a GEO accession and return its structure: platform and what each sample is."},
		func(ctx context.Context, req *mcp.CallToolRequest, in OpenIn) (*mcp.CallToolResult, OpenOut, error) {
			txt, err := h.GEO.Open(ctx, in.Accession)
			if err != nil {
				return nil, OpenOut{}, err
			}
			return nil, OpenOut{Accession: in.Accession, Structure: txt}, h.Record(ctx, role, runToken(req), "open_dataset", in.Accession, map[string]any{"accession": in.Accession}, txt)
		})
	return srv
}
