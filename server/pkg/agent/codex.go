package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

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
	if o.Coresearch {
		if err = prepareCoresearchCodexProject(o.Dir, o.CLIPaths.ConfigHome); err != nil {
			cleanup(true)
			return nil, err
		}
	}
	personal := readCodexConfig(runConfigHome("codex", o.ConfigHome))
	// Codex events name no model, so the run must know which one it chose.
	model := o.Model
	if o.Coresearch && (!o.Aristotle || model == "") {
		// Exec events do not identify the provider-selected model. Keep its
		// tokens reportable without guessing a model or injecting a selection.
		model = "unknown"
	}
	if model == "" && !o.Coresearch {
		if model, err = codexModel(ctx, bin, o, personal.Model); err != nil {
			cleanup(true)
			return nil, err
		}
	}
	// These flags belong to exec, before the optional resume subcommand.
	// Codex's own web search answers from OpenAI's side, past the sources
	// server that every cited source must come through, so it is off.
	args := []string{"exec", "--json", "--skip-git-repo-check", "-s", "danger-full-access", "-c", `web_search="disabled"`}
	if o.Guest {
		// Login profiles replace the trusted run PATH before shell tools run.
		args = append(args, "-c", "allow_login_shell=false")
	}
	if !o.Coresearch || (o.Aristotle && o.Model != "") {
		args = append(args, "-m", model)
	}
	if o.Coresearch {
		args = append(args, "--ignore-user-config", "--ignore-rules", "--disable", "hooks", "--disable", "plugins", "--disable", "remote_plugin", "--disable", "multi_agent", "--disable", "skill_mcp_dependency_install", "--disable", "apps", "--disable", "memories")
		args = append(args, "-c", `project_root_markers=[".agent-owned"]`, "-c", `skills.bundled.enabled=false`)
		o.CLIPaths.ManagedSkills, err = SkillsDir("codex", o.Dir)
		if err != nil {
			cleanup(true)
			return nil, err
		}
		if o.SourcesURL != "" {
			args = append(args, "-c", fmt.Sprintf("mcp_servers.lyk_sources.url=%s", raw(o.SourcesURL)), "-c", `mcp_servers.lyk_sources.bearer_token_env_var="LYK_TOKEN"`)
		}
	} else {
		// A study run keeps the account's config.toml settings but none of the
		// person's own context: no memories, rules, hooks, plugins or ChatGPT
		// apps, whose connectors reach the web past the sources server.
		args = append(args, "--ignore-rules")
		for _, feature := range []string{"apps", "hooks", "memories", "multi_agent", "plugins", "remote_plugin", "skill_mcp_dependency_install"} {
			args = append(args, "--disable", feature)
		}
	}
	if clash := personalClash(personal, o); !o.Coresearch && (clash != "" || personal.Unreadable) {
		// -c deep-merges into a personal entry of the same name, so its headers
		// would reach Lykeion's server. Only ignoring config.toml lets ours win.
		// Personal servers are not re-supplied: their secrets would sit in argv.
		// A config that cannot be read could start servers nothing turns off.
		args = append(args, "--ignore-user-config")
		if o.OnMessage != nil {
			text := "Your Codex config.toml has an MCP server named " + clash + ", which Lykeion uses; this run ignores your config.toml. Rename that entry to keep your Codex settings in Lykeion runs."
			if clash == "" {
				text = "Lykeion could not read your Codex config.toml, so this run ignores it. Fix the file to keep your Codex settings in Lykeion runs."
			}
			if err = o.OnMessage(Message{Type: "status", Content: raw(map[string]string{"type": "continuity", "message": text})}); err != nil {
				cleanup(true)
				return nil, err
			}
		}
	} else if !o.Coresearch {
		if override, err := codexPersonalMCPOff(personal); err != nil {
			cleanup(true)
			return nil, err
		} else if override != "" {
			args = append(args, "-c", override)
		}
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
			Item  struct{ Type, Text, Tool, Server string } `json:"item"`
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
				emit(Message{Type: "tool_use", Tool: ev.Item.Tool, Server: ev.Item.Server, Content: cp})
			case "todo_list", "error":
				emit(Message{Type: "log", Content: cp})
			default:
				// Anything else is an action, file changes and web searches
				// among them, so the fence judges every item it doesn't know.
				tool := ev.Item.Tool
				if tool == "" {
					tool = ev.Item.Type
				}
				emit(Message{Type: "tool_use", Tool: tool, Content: cp})
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

// Project configuration is a reserved daemon-owned tree in a chat. Replacing
// it through the pinned chat root prevents stale local settings from reaching
// the CLI without touching account authentication or session state.
func prepareCoresearchCodexProject(dir, account string) error {
	project := filepath.Join(dir, ".codex")
	rel, err := filepath.Rel(project, account)
	if err != nil || rel == "." || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != "..") {
		return errors.New("Coresearch account home overlaps managed project configuration")
	}
	canonical, err := filepath.EvalSymlinks(account)
	if err != nil {
		return err
	}
	rel, err = filepath.Rel(project, canonical)
	if err != nil || rel == "." || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != "..") {
		return errors.New("Coresearch account home overlaps managed project configuration")
	}
	// Filesystem identity also covers case-insensitive aliases that lexical
	// relative paths cannot recognize.
	projectInfo, statErr := os.Lstat(project)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if statErr == nil && projectInfo.IsDir() {
		for current := canonical; ; current = filepath.Dir(current) {
			info, err := os.Stat(current)
			if err != nil {
				return err
			}
			if os.SameFile(projectInfo, info) {
				return errors.New("Coresearch account home overlaps managed project configuration")
			}
			if current == filepath.Dir(current) {
				break
			}
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err = root.RemoveAll(".codex"); err != nil {
		return err
	}
	return root.MkdirAll(".codex", 0700)
}
