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
# Metadata scenarios exercise the actual hosted MCP transport under this run's
# credential. Only the external upstream and provider are synthetic.
if [ -n "$LYK_TEST_METADATA" ]; then
  set -e
  source_call() {
    LYK_TEST_MCP_CALL="$1" LYK_TEST_MCP_ARGS="$2" "$LYK_TEST_MCP_HELPER" -test.run '^TestMetadataMCPHelper$' -test.v
  }
  case "$prompt" in
    *"the literature-scout role"*)
      case "$LYK_TEST_METADATA" in
        refused-command|quota-failure)
          printf '{"type":"result","is_error":true,"result":"%s: metadata command unavailable"}\n' "$LYK_TEST_METADATA" >&3
          exit 1 ;;
      esac
      source_call search_literature '{"database":"europepmc","query":"X Y comparison"}'
      source_call fetch_text '{"source_id":"europepmc:PMC1"}'
      if [ -f "$LYK_TEST_METADATA_REPAIR" ] || [ "$LYK_TEST_METADATA" = unresolved ]; then
        source_call fetch_text '{"source_id":"geo:GSE1"}' > metadata-response
        if grep -q 'Each mouse was collected at the same time after treatment' metadata-response; then
          lyk ground evidence --claim "Each mouse was collected at the same time after treatment" --source geo:GSE1 --quote "Each mouse was collected at the same time after treatment" --stance supports > "$LYK_TEST_HYP_OUTPUT/metadata-evidence"
        fi
      fi ;;
    *"the contrarian role"*)
      source_call search_literature '{"database":"openalex","query":"X no effect Y"}'
      source_call fetch_text '{"source_id":"openalex:W2"}' ;;
    *"the data-scout role"*)
      source_call search_datasets '{"query":"X Y mice"}'
      source_call open_dataset '{"accession":"GSE1"}'
      source_call fetch_text '{"source_id":"geo:GSE1"}' ;;
  esac
fi
case "$prompt" in
  *"You are the autoresearch role"*)
    dir="${LYK_TEST_CAPTURE:-$(mktemp -d)}"
    if lyk result failed --reason x 2> "$dir/autoresearch.refused"; then echo ALLOWED >> "$dir/autoresearch.refused"; fi
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
    if [ -n "$LYK_TEST_METADATA" ] && ! lyk records | grep '"kind":"evidence"' | grep -q '"source_id":"geo:GSE1"'; then
      : > "$LYK_TEST_METADATA_REPAIR"
      lyk review verdict --result fail --step literature-scout --reason "comparison prerequisite: collection timing remains unknown in recorded E; inspect metadata and establish comparable collection times"
    else
      lyk review verdict --result pass --reason "scope, coverage and fit are adequate"
    fi ;;
esac
# The demo pack's roles are demo-<column>-author and demo-<column>-reviewer.
# Each author writes its column's note; where LYK_TEST_SKIP_NOTE names the
# column it reports nothing found instead, which the column's check fails.
# Each reviewer passes.
demo=$(printf '%s' "$prompt" | grep -o 'You are the demo-[a-z]*-[a-z]* role' | head -n 1 | sed 's/^You are the //; s/ role$//')
case "$demo" in
  demo-*-author)
    col=${demo#demo-}; col=${col%-author}
    if [ "$LYK_TEST_SKIP_NOTE" = "$col" ]; then
      lyk result nothing-found --reason "no $col note this time" || exit 1
    else
      printf 'The %s note.\n' "$col" | lyk ground notes || exit 1
    fi ;;
  demo-*-reviewer)
    lyk review verdict --result pass --reason "the note is there" || exit 1 ;;
