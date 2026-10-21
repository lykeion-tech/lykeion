#!/bin/sh
# Stands in for Claude Code: reads its role from the prompt and records what
# that role would, through lyk. The prompt arrives on stdin: in continuity mode
# as one stream-json user frame on the first line, with stdin left open until
# the result; in private mode as plain text up to end of file.
case "$*" in
  *"--input-format stream-json"*) IFS= read -r prompt ;;
  *) prompt="$(cat)" ;;
esac
# The role is the first word after "You are the" in the prompt: "autoresearch
# role", or "<role> on Lykeion study" for a study's leader, member or agent.
role=$(printf '%s' "$prompt" | grep -oE 'You are the [a-z0-9_-]+ (role|on Lykeion study)' | head -n 1 | sed 's/^You are the //; s/ role$//; s/ on Lykeion study$//')
# Tests that set LYK_TEST_CAPTURE see what the daemon gave this role: the
# skill, the MCP config and the command line, from the scratch folder the CLI
# runs in.
if [ -n "$LYK_TEST_CAPTURE" ]; then
  cp SKILL.md "$LYK_TEST_CAPTURE/$role.skill" 2>/dev/null
  cp .lyk-mcp.json "$LYK_TEST_CAPTURE/$role.mcp" 2>/dev/null
  printf '%s\n' "$@" > "$LYK_TEST_CAPTURE/$role.args"
fi
exec 3>&1
exec 1>&2
case "$prompt" in
  *"You are the autoresearch role"*)
    dir="${LYK_TEST_CAPTURE:-$(mktemp -d)}"
    if lyk result failed --reason x 2> "$dir/autoresearch.refused"; then echo ALLOWED >> "$dir/autoresearch.refused"; fi
    lyk open-card --title "Z and W" "Does Z lower W in mouse liver?" || exit 1
    printf 'Nothing new this week.\n' | lyk report || exit 1 ;;
esac
# A study's runs follow the Team protocol: the leader names its first member,
# the member writes a note, and the leader, woken with the member's work,
# declares the column done citing it. A single agent writes its note and
# declares the column done itself.
case "$role" in
  ""|autoresearch) ;;
  leader)
    case "$prompt" in
      *"finished the step you named."*|*"run failed:"*)
        key=$(printf '%s' "$prompt" | grep -o -- '- r[0-9][0-9]* (' | head -n 1 | sed 's/^- //; s/ ($//')
        printf 'The members did the column.\n' | lyk team done --content-stdin ${key:+--cites "$key"} || exit 1 ;;
      *)
        member=$(sed -n '/^Your members:/{n;s/^- \([^:]*\):.*/\1/p;}' SKILL.md)
        lyk team step "$member" --content "Do your part of the column." || exit 1 ;;
    esac ;;
  agent)
    key=$(printf '{"text":"The column note."}' | lyk record put note --content-stdin) || exit 1
    printf 'The column note is written.\n' | lyk team done --content-stdin --cites "$key" || exit 1 ;;
  *)
    printf '{"text":"The %s note."}' "$role" | lyk record put note --content-stdin || exit 1 ;;
esac
# A real Claude Code keeps its session where the daemon looks before resuming
# one; without the file the daemon starts the next run fresh.
d="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects/$(printf '%s' "$PWD" | sed 's/[^a-zA-Z0-9]/-/g')"
mkdir -p "$d" && : > "$d/s.jsonl"
# Usage is cumulative per session, as Claude Code reports it, so a resumed
# session is charged only its own turn.
case "$*" in
  *"--resume"*) n=2 ;;
  *) n=1 ;;
esac
echo '{"type":"result","is_error":false,"total_cost_usd":0.0'$n',"session_id":"s","modelUsage":{"m":{"inputTokens":'$n',"outputTokens":'$n',"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"costUSD":0.0'$n'}}}' >&3
