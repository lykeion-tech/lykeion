package workspacemcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lykeion/server/internal/auth"
	"lykeion/server/internal/connectors"
	"lykeion/server/internal/middleware"
	"lykeion/server/internal/records"
	"lykeion/server/internal/service"
	"lykeion/server/internal/store"
	"lykeion/server/internal/toolroute"
	db "lykeion/server/pkg/db/generated"
)

var ErrDisabled = errors.New("workspace connector is disabled")

var ErrBroker = errors.New("workspace MCP request failed")
var ErrTool = errors.New("tool is not in this connector's current inventory")

// ErrReserved is a call naming a server whose records only Lykeion writes.
var ErrReserved = errors.New("only Lykeion records a study's attached files")

type authorityKey struct{}

// WithRunAuthority binds request-local transport credentials to broker fences.
func WithRunAuthority(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, authorityKey{}, token)
}

type sessionKey struct {
	Workspace, Run, Connector uuid.UUID
	Revision                  int64
	Attempt                   int32
}
type brokerRow struct {
	service.WorkspaceConnectorRow
	generation uint64
}

type brokerSession struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	session *mcp.ClientSession
	closed  bool
	tools   []toolroute.Tool
}

func (s *brokerSession) close() {
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.session != nil {
		s.session.Close()
		s.session = nil
	}
}

// Broker owns remote credentials and isolates each upstream session to one run.
type Broker struct {
	Store       *store.Store
	Keys        *connectors.Client
	mu          sync.Mutex
	sessions    map[sessionKey]*brokerSession
	closed      bool
	generation  uint64
	invalidated map[sessionKey]uint64
}

func (b *Broker) closeWhere(match func(sessionKey) bool) { b.invalidate(sessionKey{}, match) }
func (b *Broker) invalidate(marker sessionKey, match func(sessionKey) bool) {
	b.mu.Lock()
	b.generation++
	if b.invalidated == nil {
		b.invalidated = map[sessionKey]uint64{}
	}
	if marker != (sessionKey{}) {
		b.invalidated[marker] = b.generation
	}
	var old []*brokerSession
	for k, s := range b.sessions {
		if match(k) {
			b.invalidated[k] = b.generation
			delete(b.sessions, k)
			old = append(old, s)
		}
	}
	b.mu.Unlock()
	for _, s := range old {
		s.close()
	}
}
func (b *Broker) CloseConnector(ws, id uuid.UUID) {
	b.invalidate(sessionKey{Workspace: ws, Connector: id}, func(k sessionKey) bool { return k.Workspace == ws && k.Connector == id })
}
func (b *Broker) CloseRun(ws, id uuid.UUID) {
	b.invalidate(sessionKey{Workspace: ws, Run: id}, func(k sessionKey) bool { return k.Workspace == ws && k.Run == id })
}
func (b *Broker) CloseAttempt(ws, id uuid.UUID, attempt int32) {
	b.invalidate(sessionKey{Workspace: ws, Run: id, Attempt: attempt}, func(k sessionKey) bool { return k.Workspace == ws && k.Run == id && k.Attempt == attempt })
}
func (b *Broker) Close() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	b.closeWhere(func(sessionKey) bool { return true })
}

