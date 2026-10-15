#!/bin/sh
# A probe never reads prompts, reports work, or creates a resumable session.
case "$1" in
  --version) echo '2.1.0 (Claude Code)'; exit 0 ;;
esac
# Stands in for Claude Code in the dashboard's browser-test lab. It reports two
# commands and a line of text, then records that it failed, so every card it
# works on is escalated at once and reaches the Inbox. The prompt arrives on
# stdin: in continuity mode as one stream-json frame on the first line, in
# private mode as plain text up to end of file.
case "$*" in
  *"--input-format stream-json"*) IFS= read -r prompt ;;
  *) prompt="$(cat)" ;;
esac
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"lyk card show"}}]}}'
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"lyk result failed"}}]}}'
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"Read the card; the lab script stops here."}]}}'
lyk result failed --reason "the lab script stops here" >/dev/null
# The daemon looks for the session before resuming one.
d="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects/fake"
mkdir -p "$d" && : > "$d/s.jsonl"
echo '{"type":"result","is_error":false,"total_cost_usd":0.01,"session_id":"s","modelUsage":{"m":{"inputTokens":1,"outputTokens":1,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"costUSD":0.01}}}'
