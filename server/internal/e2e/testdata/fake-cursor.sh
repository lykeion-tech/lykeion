#!/bin/sh
# Stands in for cursor-agent: the prompt arrives on stdin. fake-claude.sh does
# the role's work; this script speaks Cursor's stream-json and, like
# cursor-agent, may linger after its result.
case "$1" in
  --version) echo 'cursor-agent test'; exit 0 ;;
esac
prompt=$(cat)
printf '%s' "$prompt" | "$(dirname "$0")/fake-claude.sh" -p >/dev/null
echo '{"type":"system","subtype":"init","session_id":"c1","model":"GPT-5"}'
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"c1","usage":{"inputTokens":1000,"outputTokens":100}}'
sleep 300
