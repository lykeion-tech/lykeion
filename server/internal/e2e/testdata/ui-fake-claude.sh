#!/bin/sh
# A probe never reads prompts, reports work, or creates a resumable session.
case "$1" in
  --version) echo '2.1.0 (Claude Code)'; exit 0 ;;
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
        *'execute_python_cell'*'execute_shell_cell'*) title='1. Print transcript check\n2. Print second step line\n3. Inspect the study record\n4. Record the failed run outcome' ;;
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
case "$prompt" in
  *"You are the autoresearch role"*)
    {
      printf '# Fresh daemon report\n\n'
      i=1
      while [ "$i" -le 100 ]; do
        printf 'Recorded fixture paragraph %s preserves the full report from real daemon execution through the report command.\n\n' "$i"
        i=$((i+1))
      done
      printf '<script>window.reportExecuted = true</script>\n\n![inert image](https://example.com/tracker.png)\n\nend-of-real-daemon-report\n'
    } | lyk report >/dev/null || exit 1
    d="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects/$(printf '%s' "$PWD" | sed 's/[^a-zA-Z0-9]/-/g')"
    mkdir -p "$d" && : > "$d/s.jsonl"
    echo '{"type":"result","is_error":false,"total_cost_usd":0.01,"session_id":"s","modelUsage":{"m":{"inputTokens":1,"outputTokens":1,"costUSD":0.01}}}'
    exit 0 ;;
esac

# The browser tests' demo Team names its members demo-*. Its leader names its
# first member, the member writes a note, and the leader, woken with the
# member's work, declares the column done citing it.
role=$(printf '%s' "$prompt" | grep -oE 'You are the [a-z0-9_-]+ on Lykeion study' | head -n 1 | sed 's/^You are the //; s/ on Lykeion study$//')
member=$(sed -n '/^Your members:/{n;s/^- \([^:]*\):.*/\1/p;}' SKILL.md 2>/dev/null)
case "$role:$member" in leader:demo-*|demo-*:)
  case "$role" in
    leader)
      case "$prompt" in
        *"finished the step you named."*)
          key=$(printf '%s' "$prompt" | grep -o -- '- r[0-9][0-9]* (' | head -n 1 | sed 's/^- //; s/ ($//')
          printf 'The note is there.\n' | lyk team done --content-stdin ${key:+--cites "$key"} >/dev/null || exit 1 ;;
        *)
          lyk team step "$member" --content "Write the column's note." >/dev/null || exit 1 ;;
      esac ;;
    *)
      printf '{"text":"The %s note."}' "$role" | lyk record put note --content-stdin >/dev/null || exit 1 ;;
  esac
  d="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects/$(printf '%s' "$PWD" | sed 's/[^a-zA-Z0-9]/-/g')"
  mkdir -p "$d" && : > "$d/s.jsonl"
  echo '{"type":"result","is_error":false,"total_cost_usd":0.01,"session_id":"s","modelUsage":{"m":{"inputTokens":1,"outputTokens":1,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"costUSD":0.01}}}'
  exit 0 ;;
esac
case "$prompt" in
  *"Notebook cell transcript fixture"*)
    printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"nb1","name":"mcp__notebook__execute_python_cell","input":{"code":"print(\"transcript UI check\")\nprint(42)","human_description":"Print transcript check","execution_count":3}}]}}'
    printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_result","tool_use_id":"nb1","content":"transcript UI check\n42","is_error":false}]}}'
    printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"nb2","name":"mcp__notebook__execute_shell_cell","input":{"code":"printf \"second step\\n\"","human_description":"Print second step line"}}]}}'
    printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_result","tool_use_id":"nb2","content":"second step","is_error":false}]}}'
    printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"Both recorded cells printed their lines."}]}}'
    ;;
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
