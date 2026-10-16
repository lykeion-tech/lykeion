#!/bin/sh
# Stands in for Claude Code: reads its role from the prompt and records what
# that role would, through lyk. The prompt arrives on stdin: in continuity mode
# as one stream-json user frame on the first line, with stdin left open until
# the result; in private mode as plain text up to end of file.
case "$*" in
  *"--input-format stream-json"*) IFS= read -r prompt ;;
  *) prompt="$(cat)" ;;
esac
# Tests that set LYK_TEST_CAPTURE see what the daemon gave this role: the
# skill, the MCP config and the command line, from the scratch folder the CLI
# runs in.
if [ -n "$LYK_TEST_CAPTURE" ]; then
  role=$(printf '%s' "$prompt" | grep -o 'You are the [a-z0-9-]* role' | head -n 1 | sed 's/^You are the //; s/ role$//')
  cp SKILL.md "$LYK_TEST_CAPTURE/$role.skill" 2>/dev/null
  cp .lyk-mcp.json "$LYK_TEST_CAPTURE/$role.mcp" 2>/dev/null
  printf '%s\n' "$@" > "$LYK_TEST_CAPTURE/$role.args"
fi
if [ -n "$LYK_TEST_ARGV" ]; then
  case "$prompt" in
    *"the literature-scout role"*) who=literature-scout ;;
    *"the grounding-reviewer role"*) who=grounding-reviewer ;;
    *) who=other ;;
  esac
  printf '%s %s\n' "$who" "$*" >> "$LYK_TEST_ARGV"
fi
exec 3>&1
exec 1>&2
case "$prompt" in
  *"You are the autoresearch role"*)
    dir="${LYK_TEST_CAPTURE:-$(mktemp -d)}"
    if lyk ground question < /dev/null 2> "$dir/autoresearch.refused"; then echo ALLOWED >> "$dir/autoresearch.refused"; fi
    lyk open-card --title "Z and W" "Does Z lower W in mouse liver?" || exit 1
    printf 'Nothing new this week.\n' | lyk report || exit 1 ;;
  *"the scoper role"*)
    printf '## Scoped question\nDoes X raise Y in mice?\n' | lyk ground question ;;
  *"the literature-scout role"*)
    quote="X raised Y by 40%"
    case "$prompt" in *"round 1."*) [ -n "$LYK_TEST_PARAPHRASE_ROUND1" ] && quote="X increased Y by forty percent" ;; esac
    lyk ground evidence --claim "X raises Y" --source europepmc:PMC1 --quote "$quote" --stance supports ;;
  *"the contrarian role"*)
    lyk ground evidence --claim "X does not raise Y" --source openalex:W2 --quote "no effect of X on Y" --stance challenges ;;
  *"the data-scout role"*)
    if [ -f "$LYK_TEST_NODATA" ]; then
      lyk result nothing-found --reason "nothing measures Y"
    else
      lyk ground dataset --accession GSE1 --note "mouse liver"
    fi ;;
  *"the methods-scout role"*)
    printf '## Known approaches\nDESeq2\n' | lyk ground notes ;;
  *"the grounding-reviewer role"*)
    lyk review verdict --result pass --reason "scope, coverage and fit are adequate" ;;
esac
# A real Claude Code keeps its session where the daemon looks before resuming
# one; without the file the daemon starts the next run fresh.
d="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects/fake"
mkdir -p "$d" && : > "$d/s.jsonl"
# Usage is cumulative per session, as Claude Code reports it, so a resumed
# session is charged only its own turn.
case "$*" in
  *"--resume"*) n=2 ;;
  *) n=1 ;;
esac
echo '{"type":"result","is_error":false,"total_cost_usd":0.0'$n',"session_id":"s","modelUsage":{"m":{"inputTokens":'$n',"outputTokens":'$n',"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"costUSD":0.0'$n'}}}' >&3
