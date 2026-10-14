package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type Claude struct{ Bin string }

const claudeTools = "Read,Glob,Grep,Bash(lyk:*),mcp__lyk_sources"
const claudeCredFile = ".credentials.json"

func (c Claude) Execute(ctx context.Context, prompt string, o ExecOptions) (*Session, error) {
	home, cleanup, err := prepareScratch(&o, "claude", "CLAUDE_CONFIG_DIR", ".claude", claudeCredFile)
	if err != nil {
		return nil, err
	}
	accounting, err := beginClaudeUsage(home, o.Resume)
	if err != nil {
		cleanup(true)
		return nil, err
	}
	cfg := filepath.Join(o.Dir, ".lyk-mcp.json")
	// Replace only the adapter's prior file; never follow a planted link.
	if st, e := os.Lstat(cfg); e == nil {
		if !st.Mode().IsRegular() {
			cleanup(true)
			return nil, fmt.Errorf("MCP config is not a regular file: %s", cfg)
		}
		if err = os.Remove(cfg); err != nil {
			cleanup(true)
			return nil, err
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		cleanup(true)
		return nil, e
	}
	if err = privateFile(cfg, claudeMCP(o)); err != nil {
		cleanup(true)
		return nil, err
	}
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "dontAsk", "--allowedTools", claudeTools, "--mcp-config", cfg, "--strict-mcp-config", "--setting-sources", "user"}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.Resume != "" {
		args = append(args, "--resume", o.Resume)
	}
	bin := c.Bin
	if bin == "" {
		bin = "claude"
	}
	return run(ctx, command(ctx, bin, prompt, o, home, args...), cleanup, parseClaude(o.Model, accounting), accounting.finish, o.OnMessage)
}

// Later duplicate JSON keys win. Sources is last so server-sent add-ons cannot
// replace its endpoint or authorization header.
func claudeMCP(o ExecOptions) []byte {
	var b bytes.Buffer
	b.WriteString(`{"mcpServers":{`)
	first := true
	add := func(name string, v any) {
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.Write(raw(name))
		b.WriteByte(':')
		b.Write(raw(v))
	}
	for _, s := range o.MCP {
		add(s.Name, map[string]string{"type": "http", "url": s.URL})
	}
	if o.MCPURL != "" {
		add("lyk_sources", map[string]any{"type": "http", "url": o.MCPURL, "headers": map[string]string{"Authorization": "Bearer " + o.MCPToken}})
	}
	b.WriteString("}}")
	return b.Bytes()
}

type claudeTokens struct {
	Input      int64    `json:"inputTokens"`
	Output     int64    `json:"outputTokens"`
	CacheRead  int64    `json:"cacheReadInputTokens"`
	CacheWrite int64    `json:"cacheCreationInputTokens"`
	Cost       *float64 `json:"costUSD"`
}

func parseClaude(fallbackModel string, accounting *claudeAccounting) parser {
	return func(line []byte, emit emitMessage, res *Result) (bool, error) {
		var ev struct {
			Type           string   `json:"type"`
			Session        string   `json:"session_id"`
			Model          string   `json:"model"`
			IsError        bool     `json:"is_error"`
			Cost           *float64 `json:"total_cost_usd"`
			Result         string   `json:"result"`
			Subtype        string   `json:"subtype"`
			Errors         []string `json:"errors"`
			TerminalReason string   `json:"terminal_reason"`
			Usage          *struct {
				Input      int64 `json:"input_tokens"`
				Output     int64 `json:"output_tokens"`
				CacheRead  int64 `json:"cache_read_input_tokens"`
				CacheWrite int64 `json:"cache_creation_input_tokens"`
			} `json:"usage"`
			ModelUsage map[string]claudeTokens `json:"modelUsage"`
			Message    json.RawMessage         `json:"message"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			return false, fmt.Errorf("invalid Claude event: %w", err)
		}
		if ev.Session != "" {
			res.SessionID = ev.Session
		}
		switch ev.Type {
		case "assistant", "user":
			var message struct {
				Content []json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(ev.Message, &message); err != nil {
				return false, fmt.Errorf("invalid Claude message: %w", err)
			}
			for _, c := range message.Content {
				var item struct {
					Type, Name, Text, Thinking string
					Input                      json.RawMessage
				}
				if err := json.Unmarshal(c, &item); err != nil {
					return false, fmt.Errorf("invalid Claude content: %w", err)
				}
				switch item.Type {
				case "text":
					emit(Message{Type: "text", Content: raw(item.Text)})
				case "thinking":
					emit(Message{Type: "thinking", Content: raw(item.Thinking)})
				case "tool_use":
					emit(Message{Type: "tool_use", Tool: item.Name, Content: item.Input})
				case "tool_result":
					emit(Message{Type: "tool_result", Content: c})
				}
			}
		case "result":
			// Result usage is authoritative and already excludes Claude's caches from
			// input tokens. Repeated terminal snapshots replace rather than add totals.
			res.Usage = nil
			for _, model := range slices.Sorted(maps.Keys(ev.ModelUsage)) {
				u := ev.ModelUsage[model]
				if strings.TrimSpace(model) == "" {
					continue
				}
				res.Usage = append(res.Usage, Usage{Model: model, InputTokens: u.Input, OutputTokens: u.Output, CacheReadTokens: u.CacheRead, CacheWriteTokens: u.CacheWrite, CostUSD: u.Cost})
			}
			if len(res.Usage) == 0 && (ev.Cost != nil || ev.Usage != nil) {
				model := ev.Model
				if model == "" {
					model = fallbackModel
				}
				if model == "" {
					model = "claude-unattributed"
				}
				u := Usage{Model: model, CostUSD: ev.Cost}
				if ev.Usage != nil {
					u.InputTokens, u.OutputTokens = ev.Usage.Input, ev.Usage.Output
					u.CacheReadTokens, u.CacheWriteTokens = ev.Usage.CacheRead, ev.Usage.CacheWrite
				}
				res.Usage = []Usage{u}
			}
			if strings.TrimSpace(ev.TerminalReason) == "prompt_too_long" {
				res.Err = fmt.Errorf("Claude terminal_reason=prompt_too_long: context exhausted: %s", ev.Result)
				res.Reason = ReasonContextExhausted
			} else if ev.IsError || strings.HasPrefix(ev.Subtype, "error") {
				detail := strings.TrimSpace(strings.Join(append([]string{ev.Result}, ev.Errors...), "\n"))
				if detail == "" {
					detail = "Claude returned an error result without details"
				}
				res.Err = errors.New(detail)
			}
			if err := accounting.normalize(res, ev.ModelUsage); err != nil {
				res.Usage = nil
				// Accounting uncertainty must not erase the provider's terminal
				// failure or its retry classification. No ambiguous spend is sent.
				if res.Err != nil {
					res.Err = fmt.Errorf("%w; usage unavailable: %v", res.Err, err)
					return true, nil
				}
				return false, err
			}
			return true, nil
		default:
			emit(Message{Type: "log", Content: append(json.RawMessage(nil), line...)})
		}
		return false, nil
	}
}
