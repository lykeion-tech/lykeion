package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

type StepTitle struct {
	Seq       int32  `json:"seq"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	Title     string `json:"title"`
}
type StepTitles struct {
	Type    string      `json:"type"`
	Version int         `json:"version"`
	Titles  []StepTitle `json:"titles"`
}

func ValidStepTitle(title string) bool {
	return title != "" && title == strings.TrimSpace(title) && utf8.RuneCountInString(title) <= 80 && !strings.ContainsAny(title, "\r\n<>") && len(strings.Fields(title)) <= 6
}

// ParseStepTitles distinguishes our versioned metadata from ordinary status
// records. Unknown versions fail closed, without reinterpreting research data.
func ParseStepTitles(content json.RawMessage) (*StepTitles, error) {
	var header map[string]json.RawMessage
	if json.Unmarshal(content, &header) != nil {
		return nil, nil
	}
	var kind string
	if json.Unmarshal(header["type"], &kind) != nil || kind != "tool_step_titles" {
		return nil, nil
	}
	var meta StepTitles
	if json.Unmarshal(content, &meta) != nil || meta.Version != 1 || len(meta.Titles) == 0 || len(meta.Titles) > 20 {
		return nil, errors.New("invalid tool step titles")
	}
	seen := map[int32]bool{}
	for _, title := range meta.Titles {
		if title.Seq <= 0 || seen[title.Seq] || !ValidStepTitle(title.Title) {
			return nil, errors.New("invalid tool step title")
		}
		seen[title.Seq] = true
	}
	return &meta, nil
}
func namingObject(value any) map[string]any { obj, _ := value.(map[string]any); return obj }
func namingString(values ...any) string {
	for _, v := range values {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}
func namingArgs(value any) map[string]any {
	if s, ok := value.(string); ok {
		var obj map[string]any
		json.Unmarshal([]byte(s), &obj)
		return obj
	}
	return namingObject(value)
}

// ToolNamingEvidence normalizes only the tool records our adapters emit. The
// full input supplies the evidence; a technical name alone is only a fallback.
func ToolNamingEvidence(m Message) (id, tool, evidence string, ok bool) {
	if m.Type != "tool_use" {
		return
	}
	var content map[string]any
	if json.Unmarshal(m.Content, &content) != nil {
		return
	}
	tool = m.Tool
	input := content
	switch {
	case content["item"] != nil:
		item := namingObject(content["item"])
		id = namingString(item["id"])
		tool = namingString(tool, item["tool"], item["type"])
		input = namingArgs(item["arguments"])
		if item["type"] == "command_execution" {
			input = map[string]any{"command": item["command"]}
		}
	case content["tool_call"] != nil:
		id = namingString(content["call_id"])
		for name, value := range namingObject(content["tool_call"]) {
			tool = namingString(tool, name)
			input = namingObject(namingObject(value)["args"])
			break
		}
	case content["step_update"] != nil:
		step := namingObject(content["step_update"])
		if index, found := step["step_index"].(float64); found {
			id = fmt.Sprintf("step:%.0f", index)
		}
		tool = namingString(tool, step["tool_name"])
		input = namingObject(namingObject(step["tool_info"])["parameters"])
	default:
		id = namingString(content["id"])
		tool = namingString(tool, content["name"])
		if obj := namingObject(content["input"]); obj != nil {
			input = obj
		}
	}
	// Routing addresses are useful in the raw record, not in the naming voice.
	if strings.HasPrefix(tool, "mcp__") {
		parts := strings.SplitN(tool, "__", 3)
		if len(parts) == 3 {
			tool = parts[2]
		}
	}
	if strings.HasPrefix(tool, "mcp.") {
		parts := strings.SplitN(tool, ".", 3)
		if len(parts) == 3 {
			tool = parts[2]
		}
	}
	if tool == "TodoWrite" || tool == "ExitPlanMode" {
		return id, tool, "", false
	}
	if tool == "" {
		tool = "Tool step"
	}
	if len(input) > 0 {
		b, err := json.Marshal(input)
		if err == nil {
			evidence = string(b)
		}
	}
	return id, tool, evidence, true
}
