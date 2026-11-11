package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Claude struct{ Bin string }

// claudeTools are pre-approved in every run. Reads are not among them: a run
// may read only its own folders (claudeRule), and a bare Glob or Grep allow
// would search past every Read path rule.
const claudeTools = "Skill,Bash(lyk:*),mcp__lyk_door"

const claudeCredFile = ".credentials.json"

// ScriptLimit is how long a script run through the door may take.
const ScriptLimit = 2 * time.Hour

// doorCallTimeout lets a door call outlast the script it runs, so the door's
// limit ends a long script and not the CLI's own tool-call timeout. In Claude
// Code the per-server timeout also lifts its request and idle timeouts.
const doorCallTimeout = ScriptLimit + 5*time.Minute

// The installer's entry point can move to a new release between commands.
// Resolve it once, so the probe and private turn use the same versioned path.
func resolveLabClaude(bin string) (string, error) {
	path, err := exec.LookPath(bin)
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}

// Keep lab execution on the CLI version exercised by the compatibility probes.
// This version check does not establish provider or tool isolation.
func checkLabClaude(ctx context.Context, bin string, o ExecOptions) error {
	probe, cancel := context.WithTimeout(ctx, VersionTimeout)
	defer cancel()
	cmd, err := command(probe, bin, "", o, "--version")
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(out, 256))
	out.Close()
	waitErr := cmd.Wait()
	version, _, _ := strings.Cut(strings.TrimSpace(string(data)), " ")
	if err == nil && waitErr == nil && version == "2.1.317" {
		return nil
	}
	return errors.New("lab questions require verified Claude Code version 2.1.317")
}

func (c Claude) Execute(ctx context.Context, prompt string, o ExecOptions) (*Session, error) {
	if o.Lab {
		if err := LabAvailability("claude"); err != nil {
			return nil, err
		}
	}
	return c.execute(ctx, prompt, o)
}

