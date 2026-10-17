#!/bin/sh
# A probe never reads prompts, reports work, or creates a resumable session.
case "$1" in
  --version) echo '2.1.0 (Claude Code)'; exit 0 ;;
esac
# Stands in for Claude Code in the dashboard's browser-test lab. It reports recorded writing and two
# commands with inspectable results, then records that it failed, so every card it
# works on is escalated at once and reaches the Inbox. The prompt arrives on
# stdin: in continuity mode as one stream-json frame on the first line, in
# private mode as plain text up to end of file.
case "$*" in
  *"--input-format stream-json"*) IFS= read -r prompt ;;
  *) prompt="$(cat)" ;;
esac
# The browser tests' demo Team names its roles demo-<column>-author and
# demo-<column>-reviewer. Each author writes its column's note and each
# reviewer passes it, so a study on that Team moves on until one of the Team's
# own checks fails it.
demo=$(printf '%s' "$prompt" | grep -o 'You are the demo-[a-z]*-[a-z]* role' | head -n 1 | sed 's/^You are the //; s/ role$//')
if [ -n "$demo" ]; then
  case "$demo" in
    *-author)
      col=${demo#demo-}; col=${col%-author}
      printf 'The %s note.\n' "$col" | lyk ground notes >/dev/null || exit 1 ;;
    *-reviewer)
      lyk review verdict --result pass --reason "the note is there" >/dev/null || exit 1 ;;
  esac
  d="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects/fake"
  mkdir -p "$d" && : > "$d/s.jsonl"
  echo '{"type":"result","is_error":false,"total_cost_usd":0.01,"session_id":"s","modelUsage":{"m":{"inputTokens":1,"outputTokens":1,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"costUSD":0.01}}}'
  exit 0
fi
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"## Recorded observations\n\nThe lab reads **recorded evidence** before deciding what follows.\n\n- Inspect the current study\n- Keep source identity intact\n\nSee [the documentation](https://example.com/research) and literal citation [1]."}]}}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"lyk card show"}}]}}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Recorded lab reasoning remains collapsed by default."}]}}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"The scripted card read was declined. No evidence was created.","is_error":true}]}}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"### What the tool recorded\n\n| Recorded input | Outcome |\n| --- | --- |\n| Current card | Read declined |\n\n> This is scripted browser-test activity.\n\n```python\nprint(\"long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value long recorded value \")\n```\n\n<script>window.transcriptUnsafe = true</script>\n\n![inert test image](https://example.com/tracker.png)"}]}}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"lyk result failed"}}]}}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"The scripted run will stop with the existing lab failure reason.","is_error":false}]}}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"Read the card; the lab script stops here."}]}}'
lyk result failed --reason "the lab script stops here" >/dev/null
# The daemon looks for the session before resuming one.
d="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects/fake"
mkdir -p "$d" && : > "$d/s.jsonl"
echo '{"type":"result","is_error":false,"total_cost_usd":0.01,"session_id":"s","modelUsage":{"m":{"inputTokens":1,"outputTokens":1,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"costUSD":0.01}}}'
