package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"runtime"

	"lykeion/server/internal/pricing"
)

type Codex struct{ Bin string }

const codexCredFile = "auth.json"

var plainKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func (c Codex) Execute(ctx context.Context, prompt string, o ExecOptions) (*Session, error) {
	for _, s := range o.MCP {
		if !plainKey.MatchString(s.Name) {
			return nil, fmt.Errorf("MCP server name %q is not a plain key", s.Name)
		}
	}
	bin := c.Bin
	if bin == "" {
		bin = "codex"
	}
	home, cleanup, err := prepareScratch(&o, "codex", "CODEX_HOME", ".codex", codexCredFile)
	if err != nil {
		return nil, err
	}
	var personal codexConfig
	if o.Inherit {
		personal = readCodexConfig(accountHome("codex"))
	}
	// Codex events name no model, so the run must know which one it chose.
	model := o.Model
	if model == "" {
		if model, err = codexModel(ctx, bin, o.Dir, childEnv(o, home), personal.Model); err != nil {
			cleanup(true)
			return nil, err
		}
	}
	// These flags belong to exec, before the optional resume subcommand.
	args := append([]string{"exec", "--json", "--skip-git-repo-check"}, codexSandbox(runtime.GOOS)...)
	args = append(args, "-m", model)
	if !o.Inherit {
		args = append(args, "--ignore-user-config", "--ignore-rules", "--disable", "apps", "--disable", "plugins", "--disable", "remote_plugin")
	} else if clash := personalClash(personal, o); clash != "" {
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
	}
	for _, s := range o.MCP {
		args = append(args, "-c", fmt.Sprintf("mcp_servers.%s.url=%s", ServerName(s.Name), raw(s.URL)))
	}
	if o.MCPURL != "" {
		args = append(args, "-c", fmt.Sprintf("mcp_servers.lyk_sources.url=%s", raw(o.MCPURL)), "-c", `mcp_servers.lyk_sources.bearer_token_env_var="LYK_TOKEN"`)
	}
	args = append(args, "--cd", o.Dir)
	if o.Resume != "" {
		args = append(args, "resume", o.Resume)
	}
	args = append(args, "-")
	return run(ctx, command(ctx, bin, prompt, o, home, args...), cleanup, parseCodex(model), func(res *Result) { res.Model = model }, o.OnMessage)
}

// personalClash names a personal MCP entry that shares a Lykeion entry's name.
func personalClash(personal codexConfig, o ExecOptions) string {
	names := make([]string, 0, len(o.MCP)+1)
	for _, s := range o.MCP {
		names = append(names, ServerName(s.Name))
	}
	if o.MCPURL != "" {
		names = append(names, "lyk_sources")
	}
	for _, n := range names {
		if _, ok := personal.MCPServers[n]; ok {
			return n
		}
	}
	return ""
}

// codexSandbox always keeps network, so lyk reaches the server and the daemon
// socket. macOS Seatbelt ignores network_access (openai/codex#10390), so there
// both modes run unsandboxed until a Codex release with the fix is known.
func codexSandbox(goos string) []string {
	if goos == "darwin" {
		return []string{"-s", "danger-full-access"}
	}
	return []string{"-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=true"}
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
