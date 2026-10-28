# Whole-branch final review

Reviewed base `da42e83` through `4ea6812`. Read the approved plan first, including the overriding user clarification that Coresearch is independent of product Projects, then task reports/reviews and the complete supplied branch diff in bounded chunks. No source, index, Git, service or database changes; no tests or agents launched. This report is the only output file.

## Actionable findings

1. **[P2] Show welcome history fetch errors before deciding the conversation is empty** — `apps/dashboard/src/coresearch/AristotleChat.vue:227` (error UI at lines 267–269). The welcome branch uses `conversation?.is_welcome && !messages.data.value?.length`, so an uncached failed messages GET is treated as an empty welcome. The only message error and Retry button are inside the mutually exclusive transcript branch and therefore never render for this failure. Reopen an existing welcome conversation after a reload, let bootstrap/list/profile succeed but its messages request fail: existing history disappears behind the fresh introduction/cards, with no visible failure or retry. The existing `useCoresearchMessages` query only polls when cached data contains an active turn, so this state does not recover through its polling after ordinary query retries are exhausted. Handle message loading/error separately before the empty welcome decision (or place recoverable error UI outside both branches), and add a mounted regression starting with uncached welcome messages, failed GET, then successful explicit retry restoring existing history.

## Reviewed integration conclusions

- Builtin identity is independent of display name; uniqueness, authenticated bootstrap, private owner/workspace conversation queries, archive/visibility restrictions and departure serialization are coherent with the plan. Existing owner/admin agent edit permissions remain explicit rather than silently broadened.
- Each conversation gets distinct classified backing project/chat rows. Ordinary listing excludes them; project membership/ownership mutations and legacy turn creation cannot bypass the conversation snapshot. Owner reads, cancellation and stream identifiers remain compatible.
- First acceptance, configuration snapshot, user message, queued run and exact retry receipt share the scoped transaction. Receipt lookup precedes current runtime/configuration/busy checks; workspace write serialization and database uniqueness cover concurrent acceptance. Failed machine validation rolls back preliminary initialization.
- Frozen instructions reach execution prompts; the classified server claim carries the explicit model through daemon options and both CLI argument builders, while ordinary Coresearch retains provider defaults and existing sandbox/tool options.
- Scoped bootstrap/conversation keys and synchronous editing generations protect current UI state from old requests. Starters retain labels/prompts, retry UUIDs survive same-view failures, and welcome defaults remain separate from configurable draft starters.
- The conversation rail has no Projects content or query. Main navigation `/projects` and direct legacy Coresearch routes remain separate. Narrow dock rules are width-limited; accessible FAB names/pressed state and prior global New/icon/composer corrections remain present.

## Declined to judge / verification limits

- Real paid Claude/Codex execution against installed provider versions and a connected live daemon: unavailable in the reported preview. Fixture command propagation and daemon/backend tests support wiring, not paid-provider acceptance.
- Independent visual or assistive-technology acceptance: this read-only review relies on the reported actual Local Lab CUA checks and fixture Chromium narrow/light/dark checks; it does not claim a fresh visual or screen-reader run.
- A browser-authenticated second-member session was not independently exercised here. Reviewed server authorization/RLS tests and mounted scope-race tests support their narrower claims.

Existing affected backend suite passes, root 488-test pass, production build/typecheck and 21 browser checks are reported evidence, not rerun by this reviewer. The one named outside-diff inspection was `apps/dashboard/src/coresearch/api.ts:107–126`, needed to establish recovery behavior for the finding above.

**Verdict: CHANGES REQUIRED** for the single P2 welcome-history error path. No additional security or integration blockers identified within the reviewed scope.

## Scoped final re-review: 4ea6812b..f340ffc

Original P2: **ADDRESSED**. Quality verdict: **PASS**. The saved-conversation message error and pending branches now precede the empty welcome branch, expose the actual error and explicit refetch action, and prevent missing history from masquerading as a fresh welcome. The composer retains editable draft text while both its Send control and send handler block acceptance until history is available. Unsaved draft rendering and sending bypass this saved-history gate, avoiding interference from the intentionally disabled messages query.

The added regression starts without a message cache, holds GET pending, verifies guarded send, returns GET500, checks the visible error and retained draft, then explicitly retries GET200 and asserts the actual rendered existing history and re-enabled Send. Reported RED/GREEN and focused type/lint/format results were examined as evidence, not independently rerun. No new actionable breakage found in the fix diff.

This re-review read only the supplied fix diff and final-fix-report.md and updates only this report. No broad re-review, tests, agents, source or Git mutations. Final whole-branch review verdict is **PASS**, with the previously stated live-provider and independent acceptance limits retained; root owns the broader final verification.
