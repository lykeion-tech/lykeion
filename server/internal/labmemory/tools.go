// The MCP endpoint carries no person credential. Its only source of an asker's
// identity is the current, token-bound Aristotle run in PostgreSQL.
package labmemory

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/storage"
	"lykeion/server/internal/store"
	db "lykeion/server/pkg/db/generated"
)

type Searcher interface {
	Search(context.Context, uuid.UUID, string, int) ([]Ref, error)
}
type Tools struct {
	Store *store.Store
	Index Searcher
	Blobs storage.Storage
}

type refInput struct {
	Directory string `json:"directory,omitempty" jsonschema:"For workspace:context, studies (default), proposals or lessons."`
	Limit     int    `json:"limit,omitempty" jsonschema:"Page size, 1 to 20; defaults to 20."`
	After     string `json:"after,omitempty" jsonschema:"Pass next_after from the previous page."`
	Ref       string `json:"ref"`
}
type readInput struct {
	Ref     string `json:"ref"`
	Section string `json:"section,omitempty" jsonschema:"Blob section to read: body (default), error, or packages."`
	Offset  int64  `json:"offset,omitempty" jsonschema:"Byte offset for the next page of a record's full text. Use next_offset from the previous read."`
}
type searchInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}
type hit struct {
	Ref   string `json:"ref"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
}
type hitList struct {
	NextAfter string `json:"next_after,omitempty"`
	Results   []hit  `json:"results"`
}
type evidence struct {
	historyPin string
	shown      []string
	Ref        string    `json:"ref"`
	Title      string    `json:"title"`
	Text       string    `json:"text"`
	Version    string    `json:"version"`
	Links      []string  `json:"links"`
	Superseded bool      `json:"superseded"`
	Sections   []string  `json:"sections,omitempty"`
	Blob       *blobPage `json:"blob,omitempty"`
}

var errMember = errors.New("the asker is no longer a member of this lab")
var errSearch = errors.New("search is unavailable; read and follow links instead")
var errQuery = errors.New("provide a query and a limit between 1 and 20")
var errOffset = errors.New("invalid blob offset")

func toolError(err error) error {
	if err == nil {
		return nil
	}
	for _, safe := range []error{ErrNotFound, middleware.ErrRoleUnauthorized, errMember, errSearch, errQuery, errOffset, errPage} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	slog.Error("lab tool failed", "error", err)
	return errors.New("the lab record could not be read; please retry")
}
func (t Tools) Handler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		return t.server(middleware.RoleFrom(r.Context()), token)
	}, &mcp.StreamableHTTPOptions{Stateless: true})
}

type toolCall struct {
	Tools
	role  middleware.Role
	token string
}

func (c toolCall) authorized(ctx context.Context, work func(*store.Tx, uuid.UUID) error) error {
	role := c.role
	var asker uuid.UUID
	// Only identity, never evidence, is obtained as the run. Recheck this binding
	// after locking the run and token in the asker's transaction.
	lookup := func(tx *store.Tx) (uuid.UUID, error) {
		requester, err := tx.Q.LabRunRequester(ctx, db.LabRunRequesterParams{WorkspaceID: role.WorkspaceID, ID: role.AgentRunID})
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, middleware.ErrRoleUnauthorized
		}
		if err != nil {
			return uuid.Nil, err
		}
		if requester == nil {
			return uuid.Nil, middleware.ErrRoleUnauthorized
		}
		return *requester, nil
	}
	err := c.Store.InWorkspace(ctx, role.WorkspaceID, role.AgentRunID, func(tx *store.Tx) error { var e error; asker, e = lookup(tx); return e })
	if err != nil {
		return err
	}
	return c.Store.InWorkspace(ctx, role.WorkspaceID, asker, func(tx *store.Tx) error {
		if err := middleware.LockRole(ctx, tx, role, c.token); err != nil {
			return err
		}
		token, err := tx.Q.LockAgentRunToken(ctx, db.LockAgentRunTokenParams{WorkspaceID: role.WorkspaceID, AgentRunID: role.AgentRunID, TokenHash: auth.Hash(c.token)})
		if err != nil {
			return middleware.ErrRoleUnauthorized
		}
		// The route admitted the original scopes; none may have been revoked since.
		for _, scope := range role.Scopes {
			if !slices.Contains(token.Scopes, scope) {
				return middleware.ErrRoleUnauthorized
			}
		}
		current, err := lookup(tx)
		if err != nil {
			return err
		}
		if current != asker {
			return middleware.ErrRoleUnauthorized
		}
		_, err = tx.Q.LockMember(ctx, db.LockMemberParams{WorkspaceID: tx.WS, UserID: asker})
		if errors.Is(err, pgx.ErrNoRows) {
			return errMember
		}
		if err != nil {
			return err
		}
		return work(tx, asker)
	})
}
func (c toolCall) log(ctx context.Context, tx *store.Tx, ref Ref, via, version string, shown []string) error {
	return tx.Q.AppendLabRead(ctx, db.AppendLabReadParams{WorkspaceID: tx.WS, AgentRunID: c.role.AgentRunID, Ref: ref.String(), Via: via, ReadVersion: version, Shown: shown})
}
func (c toolCall) hits(ctx context.Context, tx *store.Tx, asker uuid.UUID, refs []Ref, via string, limit int) (hitList, error) {
	out := hitList{Results: []hit{}}
	seen := map[Ref]bool{}
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		f, err := Read(ctx, tx, asker, ref)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return hitList{}, err
		}
		if err := c.log(ctx, tx, ref, via, pin(f).Version, titleOnly(ref)); err != nil {
			return hitList{}, err
		}
		out.Results = append(out.Results, hit{ref.String(), ref.Kind, f.Title})
		if limit > 0 && len(out.Results) == limit {
			break
		}
	}
	return out, nil
}

func (t Tools) server(role middleware.Role, token string) *mcp.Server {
	c := toolCall{t, role, token}
	srv := mcp.NewServer(&mcp.Implementation{Name: "lyk_lab", Version: "1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "search", Description: "Find lab records and lessons by meaning. Open results with read to see the evidence."}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, hitList, error) {
		var result hitList
		err := c.authorized(ctx, func(_ *store.Tx, _ uuid.UUID) error {
			if strings.TrimSpace(in.Query) == "" || len(in.Query) > 8192 || in.Limit < 0 || in.Limit > 20 {
				return errQuery
			}
			if c.Index == nil {
				return errSearch
			}
			return nil
		})
		if err != nil {
			return nil, result, toolError(err)
		}
		if in.Limit == 0 {
			in.Limit = 10
		}
		// No database locks are held while the index calls the embedding service.
		refs, err := c.Index.Search(ctx, role.WorkspaceID, in.Query, in.Limit)
		if err != nil {
			return nil, result, errSearch
		}
		err = c.authorized(ctx, func(tx *store.Tx, asker uuid.UUID) error {
			var e error
			private, err := tx.Q.SearchPrivateLabDecisions(ctx, db.SearchPrivateLabDecisionsParams{Query: in.Query, PageSize: int32(in.Limit)})
			if err != nil {
				return err
			}
			proposals, err := tx.Q.SearchLabProposals(ctx, db.SearchLabProposalsParams{Query: in.Query, PageSize: int32(in.Limit)})
			if err != nil {
				return err
			}
			refs = slices.Concat(kindRefs("decision", private), kindRefs("proposal", proposals), refs)
			seen := map[Ref]bool{}
			unique := make([]Ref, 0, len(refs))
			for _, ref := range refs {
				if !seen[ref] {
					unique = append(unique, ref)
					seen[ref] = true
				}
			}
			result, e = c.hits(ctx, tx, asker, unique, "search", in.Limit)
			return e
		})
		return nil, result, toolError(err)
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "links", Description: "Follow a lab record's links by page. Start from workspace:context with directory studies, proposals (saved to To-do, not started) or lessons when search is unavailable. Pass next_after as after until it is absent, even when a page has no visible hits."}, func(ctx context.Context, _ *mcp.CallToolRequest, in refInput) (*mcp.CallToolResult, hitList, error) {
		var result hitList
		err := c.authorized(ctx, func(tx *store.Tx, asker uuid.UUID) error {
			ref, e := ParseRef(in.Ref)
			if e != nil {
				return ErrNotFound
			}
			var version string
			refs, next, e := linkPage(ctx, tx, asker, ref, in, &version)
			if e != nil {
				return e
			}
			if e = c.log(ctx, tx, ref, "links", version, titleOnly(ref)); e != nil {
				return e
			}
			result, e = c.hits(ctx, tx, asker, refs, ref.String(), 0)
			result.NextAfter = next
			return e
		})
		return nil, result, toolError(err)
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "read", Description: "Read live lab evidence by ref (including workspace:context). Blob-backed records include pages of full text; follow next_offset until absent."}, func(ctx context.Context, _ *mcp.CallToolRequest, in readInput) (*mcp.CallToolResult, evidence, error) {
		result, err := c.read(ctx, in)
		return nil, result, toolError(err)
	})
	return srv
}