func (b *Broker) rows(ctx context.Context, role middleware.Role, call bool) ([]brokerRow, error) {
	b.mu.Lock()
	generation := b.generation
	b.mu.Unlock()
	token, _ := ctx.Value(authorityKey{}).(string)
	var rows []service.WorkspaceConnectorRow
	err := b.Store.InWorkspace(ctx, role.WorkspaceID, role.AgentRunID, func(tx *store.Tx) error {
		if err := middleware.LockRole(ctx, tx, role, token); err != nil {
			return err
		}
		tok, err := tx.Q.LockAgentRunToken(ctx, db.LockAgentRunTokenParams{WorkspaceID: role.WorkspaceID, AgentRunID: role.AgentRunID, TokenHash: auth.Hash(token)})
		if err != nil {
			return middleware.ErrRoleUnauthorized
		}
		if call && !slices.Contains(tok.Scopes, service.ScopeCall) {
			return middleware.ErrRoleUnauthorized
		}
		if !call && !slices.Contains(tok.Scopes, service.ScopeRole) && !slices.Contains(tok.Scopes, service.ScopeReport) {
			return middleware.ErrRoleUnauthorized
		}
		run, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: role.WorkspaceID, ID: role.AgentRunID})
		if err != nil {
			return err
		}
		rows, err = service.WorkspaceConnectorRows(ctx, tx, run)
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make([]brokerRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, brokerRow{row, generation})
	}
	return out, nil
}
func (b *Broker) authorizedRow(ctx context.Context, role middleware.Role, id uuid.UUID, call bool) (brokerRow, error) {
	rows, err := b.rows(ctx, role, call)
	if err != nil {
		return brokerRow{}, err
	}
	for _, r := range rows {
		if r.ID == id {
			return r, nil
		}
	}
	return brokerRow{}, pgx.ErrNoRows
}
func (b *Broker) row(ctx context.Context, role middleware.Role, id uuid.UUID, call bool) (brokerRow, error) {
	row, err := b.authorizedRow(ctx, role, id, call)
	if err != nil {
		return row, err
	}
	if !row.PolicyEnabled {
		return brokerRow{}, ErrDisabled
	}
	return row, nil
}
func (b *Broker) completionFence(ctx context.Context, role middleware.Role, row brokerRow, call bool) error {
	current, err := b.authorizedRow(ctx, role, row.ID, call)
	if err != nil {
		return err
	}
	if current.Revision != row.Revision || current.DoorName != row.DoorName || current.Attempt != row.Attempt {
		return ErrBroker
	}
	return nil
}
func (b *Broker) fence(ctx context.Context, role middleware.Role, row brokerRow, call bool) error {
	current, err := b.row(ctx, role, row.ID, call)
	if err != nil {
		return err
	}
	if current.Revision != row.Revision || current.DoorName != row.DoorName || current.Attempt != row.Attempt {
		return ErrBroker
	}
	return nil
}
func (b *Broker) entry(role middleware.Role, row brokerRow) (*brokerSession, error) {
	key := sessionKey{role.WorkspaceID, role.AgentRunID, row.ID, row.Revision, row.Attempt}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, ErrBroker
	}
	for _, marker := range []sessionKey{key, {Workspace: key.Workspace, Connector: key.Connector}, {Workspace: key.Workspace, Run: key.Run}, {Workspace: key.Workspace, Run: key.Run, Attempt: key.Attempt}} {
		if b.invalidated[marker] > row.generation {
			b.mu.Unlock()
			return nil, ErrBroker
		}
	}
	var old []*brokerSession
	for k, session := range b.sessions {
		if k.Workspace != key.Workspace || k.Run != key.Run || k.Connector != key.Connector || k == key {
			continue
		}
		// An in-flight request can arrive with a snapshot read before a newer
		// attempt or configuration. It must never replace that newer session.
		if k.Attempt > key.Attempt || (k.Attempt == key.Attempt && k.Revision > key.Revision) {
			b.mu.Unlock()
			return nil, ErrBroker
		}
		old = append(old, session)
		delete(b.sessions, k)
	}
	if b.sessions == nil {
		b.sessions = map[sessionKey]*brokerSession{}
	}
	s := b.sessions[key]
	if s == nil {
		ctx, cancel := context.WithCancel(context.Background())
		s = &brokerSession{ctx: ctx, cancel: cancel}
		b.sessions[key] = s
	}
	b.mu.Unlock()
	for _, session := range old {
		session.close()
	}
	return s, nil
}
func (b *Broker) drop(s *brokerSession) {
	b.closeWhere(func(k sessionKey) bool { return b.sessions[k] == s })
}

