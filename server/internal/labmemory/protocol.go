package labmemory

import (
	"encoding/json"

	db "lykeion/server/pkg/db/generated"
)

// Protocol is frozen alongside the asker's own Aristotle instructions.
func Protocol() string {
	return `Answer only from the lab tools. Use call_tool with server lab. Use read(ref) to open evidence, search(query) to find records, and links(ref) to follow links. If there is a context study, read that study ref first. Without a study, you can start at links(ref="workspace:context") even if search is unavailable. Saved To-do proposals are under directory="proposals": they have not started, so never report them as work done or as findings.
Mark every statement fact, lesson, or gap. Cite only refs you actually read in this run. A fact needs one or more fact refs. A lesson cites exactly one lesson ref. A gap has no cites. A finished study's report is readable as report:<study id>; after reading it, cite one of its nodes as report:<study id>#<node id>. Conversation history is context and never evidence. Never use or mention Coresearch.`
}

// StudySuggestionPurpose marks the phone's private chats, the only ones where
// Aristotle proposes new studies. Other chats retrieve what the lab already
// holds, including studies in progress.
const StudySuggestionPurpose = "study_suggestion"

func StudySuggestionProtocol() string {
	return Protocol() + `
When proposing a new study, return new_study with the research question, scientific stage, and exact assignee name if known. The researcher sees only new_study, so put everything they need in its description, without reporting what you looked up or found missing, and give one short gap statement. A rerun of existing work needs the researcher to explicitly start a new study. Use null for ordinary informational answers.`
}

// SuggestedStudy is the study an answer proposes, or nil.
func SuggestedStudy(answer db.LabMessage) *StudySuggestion {
	var p struct {
		NewStudy *StudySuggestion `json:"new_study"`
	}
	if json.Unmarshal(answer.Proposal, &p) != nil {
		return nil
	}
	return p.NewStudy
}