func (c Claude) execute(ctx context.Context, prompt string, o ExecOptions) (session *Session, resultErr error) {
	if o.Coresearch && !o.Aristotle {
		o.Model = ""
	}
	home, cleanup, err := prepareScratch(&o, "claude")
	if err != nil {
		return nil, err
	}
	if len(o.ClaudeBaseline) > 0 {
		if len(o.ClaudeBaseline) > claudeSnapshotLimit {
			cleanup(true)
			return nil, errors.New("Claude usage baseline exceeds size limit")
		}
		if err = writeClaudeSnapshot(filepath.Join(home, ".lyk-usage.json"), o.ClaudeBaseline); err != nil {
			cleanup(true)
			return nil, err
		}
	}
	accounting, err := beginClaudeUsage(home, o.Resume)
	if err != nil {
		cleanup(true)
		return nil, err
	}
	cfg, err := mcpDescriptor(claudeMCP(o))
	if err != nil {
		cleanup(true)
		return nil, err
	}
	defer cfg.Close()
	const cfgArg = "LYKEION_MCP_FD_PATH"
	retireConfig := func() error { return nil }
	if runtime.GOOS == "darwin" {
		o.CLIPaths.MCPConfig = cfg.Name()
		fileInfo, statErr := cfg.Stat()
		dir := filepath.Dir(cfg.Name())
		dirInfo, dirErr := os.Lstat(dir)
		if statErr != nil || dirErr != nil {
			cleanup(true)
			return nil, errors.Join(statErr, dirErr, errors.New("private MCP configuration retained because identity could not be verified"))
		}
		var once sync.Once
		var retireErr error
		retireConfig = func() error {
			once.Do(func() {
				currentDir, err := os.Lstat(dir)
				if err != nil || !os.SameFile(currentDir, dirInfo) {
					retireErr = errors.Join(errors.New("private MCP directory changed or unavailable"), err)
					return
				}
				current, err := os.Lstat(cfg.Name())
				if err != nil || !os.SameFile(current, fileInfo) {
					retireErr = errors.Join(errors.New("private MCP configuration changed or unavailable"), err)
					return
				}
				if err = os.Remove(cfg.Name()); err == nil {
					err = os.Remove(dir)
				}
				retireErr = err
			})
			return retireErr
		}
		defer func() {
			if session == nil {
				resultErr = errors.Join(resultErr, retireConfig())
			}
		}()
		previous := cleanup
		cleanup = func(release bool) {
			if release {
				_ = retireConfig()
			}
			previous(release)
		}
	}
	mode := "dontAsk"
	if !o.ReadOnly {
		mode = "default" // the account's own allow and deny rules decide the rest
	}
	// Glob and Grep obey Read path rules; reads elsewhere go to the account's
	// own rules, or are refused in an isolated run. The CLI's own homes in the
	// run folder are its alone: it uses them, its file tools may not.
	tools := []string{claudeTools}
	if o.Coresearch {
		tools = []string{"Skill,Read,Edit,Write,Bash,Glob,Grep,mcp__lyk_sources"}
		if o.DoorURL != "" {
			tools = append(tools, "mcp__lyk_door")
		}
	}
	var deny []string
	if o.ReadOnly {
		// Plan mode would read any path, so a read-only run asks nothing and
		// may only read its folders, load skills and call connectors through
		// the door, never run code: no account rule edits or runs commands,
		// and nothing else is pre-approved.
		tools = []string{"Skill,mcp__lyk_door__call_tool"}
	}
	if rule := claudeRule("Edit", o.Dir, ""); rule != "" {
		if !o.ReadOnly {
			// The folder's own .claude settings, the CLI's homes, whose
			// settings and hooks the CLI loads, and its git repository, whose
			// hooks git runs, stay out of reach, as do the supervisor's
			// markers; a deny rule wins over every allow rule. A .git file
			// can point git at another repository, so .git itself is denied.
			tools = append(tools, rule)
			for _, sub := range []string{"/.claude", "/.cli-home", "/.home", "/.git"} {
				deny = append(deny, claudeRule("Edit", o.Dir, sub))
			}
			for _, file := range []string{"/.git", "/.agent-exit", "/.agent-process"} {
				deny = append(deny, strings.TrimSuffix(claudeRule("Edit", o.Dir, file), "/**)")+")")
			}
		}
		deny = append(deny, claudeRule("Read", o.Dir, "/.cli-home"), claudeRule("Read", o.Dir, "/.home"))
	}
	for _, dir := range append([]string{o.Dir}, o.ReadDirs...) {
		if rule := claudeRule("Read", dir, ""); rule != "" {
			tools = append(tools, rule)
		}
	}
	if o.ReadOnly {
		deny = append(deny, "Edit(//**)", "Bash")
	}
	if !o.Coresearch {
		// A study's evidence comes only through the sources server, whatever
		// the person's own rules allow; a deny rule wins over them. Nobody
		// answers an unattended run, so it has no subagents, questions or
		// plan mode either; the fence still stops any that slip through.
		deny = append(deny, "WebFetch", "WebSearch", "Task", "Agent", "AskUserQuestion", "EnterPlanMode", "ExitPlanMode")
	}
	if len(deny) > 0 {
		tools = append(append(tools, "--disallowedTools"), deny...)
	}
	// A run brings its own role, skills and tools, so how it behaves doesn't
	// depend on what the person has installed: none of their hooks or MCP
	// servers run, and no CLAUDE.md becomes instructions, even one in the
	// run's own folders or added after startup. A study run keeps the
	// account's settings, which may carry its sign-in and default model, but
	// not its output style; the sandbox hides its plugins, skills, styles and
	// instructions.
	settings := `{"disableAllHooks":true,"claudeMdExcludes":["**"],"outputStyle":"default"}`
	if o.Coresearch {
		settings = `{"disableAllHooks":true,"claudeMdExcludes":["**"]}`
	}
	args := slices.Concat([]string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--permission-mode", mode, "--allowedTools"}, tools, []string{"--mcp-config", cfgArg, "--strict-mcp-config", "--settings", settings})
	if o.NoTools && !o.Lab {
		args = append(args, "--tools", "StructuredOutput")
	}
	if o.Lab {
		// Retain the CLI restrictions, while recognizing that managed settings
		// may override configuration and hooks outside these command-line controls.
		args = append(args, "--restricted", "--setting-sources", "", "--tools", "StructuredOutput", "--disable-slash-commands")
	}
	if o.Coresearch {
		args = append(args, "--setting-sources", "project")
		o.CLIPaths.ManagedSkills, err = SkillsDir("claude", o.Dir)
		if err != nil {
			cleanup(true)
			return nil, err
		}
	}
	if o.Instructions != "" {
		// claudeMdExcludes keeps the run's own CLAUDE.md out too, so the role
		// arrives as a system prompt. A file keeps a long role clear of argv
		// limits; the CLI's home is out of the agent's file tools' reach.
		role := filepath.Join(home, "role.md")
		if err = writeClaudeSnapshot(role, []byte(o.Instructions)); err != nil {
			cleanup(true)
			return nil, err
		}
		args = append(args, "--append-system-prompt-file", role)
	}
	if o.Model != "" && (!o.Coresearch || o.Aristotle) {
		args = append(args, "--model", o.Model)
	}
	if o.Resume != "" {
		args = append(args, "--resume", o.Resume)
	}
	if o.OutputSchema != "" {
		args = append(args, "--json-schema", o.OutputSchema)
	}
	if o.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(o.MaxTurns))
	}
	bin := c.Bin
	if bin == "" {
		bin = "claude"
	}
	if o.Lab {
		bin, err = resolveLabClaude(bin)
		if err != nil {
			cleanup(true)
			return nil, fmt.Errorf("resolve lab Claude executable: %w", err)
		}
		if err = checkLabClaude(ctx, bin, o); err != nil {
			cleanup(true)
			return nil, err
		}
	}
	cmd, err := command(ctx, bin, prompt, o, args...)
	if err != nil {
		cleanup(true)
		return nil, err
	}
	fdPath := "/dev/fd/" + strconv.Itoa(3+len(cmd.ExtraFiles))
	if runtime.GOOS == "darwin" {
		fdPath = cfg.Name()
	}
	found := false
	for i, arg := range cmd.Args {
		if arg == cfgArg {
			cmd.Args[i] = fdPath
			found = true
		}
	}
	if !found {
		cleanup(true)
		return nil, errors.New("sandbox did not preserve MCP configuration argument")
	}
	if runtime.GOOS != "darwin" {
		cmd.ExtraFiles = append(cmd.ExtraFiles, cfg)
	}
	stdin, w, err := os.Pipe()
	if err != nil {
		cleanup(true)
		return nil, err
	}
	cmd.Stdin = stdin
	in := newClaudeInput(w)
	if err = in.send(claudeUserFrame(prompt)); err != nil {
		in.close()
		stdin.Close()
		cleanup(true)
		return nil, err
	}
	go func() {
		select {
		case <-ctx.Done():
			in.close()
		case <-in.stop:
		}
	}()
	if o.Input != nil {
		go func() {
			for {
				select {
				case <-in.stop:
					return
				case text, ok := <-o.Input:
					if !ok {
						return
					}
					// A message arriving as the turn ends has nowhere to go.
					_ = in.send(claudeUserFrame(text))
				}
			}
		}()
	}
	finish := func(res *Result, _ emitMessage) {
		in.close()
		accounting.finish(res)
		if err := retireConfig(); err != nil {
			res.Err = errors.Join(res.Err, fmt.Errorf("retire private MCP configuration: %w", err))
		}
	}
	sess, err := run(ctx, cmd, cleanup, parseClaude(o.Model, o.Resume, accounting, in), finish, o.OnMessage)
	// The child holds its own copy of the read end once started.
	stdin.Close()
	if err != nil {
		in.close()
	}
	return sess, err
}

