package agent

// doorServer is the daemon's tool door, the one MCP server a study run may call.
const doorServer = "lyk_door"

// fenceAllowed lists each harness's own tools a study run may use besides the
// door. Each touches only the run's folder or the CLI's own state; anything
// else, a web tool, a subagent or another MCP server, reaches evidence past
// Lykeion's sources.
var fenceAllowed = map[string]map[string]bool{
	"codex": {"shell": true, "file_change": true},
	"claude": {
		"Bash": true, "Read": true, "Write": true, "Edit": true, "MultiEdit": true, "Glob": true, "Grep": true, "LS": true,
		"TodoWrite": true, "NotebookEdit": true,
		// Skill loads the skills Lykeion staged; ToolSearch loads the door's
		// own tool schemas.
		"Skill": true, "ToolSearch": true,
	},
}

// Fenced reports whether m is a tool call outside what a study run may use,
// and names the call for the person.
func Fenced(harness string, m Message) (breach bool, what string) {
	if m.Type != "tool_use" {
		return false, ""
	}
	if m.Server == doorServer {
		return false, ""
	}
	if m.Server == "" && fenceAllowed[harness][m.Tool] {
		return false, ""
	}
	if m.Server != "" && harness == "codex" {
		return true, m.Server + "/" + m.Tool
	}
	return true, m.Tool
}
