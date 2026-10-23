package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"lykeion/server/internal/pricing"
)

type Codex struct{ Bin string }

const codexCredFile = "auth.json"

func (c Codex) Execute(ctx context.Context, prompt string, o ExecOptions) (*Session, error) {
	bin := c.Bin
	if bin == "" {
		bin = "codex"
	}
	_, cleanup, err := prepareScratch(&o, "codex")
	if err != nil {
		return nil, err
	}
	personal := readCodexConfig(accountHome("codex"))
	// Codex events name no model, so the run must know which one it chose.
	model := o.Model
	if model == "" {
		if model, err = codexModel(ctx, bin, o, personal.Model); err != nil {
			cleanup(true)
			return nil, err
		}
	}
	// These flags belong to exec, before the optional resume subcommand.
	// Codex's own web search answers from OpenAI's side, past the sources
	// server that every cited source must come through, so it is off.
	args := []string{"exec", "--json", "--skip-git-repo-check", "-s", "danger-full-access", "-c", `web_search="disabled"`}
	args = append(args, "-m", model)
	if clash := personalClash(personal, o); clash != "" {
		// -c deep-merges into a personal entry of the same name, so its headers
		// would reach Lykeion's server. Only ignoring config.toml lets ours win.
		// Personal servers are not re-supplied: their secrets would sit in argv.
		args = append(args, "--ignore-user-config")
		if o.OnMessage != nil {
			text := "Your Codex config.toml has an MCP server named " + clash + ", which Lykeion uses; this run ignores your config.toml. Rename that entry to keep your Codex settings in Lykeion runs."
			if err = o.OnMessage(Message{Type: "status", Content: raw(map[string]string{"type": "continuity", "message": text})}); err != nil {
				cleanup(true)
				return nil, err
			}
		}
	} else if override, err := codexMCPCacheOverride(personal, o.CacheDir); err != nil {
		cleanup(true)
		return nil, err
	} else if override != "" {
		args = append(args, "-c", override)
	}
	if o.DoorURL != "" {
		// The door's token stays out of argv: Codex reads it from the env.
		args = append(args, "-c", fmt.Sprintf("mcp_servers.lyk_door.url=%s", raw(o.DoorURL)), "-c", `mcp_servers.lyk_door.bearer_token_env_var="LYK_DOOR_TOKEN"`,
			"-c", fmt.Sprintf("mcp_servers.lyk_door.tool_timeout_sec=%d", int(doorCallTimeout.Seconds())))
	}
	args = append(args, "--cd", o.Dir)
	if o.Resume != "" {
		args = append(args, "resume", o.Resume)
	}
	args = append(args, "-")
	cmd, err := command(ctx, bin, prompt, o, args...)
	if err != nil {
		cleanup(true)
		return nil, err
	}
	return run(ctx, cmd, cleanup, parseCodex(model), func(res *Result, _ emitMessage) { res.Model = model }, o.OnMessage)
}

// personalClash names a personal MCP entry that shares a Lykeion entry's name.
func personalClash(personal codexConfig, o ExecOptions) string {
	if _, ok := personal.MCPServers["lyk_door"]; ok && o.DoorURL != "" {
		return "lyk_door"
	}
	return ""
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
			_, priced := pricing.Prices[model]
			if !priced {
				message = "No list price is known for " + model + "; its tokens are recorded but count $0 against the card budget."
			}
			emit(Message{Type: "status", Content: cp})
			emit(Message{Type: "status", Content: raw(map[string]any{"type": "accounting", "model": model, "policy": "api_list_price_estimate", "priced": priced, "conservative_upper_bound": upper, "message": message})})
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
