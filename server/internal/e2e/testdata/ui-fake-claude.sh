#!/bin/sh
# A probe never reads prompts, reports work, or creates a resumable session.
case "$1" in
  --version) echo '2.1.69 (Claude Code)'; exit 0 ;;
  --help) echo '  --json-schema <schema>  JSON Schema'; exit 0 ;;
esac
# Naming is an independent, tool-free conversation. Its fixture never invokes
# lyk or writes a research result from that conversation.
previous=''
for option in "$@"; do
  if [ "$previous" = '--tools' ] && [ -z "$option" ]; then
    count=0
    while IFS= read -r prompt; do
      count=$((count+1))
      case "$prompt" in
        *'lyk card show'*'lyk result failed'*) title='1. Inspect the study record\n2. Record the failed run outcome' ;;
        *'lyk result failed'*) title='1. Record the failed run outcome' ;;
        *) title='1. Inspect the study record' ;;
      esac
      printf '{"type":"assistant","message":{"content":[{"type":"text","text":"%s"}]}}\n' "$title"
      printf '{"type":"result","is_error":false,"session_id":"naming-fixture","modelUsage":{"m":{"inputTokens":%s,"outputTokens":%s,"costUSD":0.001}}}\n' "$count" "$count"
    done
    exit 0
  fi
  previous=$option
done
# Stands in for Claude Code in the dashboard's browser-test lab. It reports recorded writing and two
# commands with inspectable results, then records that it failed, so every card it
# works on is escalated at once and reaches the Inbox. The prompt arrives on
# stdin: in continuity mode as one stream-json frame on the first line, in
# private mode as plain text up to end of file.
case "$*" in
  *"--input-format stream-json"*) IFS= read -r prompt ;;
  *) prompt="$(cat)" ;;
esac
# The daemon's preflight hello is answered as a real Claude Code would.
case "$prompt" in
  *'Reply with {'*'ok'*'true}.'*) echo '{"type":"result","is_error":false,"session_id":"hello","structured_output":{"ok":true}}'; exit 0 ;;
  *'You are working in a shared Cowrite paper.'*)
    printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"I can help revise the manuscript. What would you like to work on first?"}]}}'
    echo '{"type":"result","is_error":false}'
    exit 0 ;;
esac
case "$prompt" in
  *"You are the autoresearch role"*)
    # The final answer is the hand-in: its report is the text below as one JSON string.
    report=$({
      printf '# Fresh daemon report\n\n'
      i=1
      while [ "$i" -le 100 ]; do
        printf 'Recorded fixture paragraph %s preserves the full report from real daemon execution through the hand-in.\n\n' "$i"
        i=$((i+1))
      done
      printf '<script>window.reportExecuted = true</script>\n\n![inert image](https://example.com/tracker.png)\n\nend-of-real-daemon-report\n'
    } | awk '{printf "%s\\n", $0}')
    d="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects/$(printf '%s' "$PWD" | sed 's/[^a-zA-Z0-9]/-/g')"
    mkdir -p "$d" && : > "$d/s.jsonl"
    printf '%s\n' '{"type":"result","is_error":false,"structured_output":{"status":"done","answer":"Fresh daemon report","reason":null,"report":"'"$report"'","new_studies":[],"decision":null},"total_cost_usd":0.01,"session_id":"s","modelUsage":{"m":{"inputTokens":1,"outputTokens":1,"costUSD":0.01}}}'
    exit 0 ;;
esac

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
d="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects/$(printf '%s' "$PWD" | sed 's/[^a-zA-Z0-9]/-/g')"
mkdir -p "$d" && : > "$d/s.jsonl"
echo '{"type":"result","is_error":false,"total_cost_usd":0.01,"session_id":"s","modelUsage":{"m":{"inputTokens":1,"outputTokens":1,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"costUSD":0.01}}}'
