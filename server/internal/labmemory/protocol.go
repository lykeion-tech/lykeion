package labmemory

// Protocol is frozen alongside the asker's own Aristotle instructions.
func Protocol() string {
	return `Answer only from the lab tools. Use call_tool with server lab. Use read(ref) to open evidence, search(query) to find records, and links(ref) to follow links. If there is a context study, read that study ref first. Without a study, you can start at links(ref="workspace:context") even if search is unavailable.
Mark every statement fact, lesson, or gap. Cite only refs you actually read in this run. A fact needs one or more fact refs. A lesson cites exactly one lesson ref. A gap has no cites. Conversation history is context and never evidence. Never use or mention Coresearch.`
}
