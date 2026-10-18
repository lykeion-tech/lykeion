package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Antigravity struct {
	Bin string
}

// AntigravityModel is passed when the agent run names no model: agy reports no
// cost, so the adapter must know which model its usage is priced as.
const AntigravityModel = "gemini-3-pro"

// antigravityCredFile is the sign-in file under agy's config home, which it
// shares with the Gemini CLI.
const antigravityCredFile = ".gemini/oauth_creds.json"

// agy stops waiting for the model after this long; the server's lease bounds
// the agent run anyway.
const antigravityPrintTimeout = "1h0m0s"

// ErrNoReadOnly is returned for a read-only run: agy has no mode that
// guarantees it changes nothing.
var ErrNoReadOnly = errors.New("antigravity has no read-only mode")

var (
	agyConversation = regexp.MustCompile(`conversation=([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`)
	agyPrintTimeout = regexp.MustCompile(`Print mode: timed out after \d+ polls`)
	agyProviderErr  = regexp.MustCompile(`agent executor error:\s*(.+)`)
)

// Execute runs agy in print mode with stream-json events. agy writes its
// conversation id, and some failures, only to its log file, so the log is read
// once agy has exited.
func (a Antigravity) Execute(ctx context.Context, prompt string, o ExecOptions) (*Session, error) {
	if o.Inherit {
		return nil, errPrivateOnly
	}
	if o.ReadOnly {
		return nil, ErrNoReadOnly
	}
	home, cleanup, err := prepareScratch(&o, "antigravity", "GEMINI_CLI_HOME", "", antigravityCredFile)
	if err != nil {
		return nil, err
	}
	if err = writeAntigravityMCP(o); err != nil {
		cleanup(true)
		return nil, err
	}
	model := o.Model
	if model == "" {
		model = AntigravityModel
	}
	logPath := filepath.Join(o.Dir, ".agy.log")
	if err = os.Remove(logPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		cleanup(true)
		return nil, err
	}
	args := []string{"-p", prompt, "--dangerously-skip-permissions", "--output-format", "stream-json", "--model", model,
		"--print-timeout", antigravityPrintTimeout, "--log-file", logPath, "--add-dir", o.Dir}
	if o.Resume != "" {
		args = append(args, "--conversation", o.Resume)
	}
	bin := a.Bin
	if bin == "" {
		bin = "agy"
	}
	r := &agyRun{model: model, steps: map[int]agyUsage{}, tools: map[int]int{}}
	return run(ctx, command(ctx, bin, prompt, o, home, args...), cleanup, r.parse, r.finish(ctx, logPath, home), o.OnMessage)
}

type agyUsage struct {
	Input      int64 `json:"input_tokens"`
	Output     int64 `json:"output_tokens"` // includes thinking tokens
	CacheRead  int64 `json:"cache_read_tokens"`
	CacheWrite int64 `json:"cache_write_tokens"`
}

type agyResult struct {
	ConversationID string    `json:"conversation_id"`
	Status         string    `json:"status"`
	Response       string    `json:"response"`
	Error          string    `json:"error"`
	Usage          *agyUsage `json:"usage"`
}

// agyRun is the state one run's events build up.
type agyRun struct {
	model   string
	session string
	text    bool             // any reply text was streamed
	events  bool             // any stream-json event arrived
	steps   map[int]agyUsage // usage of completed steps, by step index
	tools   map[int]int      // per tool step: 1 reported as used, 2 also as done
	result  *agyResult
}

// parse reports every line as terminal: agy's end is judged once it has
// exited, by finish.
func (r *agyRun) parse(line []byte, emit emitMessage, _ *Result) (bool, error) {
	var ev struct {
		Event          string `json:"event"`
		ConversationID string `json:"conversation_id"`
		StepUpdate     *struct {
			ConversationID string    `json:"conversation_id"`
			StepIndex      *int      `json:"step_index"`
			State          string    `json:"state"`
			StepType       string    `json:"step_type"`
			TextDelta      string    `json:"text_delta"`
			ToolName       string    `json:"tool_name"`
			Usage          *agyUsage `json:"usage"`
			ToolInfo       *struct {
				Name string `json:"name"`
			} `json:"tool_info"`
		} `json:"step_update"`
		Result *agyResult `json:"result"`
	}
	if json.Unmarshal(line, &ev) != nil || ev.Event == "" {
		// Not an event: agy prints plain text in some releases and for its
		// own errors.
		if len(bytes.TrimSpace(line)) == 0 {
			return false, nil
		}
		r.text = r.text || len(bytes.TrimSpace(line)) > 0
		emit(Message{Type: "text", Content: raw(string(line))})
		return true, nil
	}
	r.events = true
	cp := json.RawMessage(append([]byte(nil), line...))
	switch ev.Event {
	case "init":
		r.session = ev.ConversationID
		emit(Message{Type: "log", Content: cp})
	case "step_update":
		su := ev.StepUpdate
		if su == nil {
			return true, nil
		}
		if su.ConversationID != "" {
			r.session = su.ConversationID
		}
		done := strings.EqualFold(su.State, "done")
		switch su.StepType {
		case "agent_response":
			if su.TextDelta != "" {
				r.text = r.text || strings.TrimSpace(su.TextDelta) != ""
				emit(Message{Type: "text", Content: raw(su.TextDelta)})
			}
		case "tool":
			name := su.ToolName
			if name == "" && su.ToolInfo != nil {
				name = su.ToolInfo.Name
			}
			if su.StepIndex != nil && name != "" {
				i := *su.StepIndex
				if r.tools[i] == 0 {
					r.tools[i] = 1
					emit(Message{Type: "tool_use", Tool: name, Content: cp})
				}
				if done && r.tools[i] == 1 {
					r.tools[i] = 2
					emit(Message{Type: "tool_result", Tool: name, Content: cp})
				}
			}
		}
		// A step is re-emitted as it changes; keying by index counts it once.
		if done && su.StepIndex != nil && su.Usage != nil {
			r.steps[*su.StepIndex] = *su.Usage
		}
	case "result":
		r.result = ev.Result
		if ev.Result != nil {
			if ev.Result.ConversationID != "" {
				r.session = ev.Result.ConversationID
			}
			if ev.Result.Response != "" && !r.text {
				r.text = strings.TrimSpace(ev.Result.Response) != ""
				emit(Message{Type: "text", Content: raw(ev.Result.Response)})
			}
		}
	default:
		emit(Message{Type: "log", Content: cp})
	}
	return true, nil
}

