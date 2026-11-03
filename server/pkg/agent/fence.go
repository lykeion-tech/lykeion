package agent

import "encoding/json"

// doorServer is the daemon's tool door, the one MCP server a study run may call.
const doorServer = "lyk_door"

// claudeAllowed lists Claude's own tools a study run may use besides the
// door. Each touches only the run's folder, its own shells or the CLI's own
// state; anything else, a web tool, a subagent or another MCP server, reaches
// evidence past Lykeion's sources.
var claudeAllowed = map[string]bool{
	"Bash": true, "Read": true, "Write": true, "Edit": true, "MultiEdit": true, "Glob": true, "Grep": true, "LS": true,
	"TodoWrite": true, "NotebookEdit": true,
	// Skill loads the skills Lykeion staged; ToolSearch loads the door's own
	// tool schemas.
	"Skill": true, "ToolSearch": true,
	// The hand-in's own structured-output tool, if the CLI reports it as a call.
	"StructuredOutput": true,
	// Background shells the run started itself.
	"BashOutput": true, "TaskOutput": true, "KillShell": true, "KillBash": true, "TaskStop": true,
	// Resources of the configured MCP servers, which is the door alone.
	"ListMcpResourcesTool": true, "ReadMcpResourceTool": true,
}

// Fenced reports whether m is a tool call outside what a study run may use,
// and names the call for the person.
func Fenced(harness string, m Message) (breach bool, what string) {
	if harness == "codex" {
		return codexFenced(m, false)
	}
	if m.Type != "tool_use" || m.Server == doorServer || (harness == "claude" && m.Server == "" && claudeAllowed[m.Tool]) {
		return false, ""
	}
	return true, m.Tool
}

// LabFenced permits only the private lab door and the CLI machinery used
// to produce the structured answer.
func LabFenced(harness string, m Message) (bool, string) {
	if harness == "codex" {
		return codexFenced(m, true)
	}
	if m.Type != "tool_use" || m.Server == doorServer && m.Tool == "mcp__lyk_door__call_tool" ||
		m.Server == "" && m.Tool == "StructuredOutput" {
		return false, ""
	}
	return true, m.Tool
}

// codexFenced judges Codex's own event, so an item type the parser doesn't
// know is a breach rather than a log. A call to another server is caught
// when it starts, before its result reaches the agent.
func codexFenced(m Message, lab bool) (bool, string) {
	var ev struct {
		Type string
		Item struct{ Type, Server, Tool string }
	}
	if json.Unmarshal(m.Content, &ev) != nil {
		return m.Type == "tool_use", m.Tool
	}
	switch {
	case m.Type == "tool_use":
	case m.Type == "log" && ev.Type == "item.started" && (ev.Item.Type == "mcp_tool_call" || ev.Item.Type == "web_search" || lab && (ev.Item.Type == "command_execution" || ev.Item.Type == "file_change")):
	default:
		return false, ""
	}
	switch ev.Item.Type {
	case "command_execution", "file_change":
		if lab {
			return true, ev.Item.Type
		}
		return false, ""
	case "mcp_tool_call":
		if ev.Item.Server == doorServer && (!lab || ev.Item.Tool == "call_tool") {
			return false, ""
		}
		return true, ev.Item.Server + "/" + ev.Item.Tool
	}
	what := ev.Item.Tool
	if what == "" {
		what = ev.Item.Type
	}
	return true, what
}
