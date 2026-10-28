# Task 4: automated browser acceptance

Owned tests/fixtures/Playwright config only. No production edits, backend migrations/restarts, paid provider invocation, merge or push. Existing API1430 and Vite1431 untouched. Isolated fixture Vite used1457 and exited after tests. Final production bases include welcome fix56ca4c9, mobile fix7e9c560 and independent rail3c16868.

## Coverage

Seven new real Chromium/Playwright scenarios exercise production Vue/router/API-client behavior against explicit HTTP fixtures: first bootstrap welcome with rendered portrait and three research cards, both ash/midnight; unsaved plus draft with zero conversation/domain writes; no own runtime blocker and retained draft/settings link; Agents-first discovery; Settings deep link and PATCH saved starter reflection while welcome retains its fixed three research actions; accepted receipt IDs/storage/navigation; actual current transcript messages and Stop URL using current-run rather than stale accepted-run; valid last-chat home restoration/reload; failed transport exact UUID reuse and edited payload new UUID; server-accepted response loss replay with one fixture acceptance receipt; stale and foreign member/lab storage pointers excluded; exclusive nav/conversation rails and persistence; no Projects/Manage projects in chat rail, independent main navigation Projects -> /projects; legacy direct project management/detail compatibility; narrow sidebar open/closed, accessible labels, input focus/typing and no horizontal overflow. Both FAB rectangles must have zero intersection with textarea for single and four-line drafts.

Updated legacy final tests preserve global New -> study composer/no hint and focus/rename assertions at new routes. Explicit bootstrap endpoint handling in legacy fixtures preserves original file/compute domain writes arrays and every existing assertion. Markdown/notebook/files/final regression suites remain in config.

## Evidence and verification

- First execution sandbox denied local1457 bind (EPERM). Same command with approved local-network escalation ran successfully. All browser commands used `PLAYWRIGHT_BROWSERS_PATH=/tmp/coresearch-task10-browser LYK_UI_RENDERER_PORT=1457`.
- Initial targeted10:7passed/3failed because two portrait selectors and Agents exact-text selector were inaccurate. Fixed selectors to real rendered SVG/table cell. Next6:5passed/1failed from navigating before asynchronous save completed; added actual `Agent saved.` wait.
- Requested welcome regression RED: `npx playwright test --config playwright.coresearch.config.ts tests/e2e/aristotle.spec.ts -g 'starter settings'` failed1: expected3 welcome cards received1 after saved custom starter. Paused; production owner fixed56ca4c9. Strengthened7-scenario suite then passed7 in6.7s.
- First full isolated config:16passed/5failed26.6s; all failures were new startup bootstrap recorded as domain writes (files4/notebook owner1). Added explicit bootstrap fixture handling before domain writes, unchanged assertions. Full config then passed21 in15.9s.
- Visual review requested stronger FAB overlap regression. Targeted ash RED: expected0 intersection received63px². Paused; production owner fixed7e9c560. Tests include both FABs, single/four-line input.
- Final after independent rail steering: `npx playwright test --config playwright.coresearch.config.ts` PASS21/21 in17.1s. Includes new7 + legacy files7 + final4 + markdown1 + notebook2. Nonfatal WebSocket proxy ECONNREFUSED8080 appeared in legacy markdown/notebook HTTP-only fixtures; production HTTP assertions and browser error assertions passed.
- `npx eslint tests/e2e/aristotle-fixture.ts tests/e2e/aristotle.spec.ts tests/e2e/coresearch-files-fixture.ts tests/e2e/coresearch-final.spec.ts tests/e2e/coresearch-notebook.spec.ts tests/e2e/coresearch-markdown.spec.ts playwright.coresearch.config.ts`: PASS.
- `npx prettier --check` with same seven paths: PASS.
- `git diff --check`: PASS. Parent's unrelated plan edit preserved and excluded from commit.

## Screenshots

Generated final fixture evidence:
- `/tmp/aristotle-ash-desktop.png`, `/tmp/aristotle-midnight-desktop.png`
- `/tmp/aristotle-ash-narrow-open.png`, `/tmp/aristotle-midnight-narrow-open.png`
- `/tmp/aristotle-ash-narrow-closed.png`, `/tmp/aristotle-midnight-narrow-closed.png`
- `/tmp/coresearch-final-focus-ash.png`, `/tmp/coresearch-final-focus-midnight.png`

Screenshots are fixture UI, not actual Local Lab/provider evidence. Root owns actual CUA/live lab verification. Member/lab scope browser test proves foreign storage pointers excluded; mounted/UI/backend tests cover in-flight scope races and authoritative server isolation. No browser login-as-second-member, real online daemon acceptance, provider credentials or paid run was performed by this worker. Lost-response single acceptance is an explicit test-only server model; backend idempotency remains separately verified by backend tests. No Go or broad frontend suite rerun in this tests-only subtask; parent owns final branch verification boundaries.

Commit: `4ea6812` — `test(coresearch): cover Aristotle browser acceptance`, seven explicitly staged test/config files. Git index write initially sandbox-denied; same exact authorized paths committed with approved Git metadata escalation. Parent-owned plan edit was excluded and parent committed it concurrently; post-commit `git status --short` is clean. No task-owned changes left unstaged. No merge/push.