esac
# Hypotheses roles act only in tests that ask for them; other tests leave the
# card waiting in Hypotheses.
if [ -n "$LYK_TEST_HYPOTHESES" ]; then
  sup=$(lyk records --column grounding | sed -n 's/.*"key":"\(E[0-9]*\)".*"stance":"supports".*/\1/p' | tail -n 1)
  survivors=$(lyk records | grep '"kind":"dedup"' | grep -o '"survivors":\[[^]]*\]' | grep -o 'H[0-9]*' || true)
  mech='{"statement":"X raises Y by inducing Z","kind":"mechanism","mechanism":[{"text":"X induces Z","claims":["'$sup'"]}],"assumptions":[{"text":"Z is expressed in mouse liver","claims":["'$sup'"]}],"boundary_conditions":["mouse liver"],"uncertainties":["Z may only mark the effect"]}'
  if [ -n "$LYK_TEST_METADATA" ]; then
    meta=$(lyk records --column grounding | grep '"kind":"evidence"' | grep '"source_id":"geo:GSE1"' | sed -n 's/.*"key":"\(E[0-9]*\)".*/\1/p' | tail -n 1)
    premise="Each mouse was collected at the same time after treatment"
    mech='{"statement":"X raises Y by inducing Z","kind":"mechanism","mechanism":[{"text":"X induces Z","claims":["'$sup'"]}],"assumptions":[{"text":"'$premise'","claims":["'$meta'"]}],"boundary_conditions":["mouse liver"],"uncertainties":["Z may only mark the effect"]}'
  fi
  case "$prompt" in
    *"the generator-1 role"*)
      if [ -f "$LYK_TEST_MISSING_PREMISE" ]; then
        rm -f "$LYK_TEST_MISSING_PREMISE"
        lyk hyp missing-premise --gap "the dose at which X saturates Y"
      else
        if [ -n "$LYK_TEST_HYP_OUTPUT" ]; then
          printf '%s' "$mech" | lyk hyp candidate > "$LYK_TEST_HYP_OUTPUT/candidate" || exit 1
        else
          printf '%s' "$mech" | lyk hyp candidate
        fi
      fi ;;
    *"the generator-2 role"*)
      printf '{"statement":"A batch artefact in the Y assay mimics an effect of X","kind":"artefact","mechanism":[{"text":"treated and control samples ran in different assay batches","claims":["%s"]}],"assumptions":[{"text":"the Y assay drifts between batches","claims":["%s"]}],"boundary_conditions":[],"uncertainties":[]}' "$sup" "$sup" | lyk hyp candidate ;;
    *"the generator-3 role"*)
      printf '%s' "$mech" | lyk hyp candidate ;;
    *"the decomposer role"*)
      for h in $survivors; do
        assumption="Y is measured once per mouse"
        assumption_key="$sup"
        if [ -n "$LYK_TEST_METADATA" ]; then assumption="$premise"; assumption_key="$meta"; fi
        printf '{"candidate":"%s","assumptions":[{"id":"A1","text":"%s","claims":["%s"]}],"predictions":[{"id":"P1","text":"Y differs between treated and control mice"}],"tests":[{"id":"T1","targets":"P1","null":"Y does not differ between groups","alternative":"Y differs between groups","procedure":"compare Y between groups in GSE1, one mouse per observation"}]}' "$h" "$assumption" "$assumption_key" | lyk hyp decompose
      done ;;
    *"the novelty-scout role"*)
      if [ -n "$LYK_TEST_METADATA" ]; then
        source_call search_literature '{"database":"openalex","query":"X raises Y prior work"}'
      fi
      for h in $survivors; do
        lyk hyp novelty --candidate "$h" --status unknown --query "X raises Y prior work"
      done ;;
    *"the selector role"*)
      if [ -n "$LYK_TEST_HYP_OUTPUT" ]; then
        lyk hyp rank > "$LYK_TEST_HYP_OUTPUT/ranking" || exit 1
      else
        lyk hyp rank
      fi
      a=$(printf '%s\n' $survivors | sed -n 1p)
      b=$(printf '%s\n' $survivors | sed -n 2p)
      if [ -n "$LYK_TEST_METADATA" ]; then
        exec 4> "$LYK_TEST_HYP_OUTPUT/matrix"
      else
        exec 4>&1
      fi
      lyk hyp matrix >&4 <<EOF
{"measurement":{"id":"M1","dataset":"D1","description":"log2 fold change of Y, treated against control, one mouse per observation",
 "keys":[{"name":"effect","description":"log2 fold change","unit":"log2FC"},{"name":"ci_low","description":"lower bound of the 95 percent interval","unit":"log2FC"},{"name":"ci_high","description":"upper bound of the 95 percent interval","unit":"log2FC"}],
 "ordered":["ci_low","effect","ci_high"]},
 "rivals":[
  {"id":"R0","kind":"null","statement":"X does not change Y","measurement":"M1","prediction":"ci_low >= -0.2 and ci_high <= 0.2","equivalence":{"key":"effect","low":-0.2,"high":0.2},"falsifier":"ci_low > 0.2 or ci_high < -0.2","indeterminate":"(ci_low < -0.2 and ci_high >= -0.2) or (ci_high > 0.2 and ci_low <= 0.2)"},
  {"id":"R1","kind":"mechanism","candidate":"$a","statement":"the first survivor","measurement":"M1","prediction":"ci_low > 0.2","equivalence":{"key":"effect","low":-0.2,"high":0.2},"falsifier":"ci_high < 0.2","indeterminate":"ci_low <= 0.2 and ci_high >= 0.2"},
  {"id":"R2","kind":"artefact","candidate":"$b","statement":"the second survivor","measurement":"M1","prediction":"ci_high < -0.2","equivalence":{"key":"effect","low":-0.2,"high":0.2},"falsifier":"ci_low > -0.2","indeterminate":"ci_low <= -0.2 and ci_high >= -0.2"}],
 "rationale":"the null, a mechanism and an artefact predict disjoint intervals"}
EOF
      ;;
    *"the hypotheses-reviewer role"*)
      lyk review verdict --result pass --reason "honest rivals; the measurement fits the dataset and its unit" ;;
  esac
fi
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