// claudeRule names Claude's file-edit or file-read tools (tool is Edit or
// Read) for everything under dir, or under sub inside it. The folder is
// matched literally: its glob characters are escaped. A name the CLI would
// split into several rules (commas, parentheses) gets no rule at all, so use
// there falls back to the permission mode.
func claudeRule(tool, dir, sub string) string {
	abs, err := filepath.Abs(dir)
	if err != nil || !filepath.IsAbs(abs) || strings.ContainsAny(abs, ",()\r\n") {
		return ""
	}
	var b strings.Builder
	for _, r := range abs {
		if strings.ContainsRune(`\*?[]{}!`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	// A leading "//" makes the pattern absolute rather than settings-relative.
	return tool + "(/" + b.String() + sub + "/**)"
}

// claudeMCP names the daemon's tool door and nothing else.
func claudeMCP(o ExecOptions) []byte {
	servers := map[string]any{}
	if o.Coresearch && o.SourcesURL != "" {
		servers["lyk_sources"] = map[string]any{"type": "http", "url": o.SourcesURL, "headers": map[string]string{"Authorization": "Bearer ${LYK_TOKEN}"}}
	}
	if o.DoorURL != "" {
		servers["lyk_door"] = map[string]any{"type": "http", "url": o.DoorURL, "headers": map[string]string{"Authorization": "Bearer " + o.DoorToken}, "timeout": doorCallTimeout.Milliseconds()}
	}
	return raw(map[string]any{"mcpServers": servers})
}

type claudeTokens struct {
	Input      int64    `json:"inputTokens"`
	Output     int64    `json:"outputTokens"`
	CacheRead  int64    `json:"cacheReadInputTokens"`
	CacheWrite int64    `json:"cacheCreationInputTokens"`
	Cost       *float64 `json:"costUSD"`
}

// in is nil for the F2 text-stdin transport, whose control frames stay logs.
func parseClaude(fallbackModel, resume string, accounting *claudeAccounting, in *claudeInput) parser {
	sawWork := false
	return func(line []byte, emit emitMessage, res *Result) (bool, error) {
		var ev struct {
			Type           string          `json:"type"`
			Session        string          `json:"session_id"`
			Model          string          `json:"model"`
			IsError        bool            `json:"is_error"`
			Cost           *float64        `json:"total_cost_usd"`
			Result         string          `json:"result"`
			Structured     json.RawMessage `json:"structured_output"`
			Subtype        string          `json:"subtype"`
			Errors         []string        `json:"errors"`
			TerminalReason string          `json:"terminal_reason"`
			NumTurns       int             `json:"num_turns"`
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
		if ev.Type == "system" && ev.Model != "" && len(ev.Model) <= 200 {
			res.Model = ev.Model
		}
		switch ev.Type {
		case "control_request":
			if in == nil {
				emit(Message{Type: "log", Content: append(json.RawMessage(nil), line...)})
				return false, nil
			}
			reply, record, err := claudeControl(line)
			if err != nil {
				return false, err
			}
			// The durable record precedes the reply the CLI acts on.
			emit(record)
			if err := in.send(reply); err != nil {
				return false, fmt.Errorf("answer Claude control request: %w", err)
			}
		case "assistant", "user":
			if ev.Type == "assistant" {
				sawWork = true
			}
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
					// The whole block, so its id pairs it with its tool_result.
					// An MCP tool keeps its whole mcp__<server>__<tool> name.
					m := Message{Type: "tool_use", Tool: item.Name, Content: c}
					if rest, ok := strings.CutPrefix(item.Name, "mcp__"); ok {
						m.Server, _, _ = strings.Cut(rest, "__")
					}
					emit(m)
				case "tool_result":
					emit(Message{Type: "tool_result", Content: c})
				}
			}
		case "result":
			if in != nil {
				in.close()
			}
			if resume != "" && !sawWork && ev.IsError && ev.NumTurns == 0 && (ev.Cost == nil || *ev.Cost == 0) && len(ev.ModelUsage) == 0 {
				res.ResumeRefused = true
			}
			// Result usage is authoritative and already excludes Claude's caches from
			// input tokens. Repeated terminal snapshots replace rather than add totals.
			res.Output = ev.Result
			if len(ev.Structured) > 0 && string(ev.Structured) != "null" {
				res.Output = string(ev.Structured)
			}
			res.Usage = nil
			for _, model := range slices.Sorted(maps.Keys(ev.ModelUsage)) {
				u := ev.ModelUsage[model]
				if strings.TrimSpace(model) == "" {
					continue
				}
				res.Usage = append(res.Usage, Usage{Model: model, InputTokens: u.Input, OutputTokens: u.Output, CacheReadTokens: u.CacheRead, CacheWriteTokens: u.CacheWrite, CostUSD: u.Cost})
			}
			if res.Model == "" && len(res.Usage) > 0 {
				res.Model = res.Usage[0].Model
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

// regularFileExists checks a file the adapter replaces without following
// links: it is absent or a regular file, never anything else.
func regularFileExists(path string) (bool, error) {
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !st.Mode().IsRegular() {
		return false, fmt.Errorf("not a regular file: %s", path)
	}
	return true, nil
}