// configuredHeaders adds credentials only to the validated endpoint's requests.
type configuredHeaders struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t configuredHeaders) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header = r.Header.Clone()
	for k, v := range t.headers {
		r.Header.Set(k, v)
	}
	return t.base.RoundTrip(r)
}
func (b *Broker) config(row brokerRow) (Config, error) {
	if b.Keys == nil {
		return Config{}, ErrBroker
	}
	raw, err := b.Keys.Open(row.WorkspaceID, resource(row.ID), row.ConfigCiphertext)
	var c Config
	if err != nil || json.Unmarshal([]byte(raw), &c) != nil {
		return Config{}, ErrBroker
	}
	return c, nil
}
func (b *Broker) connect(ctx context.Context, s *brokerSession, c Config) error {
	if s.session != nil {
		return nil
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ErrBroker
	}
	hc := connectors.Guarded()
	if b.Keys.HTTP != nil {
		copy := *b.Keys.HTTP
		hc = &copy
	}
	base := hc.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	hc.Transport = configuredHeaders{base, c.Headers}
	hc.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if next.URL.Scheme != "https" || next.URL.Host != u.Host || len(via) >= 10 {
			return ErrBroker
		}
		return nil
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "lykeion_workspace", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: c.URL, HTTPClient: hc, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return ErrBroker
	}
	s.session = session
	return nil
}
func listTools(ctx context.Context, s *mcp.ClientSession) ([]toolroute.Tool, error) {
	tools := []toolroute.Tool{}
	for t, err := range s.Tools(ctx, nil) {
		if err != nil {
			return nil, ErrBroker
		}
		tools = append(tools, toolroute.Tool{Name: t.Name, Description: t.Description})
	}
	return tools, nil
}

// scrub removes saved credential material even if an upstream echoes it.
func scrub[T any](value T, c Config) (T, error) {
	var zero T
	raw, err := json.Marshal(value)
	if err != nil {
		return zero, ErrBroker
	}
	secrets := []string{c.URL}
	if u, err := url.Parse(c.URL); err == nil {
		if u.User != nil {
			secrets = append(secrets, u.User.Username())
			if p, ok := u.User.Password(); ok {
				secrets = append(secrets, p)
			}
		}
		for _, vs := range u.Query() {
			secrets = append(secrets, vs...)
		}
	}
	for _, v := range c.Headers {
		secrets = append(secrets, v)
		if _, tail, ok := strings.Cut(v, " "); ok {
			secrets = append(secrets, tail)
		}
	}
	text := string(raw)
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		for _, v := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret)} {
			encoded, _ := json.Marshal(v)
			text = strings.ReplaceAll(text, string(encoded[1:len(encoded)-1]), "[credential removed]")
		}
	}
	if json.Unmarshal([]byte(text), &zero) != nil {
		return zero, ErrBroker
	}
	return zero, nil
}

func (b *Broker) Tools(ctx context.Context, run middleware.Role, id uuid.UUID) ([]toolroute.Tool, error) {
	return b.tools(ctx, run, id, true)
}
func (b *Broker) tools(ctx context.Context, run middleware.Role, id uuid.UUID, call bool) ([]toolroute.Tool, error) {
	row, err := b.row(ctx, run, id, call)
	if err != nil {
		return nil, err
	}
	s, err := b.entry(run, row)
	if err != nil {
		return nil, err
	}
	if row.Transport == "command" {
		s.mu.Lock()
		out := slices.Clone(s.tools)
		s.mu.Unlock()
		if err := b.fence(ctx, run, row, call); err != nil {
			if !errors.Is(err, ErrDisabled) {
				b.drop(s)
			}
			return nil, err
		}
		return out, nil
	}
	c, err := b.config(row)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { b.drop(s) })
	defer stop()
	discard := false
	defer func() {
		if discard {
			b.drop(s)
		}
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrBroker
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopSession := context.AfterFunc(s.ctx, cancel)
	defer stopSession()
	if err := b.connect(ctx, s, c); err != nil {
		return nil, err
	}
	out, err := listTools(ctx, s.session)
	if err != nil {
		return nil, err
	}
	if err := b.fence(ctx, run, row, call); err != nil {
		discard = true
		return nil, err
	}
	return scrub(out, c)
}
func (b *Broker) Call(ctx context.Context, run middleware.Role, id uuid.UUID, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	row, err := b.row(ctx, run, id, true)
	if err != nil {
		return nil, err
	}
	if row.Transport != "url" {
		return nil, ErrBroker
	}
	c, err := b.config(row)
	if err != nil {
		return nil, err
	}
	s, err := b.entry(run, row)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { b.drop(s) })
	defer stop()
	discard := false
	defer func() {
		if discard {
			b.drop(s)
		}
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrBroker
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopSession := context.AfterFunc(s.ctx, cancel)
	defer stopSession()
	if err := b.connect(ctx, s, c); err != nil {
		return nil, err
	}
	tools, err := listTools(ctx, s.session)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(tools, func(t toolroute.Tool) bool { return t.Name == tool }) {
		return nil, ErrTool
	}
	if err := b.fence(ctx, run, row, true); err != nil {
		discard = true
		return nil, err
	}
	result, err := s.session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return nil, ErrBroker
	}
	if err := b.completionFence(ctx, run, row, true); err != nil {
		discard = true
		return nil, err
	}
	cleaned, err := scrub(result, c)
	if err != nil {
		return nil, err
	}
	if capture, _ := ctx.Value(receiptKey{}).(*RecordCapture); capture != nil {
		capture.Ticket, err = b.sealReceipt(run, row, tool, args, AnswerHash(AnswerText(cleaned)))
		if err != nil {
			return nil, err
		}
	}
	return cleaned, nil
}

