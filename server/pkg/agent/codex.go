package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"

	"lykeion/server/internal/pricing"
)

type Codex struct{ Bin string }

// Codex does not price events or report their model; pin it for server pricing.
const CodexModel = "gpt-5.6-sol"
const codexCredFile = "auth.json"

var plainKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func (c Codex) Execute(ctx context.Context, prompt string, o ExecOptions) (*Session, error) {
	model := o.Model
	if model == "" {
		model = CodexModel
	}
	// These flags belong to exec, before the optional resume subcommand.
	args := []string{"exec", "--json", "--skip-git-repo-check", "-s", "workspace-write", "-m", model, "--ignore-user-config", "--ignore-rules", "--disable", "apps", "--disable", "plugins", "--disable", "remote_plugin"}
	for _, s := range o.MCP {
		if !plainKey.MatchString(s.Name) {
			return nil, fmt.Errorf("MCP server name %q is not a plain key", s.Name)
		}
		args = append(args, "-c", fmt.Sprintf("mcp_servers.%s.url=%s", s.Name, raw(s.URL)))
	}
	if o.MCPURL != "" {
		args = append(args, "-c", fmt.Sprintf("mcp_servers.lyk_sources.url=%s", raw(o.MCPURL)), "-c", `mcp_servers.lyk_sources.bearer_token_env_var="LYK_TOKEN"`)
	}
	home, cleanup, err := prepareScratch(&o, "codex", "CODEX_HOME", ".codex", codexCredFile)
	if err != nil {
		return nil, err
	}
	args = append(args, "--cd", o.Dir)
	if o.Resume != "" {
		args = append(args, "resume", o.Resume)
	}
	args = append(args, "-")
	bin := c.Bin
	if bin == "" {
		bin = "codex"
	}
	return run(ctx, command(ctx, bin, prompt, o, home, args...), cleanup, parseCodex(model), nil, o.OnMessage)
}
func parseCodex(model string) parser {
	return func(line []byte, emit emitMessage, res *Result) (bool, error) {
		var ev struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Message  string `json:"message"`
			Error    struct {
				Message string `json:"message"`
			} `json:"error"`
			Item  struct{ Type, Text, Tool string } `json:"item"`
			Usage struct {
				Input      int64 `json:"input_tokens"`
				Cached     int64 `json:"cached_input_tokens"`
				CacheWrite int64 `json:"cache_write_input_tokens"`
				Output     int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			return false, fmt.Errorf("invalid Codex event: %w", err)
		}
		cp := append(json.RawMessage(nil), line...)
		switch ev.Type {
		case "thread.started":
			res.SessionID = ev.ThreadID
			emit(Message{Type: "log", Content: cp})
		case "item.completed":
			switch ev.Item.Type {
			case "agent_message":
				emit(Message{Type: "text", Content: raw(ev.Item.Text)})
			case "reasoning":
				emit(Message{Type: "thinking", Content: raw(ev.Item.Text)})
			case "command_execution":
				emit(Message{Type: "tool_use", Tool: "shell", Content: cp})
			case "mcp_tool_call":
				emit(Message{Type: "tool_use", Tool: ev.Item.Tool, Content: cp})
			default:
				emit(Message{Type: "log", Content: cp})
			}
		case "turn.completed":
			v := ev.Usage
			if v.Input < 0 || v.Output < 0 || v.Cached < 0 || v.CacheWrite < 0 || v.Cached > v.Input || v.CacheWrite > v.Input-v.Cached {
				return false, errors.New("invalid Codex usage: negative or inconsistent token counts")
			}
			ordinary := v.Input - v.Cached - v.CacheWrite
			var u Usage
			if len(res.Usage) > 0 {
				u = res.Usage[0]
			} else {
				u.Model = model
			}
			if ordinary > math.MaxInt64-u.InputTokens || v.Output > math.MaxInt64-u.OutputTokens || v.Cached > math.MaxInt64-u.CacheReadTokens || v.CacheWrite > math.MaxInt64-u.CacheWriteTokens {
				return false, errors.New("invalid Codex usage: aggregate token count overflow")
			}
			u.InputTokens += ordinary
			u.CacheReadTokens += v.Cached
			u.CacheWriteTokens += v.CacheWrite
			u.OutputTokens += v.Output
			res.Usage = []Usage{u}
			upper := pricing.ConservativeUpperBound(model, u.InputTokens, u.CacheReadTokens, u.CacheWriteTokens)
			message := "Standard API list-price estimate with no Fast tier requested; not ChatGPT billed spend."
			if upper {
				message += " Per-request pricing bands are unavailable; long-context rates applied to all aggregate tokens are a conservative upper bound."
			}
			emit(Message{Type: "status", Content: cp})
			emit(Message{Type: "status", Content: raw(map[string]any{"type": "accounting", "model": model, "policy": "api_list_price_estimate", "conservative_upper_bound": upper, "message": message})})
			return true, nil
		case "turn.failed":
			detail := ev.Error.Message
			if detail == "" {
				detail = "Codex turn failed without details"
			}
			res.Err = errors.New(detail)
			return true, nil
		case "error":
			detail := ev.Message
			if detail == "" {
				detail = "Codex error without details"
			}
			res.Err = errors.New(detail)
		default:
			emit(Message{Type: "log", Content: cp})
		}
		return false, nil
	}
}