// finish returns run's finish hook, which completes the result once agy has
// exited and its log is final. A failure agy reports only in its log, or no
// output at all, replaces run's generic verdict, which says less; run then
// classifies it.
func (r *agyRun) finish(ctx context.Context, logPath, home string) func(*Result, emitMessage) {
	return func(res *Result, emit emitMessage) {
		log, _ := os.ReadFile(logPath)
		res.SessionID = r.session
		if res.SessionID == "" {
			if m := agyConversation.FindAllSubmatch(log, -1); len(m) > 0 {
				res.SessionID = string(m[len(m)-1][1])
			}
		}
		// Completed steps cover only this run; the result's usage is the running
		// total of a resumed conversation, so it is used only without them.
		var u agyUsage
		for _, s := range r.steps {
			u.Input, u.Output, u.CacheRead, u.CacheWrite = u.Input+s.Input, u.Output+s.Output, u.CacheRead+s.CacheRead, u.CacheWrite+s.CacheWrite
		}
		if len(r.steps) == 0 && r.result != nil && r.result.Usage != nil {
			u = *r.result.Usage
		}
		if u != (agyUsage{}) {
			res.Usage = []Usage{{Model: r.model, InputTokens: max(u.Input, 0), OutputTokens: max(u.Output, 0),
				CacheReadTokens: max(u.CacheRead, 0), CacheWriteTokens: max(u.CacheWrite, 0)}}
		}
		if res.Reason != "" { // cancelled, or the stream itself failed: run has judged it
			return
		}
		var err error
		switch status := strings.ToUpper(r.statusOrEmpty()); {
		case status != "" && status != "SUCCESS" && status != "COMPLETED":
			msg := r.result.Error
			if msg == "" {
				msg = "agy returned status " + r.result.Status
			}
			err = errors.New(msg)
		case agyPrintTimeout.Match(log):
			err = errors.New("agy's print timeout elapsed before the agent answered")
		case agyProviderErr.Match(log):
			m := agyProviderErr.FindAllSubmatch(log, -1)
			err = fmt.Errorf("agy provider error: %s", strings.TrimSpace(string(m[len(m)-1][1])))
		}
		if err != nil {
			res.Err = err
			return
		}
		if res.Err != nil && !errors.Is(res.Err, errMissingTerminal) {
			return
		}
		if !r.text {
			if text := recoverAgyTranscript(ctx, log, home, res.SessionID); text != "" && ctx.Err() == nil {
				emit(Message{Type: "text", Content: raw(text)})
				res.Err = nil
			} else {
				res.Err = fmt.Errorf("agy produced no output; is %q a model this agy offers?", r.model)
			}
		}
	}
}

func (r *agyRun) statusOrEmpty() string {
	if r.result == nil {
		return ""
	}
	return r.result.Status
}

var agyAppData = regexp.MustCompile(`CLI app data directory:\s*([^\r\n]+)`)

// recoverAgyTranscript selects completed model narration after the latest user
// input. A private run never follows a logged path outside its own CLI home.
func recoverAgyTranscript(ctx context.Context, log []byte, home, session string) string {
	if ctx.Err() != nil || !sessionToken.MatchString(session) {
		return ""
	}
	match := agyAppData.FindSubmatch(log)
	if match == nil {
		return ""
	}
	app := strings.TrimSpace(string(match[1]))
	if !filepath.IsAbs(app) {
		return ""
	}
	path := filepath.Join(app, "brain", session, ".system_generated", "logs", "transcript.jsonl")
	rel, err := filepath.Rel(home, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	// Check the root too: replacing the home itself with a symlink must not
	// redirect a transcript read to the account's files.
	current := home
	parts := append([]string{""}, strings.Split(rel, string(filepath.Separator))...)
	for i, part := range parts {
		current = filepath.Join(current, part)
		st, err := os.Lstat(current)
		if err != nil || st.Mode()&os.ModeSymlink != 0 {
			return ""
		}
		if i == len(parts)-1 {
			if !st.Mode().IsRegular() {
				return ""
			}
		} else if !st.IsDir() {
			return ""
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	return scanAgyTranscript(ctx, f)
}

func scanAgyTranscript(ctx context.Context, input io.Reader) string {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 64<<10), 16<<20)
	var selected []string
	for ctx.Err() == nil && scanner.Scan() {
		if ctx.Err() != nil {
			return ""
		}
		var item struct {
			Type    string          `json:"type"`
			Source  string          `json:"source"`
			Status  string          `json:"status"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(scanner.Bytes(), &item) != nil {
			continue
		}
		if item.Type == "USER_INPUT" {
			selected = nil
			continue
		}
		if item.Type != "PLANNER_RESPONSE" || item.Source != "MODEL" || item.Status != "DONE" {
			continue
		}
		var text string
		if json.Unmarshal(item.Content, &text) == nil && strings.TrimSpace(text) != "" {
			selected = append(selected, text)
		}
	}
	if ctx.Err() != nil || scanner.Err() != nil {
		return ""
	}
	return strings.Join(selected, "\n\n")
}