// PublishTools accepts inventory only on the private daemon transport. The next
// read fences the run and revision again; agent-supplied lists never enter it.
func (b *Broker) PublishTools(ctx context.Context, run middleware.Role, id uuid.UUID, tools []toolroute.Tool, fingerprint string) error {
	row, err := b.row(ctx, run, id, true)
	if err != nil {
		return err
	}
	if row.Transport != "command" {
		return ErrBroker
	}
	config, err := b.config(row)
	if err != nil {
		return err
	}
	if fingerprint != service.CommandFingerprint(append([]string{config.Command}, config.Args...), config.Env) {
		return ErrBroker
	}
	s, err := b.entry(run, row)
	if err != nil {
		return err
	}
	discard := false
	defer func() {
		if discard {
			b.drop(s)
		}
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrBroker
	}
	if err := b.fence(ctx, run, row, true); err != nil {
		discard = !errors.Is(err, ErrDisabled)
		return err
	}
	s.tools = slices.Clone(tools)
	return nil
}

// Routing returns current workspace connectors with inventories from their
// authorized sources, ignoring the agent's proposed tool names.
func (b *Broker) Routing(ctx context.Context, run middleware.Role) ([]service.WorkspaceConnectorClaim, map[string][]toolroute.Tool, func(*store.Tx) error, error) {
	rows, err := b.rows(ctx, run, false)
	if err != nil {
		return nil, nil, nil, err
	}
	claims := []service.WorkspaceConnectorClaim{}
	tools := map[string][]toolroute.Tool{}
	for _, row := range rows {
		if !row.PolicyEnabled {
			continue
		}
		ts, err := b.tools(ctx, run, row.ID, false)
		if err != nil {
			return nil, nil, nil, err
		}
		tools[row.DoorName] = ts
		claims = append(claims, service.WorkspaceConnectorClaim{ID: row.ID, Name: row.DoorName, Transport: row.Transport})
	}
	check := func(tx *store.Tx) error {
		currentRun, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: run.WorkspaceID, ID: run.AgentRunID})
		if err != nil {
			return err
		}
		current, err := service.WorkspaceConnectorRows(ctx, tx, currentRun)
		if err != nil {
			return err
		}
		current = slices.DeleteFunc(current, func(row service.WorkspaceConnectorRow) bool { return !row.PolicyEnabled })
		rows = slices.DeleteFunc(slices.Clone(rows), func(row brokerRow) bool { return !row.PolicyEnabled })
		if len(current) != len(rows) {
			return ErrBroker
		}
		for _, want := range rows {
			if !slices.ContainsFunc(current, func(have service.WorkspaceConnectorRow) bool {
				return have.ID == want.ID && have.Revision == want.Revision && have.DoorName == want.DoorName && have.Attempt == want.Attempt
			}) {
				return ErrBroker
			}
		}
		return nil
	}
	return claims, tools, check, nil
}

