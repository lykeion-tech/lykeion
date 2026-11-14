package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type Model struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

const MaxModels = 256

// ValidModels bounds a CLI-reported catalog before it is stored or transported.
func ValidModels(models []Model) bool {
	if len(models) > MaxModels {
		return false
	}
	seen := map[string]bool{}
	for _, m := range models {
		if m.Value == "" || m.Label == "" || len(m.Value) > 256 || len(m.Label) > 256 || len(m.Description) > 2048 || strings.ContainsRune(m.Value+m.Label+m.Description, 0) || seen[m.Value] {
			return false
		}
		seen[m.Value] = true
	}
	b, e := json.Marshal(models)
	return e == nil && len(b) <= 60000
}

// ListModels asks only the installed CLI. Unknown shapes return no choices.
// Discovery never starts a turn or changes the model saved in a conversation.
func ListModels(ctx context.Context, harness, bin string, sandbox SandboxCommand) ([]Model, error) {
	empty := []Model{}
	if sandbox == nil {
		return empty, ErrNoSandbox
	}
	if harness != "claude" && harness != "codex" {
		return empty, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	dir, e := os.MkdirTemp("", "lyk-models-")
	if e != nil {
		return empty, e
	}
	defer os.RemoveAll(dir)
	opts := ExecOptions{Dir: dir, Sandbox: sandbox, Coresearch: true}
	// app-server does not support exec's ignore-user-config switch. Its catalog
	// is available in a fresh auth-free home: no copied/rotating credentials,
	// personal config, plugin startup, or account-specific eligibility claim.
	if harness == "codex" {
		opts.ConfigHome = filepath.Join(dir, ".catalog-home")
		opts.Guest = true
	}
	_, cleanup, e := prepareScratch(&opts, harness)
	if e != nil {
		return empty, e
	}
	defer cleanup(true)
	opts.CLIPaths.Scratch = dir
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--permission-mode", "dontAsk", "--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--setting-sources", "", "--disable-slash-commands", "--no-session-persistence"}
	if harness == "codex" {
		args = []string{"app-server"}
		opts.CLIPaths.Keychain = false
	}
	if bin == "" {
		bin = harness
	}
	cmd, e := command(ctx, bin, "", opts, args...)
	if e != nil {
		return empty, e
	}
	reader, writer, e := os.Pipe()
	if e != nil {
		return empty, e
	}
	defer writer.Close()
	cmd.Stdin = reader
	send := func(v any) error {
		b, e := json.Marshal(v)
		if e == nil {
			_, e = writer.Write(append(b, '\n'))
		}
		return e
	}
	total, pages, id := 0, 0, 1
	seenCursor := map[string]bool{}
	models := empty
	defaults := map[string]bool{}
	parse := func(line []byte, _ emitMessage, _ *Result) (bool, error) {
		total += len(line)
		if len(line) > 256<<10 || total > 1<<20 {
			return false, errors.New("model response exceeds limit")
		}
		var frame struct {
			Type     string          `json:"type"`
			ID       int             `json:"id"`
			Result   json.RawMessage `json:"result"`
			Error    json.RawMessage `json:"error"`
			Response struct {
				Subtype   string          `json:"subtype"`
				RequestID string          `json:"request_id"`
				Response  json.RawMessage `json:"response"`
			} `json:"response"`
		}
		if json.Unmarshal(line, &frame) != nil {
			models = empty
			return true, errStop
		}
		if harness == "claude" {
			if frame.Type != "control_response" || frame.Response.RequestID != "models-init" {
				return false, nil
			}
			var payload struct {
				Models []struct {
					Value       string `json:"value"`
					DisplayName string `json:"displayName"`
					Description string `json:"description"`
				} `json:"models"`
			}
			if frame.Response.Subtype != "success" || json.Unmarshal(frame.Response.Response, &payload) != nil {
				return true, errStop
			}
			for _, m := range payload.Models {
				models = append(models, Model{m.Value, m.DisplayName, m.Description})
			}
			return true, errStop
		}
		if frame.ID != id {
			return false, nil
		}
		if len(frame.Error) > 0 && string(frame.Error) != "null" {
			models = empty
			return true, errStop
		}
		if id == 1 {
			if len(frame.Result) == 0 {
				return true, errStop
			}
			if e := send(map[string]any{"method": "initialized"}); e != nil {
				return false, e
			}
			id = 2
			return false, send(map[string]any{"id": id, "method": "model/list", "params": map[string]any{"limit": 100}})
		}
		var payload struct {
			Data []struct {
				Model       string `json:"model"`
				DisplayName string `json:"displayName"`
				Description string `json:"description"`
				Hidden      bool   `json:"hidden"`
				Default     bool   `json:"isDefault"`
			} `json:"data"`
			Next *string `json:"nextCursor"`
		}
		if json.Unmarshal(frame.Result, &payload) != nil || payload.Data == nil {
			models = empty
			return true, errStop
		}
		for _, m := range payload.Data {
			if !m.Hidden {
				models = append(models, Model{m.Model, m.DisplayName, m.Description})
				defaults[m.Model] = m.Default
			}
		}
		if !ValidModels(models) {
			models = empty
			return true, errStop
		}
		pages++
		if payload.Next == nil || *payload.Next == "" {
			slices.SortStableFunc(models, func(a, b Model) int {
				if defaults[a.Value] == defaults[b.Value] {
					return 0
				}
				if defaults[a.Value] {
					return -1
				}
				return 1
			})
			return true, errStop
		}
		if pages >= 16 || len(*payload.Next) > 4096 || seenCursor[*payload.Next] {
			models = empty
			return true, errStop
		}
		seenCursor[*payload.Next] = true
		id++
		return false, send(map[string]any{"id": id, "method": "model/list", "params": map[string]any{"limit": 100, "cursor": *payload.Next}})
	}
	session, e := run(ctx, cmd, cleanup, parse, nil, func(Message) error { return nil })
	reader.Close()
	if e != nil {
		return empty, e
	}
	if harness == "claude" {
		e = send(map[string]any{"type": "control_request", "request_id": "models-init", "request": map[string]string{"subtype": "initialize"}})
	} else {
		e = send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]string{"name": "lykeion", "version": "1"}}})
	}
	if e != nil {
		cancel()
	}
	result := <-session.Done
	if result.Err != nil {
		return empty, result.Err
	}
	if e != nil {
		return empty, e
	}
	if !ValidModels(models) {
		return empty, nil
	}
	return models, nil
}