// CheckCommand fences process launch as well as each later tool execution.
func (b *Broker) CheckCommand(ctx context.Context, run middleware.Role, id uuid.UUID, fingerprint string) error {
	row, err := b.row(ctx, run, id, true)
	if err != nil {
		return err
	}
	if row.Transport != "command" {
		return ErrBroker
	}
	config, err := b.config(row)
	if err != nil {
		return err
	}
	if fingerprint != service.CommandFingerprint(append([]string{config.Command}, config.Args...), config.Env) {
		return ErrBroker
	}
	return b.fence(ctx, run, row, true)
}

// RecordHeader carries a private execution receipt between server and daemon.
const RecordHeader = "X-Lykeion-Workspace-Call"

type receiptKey struct{}
type RecordCapture struct{ Ticket string }

func WithRecordCapture(ctx context.Context) (context.Context, *RecordCapture) {
	capture := &RecordCapture{}
	return context.WithValue(ctx, receiptKey{}, capture), capture
}

type CommandRecord struct {
	Tool         string         `json:"tool"`
	Args         map[string]any `json:"args"`
	AnswerSHA256 string         `json:"answer_sha256"`
}
type callReceipt struct {
	Workspace, Run, Connector        uuid.UUID
	Attempt                          int32
	Revision                         int64
	Name, Tool, ArgsHash, AnswerHash string
}

func AnswerHash(answer string) string {
	sum := sha256.Sum256([]byte(answer))
	return hex.EncodeToString(sum[:])
}
func argsHash(args any) (string, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return "", ErrBroker
	}
	var canonical any
	if json.Unmarshal(raw, &canonical) != nil {
		return "", ErrBroker
	}
	if canonical == nil {
		canonical = map[string]any{}
	}
	raw, err = json.Marshal(canonical)
	if err != nil {
		return "", ErrBroker
	}
	return AnswerHash(string(raw)), nil
}

// AnswerText is the exact representation recorded by the daemon's Door.
func AnswerText(result *mcp.CallToolResult) string {
	var out strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			out.WriteString(text.Text)
		}
	}
	if out.Len() == 0 && result.StructuredContent != nil {
		raw, _ := json.Marshal(result.StructuredContent)
		return string(raw)
	}
	return out.String()
}
func (b *Broker) sealReceipt(run middleware.Role, row brokerRow, tool string, args any, answerHash string) (string, error) {
	return b.sealCall(run, row, tool, args, answerHash, "mcp-call-receipt")
}
func (b *Broker) sealCall(run middleware.Role, row brokerRow, tool string, args any, answerHash, purpose string) (string, error) {
	hash, err := argsHash(args)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(callReceipt{run.WorkspaceID, run.AgentRunID, row.ID, row.Attempt, row.Revision, row.DoorName, tool, hash, answerHash})
	if err != nil {
		return "", ErrBroker
	}
	sealed, err := b.Keys.Seal(run.WorkspaceID, purpose, string(raw))
	if err != nil {
		return "", ErrBroker
	}
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// BeginCommand binds authorization to the exact call before local execution.
func (b *Broker) BeginCommand(ctx context.Context, run middleware.Role, id uuid.UUID, fingerprint string, record CommandRecord) (string, error) {
	if err := b.CheckCommand(ctx, run, id, fingerprint); err != nil {
		return "", err
	}
	row, err := b.row(ctx, run, id, true)
	if err != nil {
		return "", err
	}
	s, err := b.entry(run, row)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	listed := slices.ContainsFunc(s.tools, func(t toolroute.Tool) bool { return t.Name == record.Tool })
	s.mu.Unlock()
	if !listed {
		return "", ErrTool
	}
	if err := b.fence(ctx, run, row, true); err != nil {
		return "", err
	}
	return b.sealCall(run, row, record.Tool, record.Args, "", "mcp-command-start")
}

// CommandReceipt follows the daemon's final actual-inventory handoff.
func (b *Broker) CommandReceipt(ctx context.Context, run middleware.Role, id uuid.UUID, fingerprint string, record CommandRecord, start string) (string, error) {
	row, err := b.authorizedRow(ctx, run, id, true)
	if err != nil {
		return "", err
	}
	if row.Transport != "command" || record.Tool == "" {
		return "", ErrBroker
	}
	sealed, err := base64.RawURLEncoding.DecodeString(start)
	if err != nil || b.Keys == nil {
		return "", ErrBroker
	}
	raw, err := b.Keys.Open(run.WorkspaceID, "mcp-command-start", sealed)
	var begun callReceipt
	if err != nil || json.Unmarshal([]byte(raw), &begun) != nil {
		return "", ErrBroker
	}
	args, err := argsHash(record.Args)
	if err != nil || begun.Workspace != run.WorkspaceID || begun.Run != run.AgentRunID || begun.Connector != row.ID || begun.Attempt != row.Attempt || begun.Revision != row.Revision || begun.Name != row.DoorName || begun.Tool != record.Tool || begun.ArgsHash != args {
		return "", ErrBroker
	}

	hash, err := hex.DecodeString(record.AnswerSHA256)
	if err != nil || len(hash) != sha256.Size {
		return "", ErrBroker
	}
	config, err := b.config(row)
	if err != nil {
		return "", err
	}
	if fingerprint != service.CommandFingerprint(append([]string{config.Command}, config.Args...), config.Env) {
		return "", ErrBroker
	}
	s, err := b.entry(run, row)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	listed := slices.ContainsFunc(s.tools, func(t toolroute.Tool) bool { return t.Name == record.Tool })
	s.mu.Unlock()
	if !listed {
		return "", ErrTool
	}
	if err := b.completionFence(ctx, run, row, true); err != nil {
		b.drop(s)
		return "", err
	}
	return b.sealReceipt(run, row, record.Tool, record.Args, record.AnswerSHA256)
}

// RecordFence validates the execution receipt and returns the check that must
// run after LockRole in the same transaction as the evidence write.
func (b *Broker) RecordFence(ctx context.Context, run middleware.Role, ticket, server, tool string, args any, answer string) (func(*store.Tx) error, error) {
	if server == records.AttachmentServer {
		return nil, ErrReserved
	}
	var receipt callReceipt
	if ticket != "" {
		sealed, err := base64.RawURLEncoding.DecodeString(ticket)
		if err != nil || b.Keys == nil {
			return nil, ErrBroker
		}
		raw, err := b.Keys.Open(run.WorkspaceID, "mcp-call-receipt", sealed)
		if err != nil || json.Unmarshal([]byte(raw), &receipt) != nil {
			return nil, ErrBroker
		}
		hash, err := argsHash(args)
		if err != nil || receipt.Workspace != run.WorkspaceID || receipt.Run != run.AgentRunID || receipt.Name != server || receipt.Tool != tool || receipt.ArgsHash != hash || receipt.AnswerHash != AnswerHash(answer) {
			return nil, ErrBroker
		}
	}
	return func(tx *store.Tx) error {
		current, err := tx.Q.GetAgentRun(ctx, db.GetAgentRunParams{WorkspaceID: run.WorkspaceID, ID: run.AgentRunID})
		if err != nil {
			return err
		}
		rows, err := service.WorkspaceConnectorRows(ctx, tx, current)
		if err != nil {
			return err
		}
		if ticket == "" {
			if slices.ContainsFunc(rows, func(row service.WorkspaceConnectorRow) bool { return row.DoorName == server }) {
				return ErrBroker
			}
			return nil
		}
		if current.Attempt != receipt.Attempt {
			return ErrBroker
		}
		if !slices.ContainsFunc(rows, func(row service.WorkspaceConnectorRow) bool {
			return row.ID == receipt.Connector && row.Revision == receipt.Revision && row.DoorName == receipt.Name
		}) {
			return ErrBroker
		}
		return nil
	}, nil
}
