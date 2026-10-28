# Task 3: Aristotle Vue experience

Implemented in this worktree on backend base `1c367ff`. No merge/push, backend changes, service restarts, e2e changes, or browser automation performed by this worker. Existing six UI corrections retained. Parent performed live visual review; requested rail portraits, circular send button, and no duplicate draft header were incorporated.

## Routes and components

- `/coresearch`: restore last valid member/lab conversation, otherwise welcome, replacing URL with `/coresearch/conversations/:conversationId`.
- `/coresearch/new`: unsaved Aristotle draft. No project/chat/conversation POST until first accepted send.
- `/coresearch/conversations/:conversationId`: saved welcome or conversation. Missing/stale ID falls back to current member/lab welcome.
- `/coresearch/projects`: existing project management list (rename/archive retained).
- Existing `/coresearch/:projectId/:chatId?` retained with original ProjectSidebar and project layout. New routes are declared before it.
- `/agents/:id?tab=settings`: query respected initially, after query-only navigation, and after lab/id changes. Real general profile and description remain editable. Builtin starter label/prompt editor saves alongside profile. Empty list previews product defaults. Builtin archive unavailable, visibility inputs disabled. Ordinary agent controls retained.

`AristotleChat.vue` owns page/acceptance flow; `AristotleComposer.vue` owns presentational composer; `ConversationRail.vue` owns history/projects; `RailFabs.vue` ports L2 controls with attribution to existing L2-LICENSE. `aristotle.ts` owns scoped bootstrap/list queries and request/restore helpers. App calls bootstrap on authenticated lab startup, including Agents-first navigation. Bootstrap success invalidates Agents list/detail and fetches current profile rather than overwriting customization with a stale bootstrap response.

App uses one sidebar slot. Only Aristotle routes receive conversation rail/FABs; nav/context persisted independently per member/lab. Narrow viewport uses contextual overlay plus existing WorkspaceTabs collapse toggle and persistent FABs. Legacy project routes do not receive new contextual outer rail.

## Acceptance and isolation

All sends use exact `{body, request_id}`; UUID retained for identical retry, changed body/target allocates new UUID. Errors keep draft. Existing conversation snapshot determines machine blocker, rather than edited live harness. Starters send their saved prompt. Missing machine leaves draft/history/settings usable and exposes Settings machines link.

201 response `{conversation,run}` IDs drive actual current message polling/transcript/steps/cancel URLs via existing components; receipt status is never treated as current run state. Full accepted IDs are retained in local storage under the original scope even if a lab switch occurs in flight. Old-scope success never navigates or mutates current lab draft. Conversation preview list invalidates on Coresearch/member events. New query keys are member/lab scoped, abortable; no prior-scope placeholder data.

Storage keys:
- `lykeion.aristotle.last:<member>:<lab>`
- `lykeion.aristotle.rail:<member>:<lab>`
- `lykeion.aristotle.accepted:<member>:<lab>` JSON `{conversation,project,chat,run}` for retained acceptance receipt IDs.

## Hooks/selectors for Task 4

- `[data-testid="aristotle-chat"]`, `[data-testid="rail-fabs"]`.
- `aria-label="New conversation"`, `Show navigation`, `Show conversations` (FAB aria-pressed).
- `aria-label="Message Aristotle"`, `Send`, `Stop`.
- `Customize starters` link; `Agent name`, `Agent description`, `Starter 1 label`, `Starter 1 prompt`; `Add starter`, `Remove starter 1`, `Save changes`.
- Rail accessible name `Conversations and projects`; navigation rail `Workspace navigation`; Projects header routes management list.

## Verification

- RED before production: `npx vitest run tests/aristotle.test.ts --config apps/dashboard/vite.config.ts --root . --maxWorkers=1` failed because imported Aristotle helper module did not exist. GREEN after implementation: initial 3 tests passed; final 4 helper/event tests passed.
- Initial mounted UI run: 7 tests, 4 failed due memory-history href expectation and native textarea/add-button event/whitespace harness assumptions. Corrected harness, no weakening of behavior assertions. Expanded final mounted suite 9 passed covering unsaved draft/no duplicate header, prompt-vs-label sends, request retry/edit UUID, machine blocker/settings access, stale restoration, frozen snapshot, settings save, rail aria state, and in-flight lab-switch acceptance preservation.
- Full `npm test` initial sandbox run: desktop redirect test could not bind localhost (`listen EPERM`), 304 passed/1 timeout; rerun unchanged with local-network escalation passed existing 469 tests.
- Added both new files to enumerated package test script. Final full `npm test` with escalation PASS: first group 309 tests/16 files; custom-client group 173 tests/11 files; total 482 tests/27 files. Run times4.96s and2.10s.
- Conversation realtime invalidation initially made existing exact-key regression fail; updated fixture to require the new key in addition to existing project keys and cross-workspace exclusion. Final full custom-client group passed173.
- `npm run typecheck`: PASS.
- Exact ESLint of all changed TS/Vue/tests: PASS.
- Exact Prettier check of all changed source/tests/package: PASS.
- `git diff --check`: PASS.
- Final draft-header-only correction followed by focused mounted UI suite:9 passed; no broad rerun needed.

## Task 4 boundaries

Requested Desktop screenshots were absent from filesystem, so neither could be opened; parent supplied detailed geometry and inspected live UI against inline references. No worker screenshots produced. CSS follows supplied max896 content/composer, 384 starter stack, 49px saved-chat header,216px three-card row,26px circular send. Existing Aristotle component now uses existing face-only portrait JSON in portrait mode. Parent live visual check remains authoritative; verify light/dark/narrow, overlay/collapse interactions, reload/member/lab restoration and ordinary project/global New in Playwright. No paid provider invocation was performed. Runtime freshness/bound-daemon acceptance remains enforced by backend; frontend availability hint uses existing own-online runtime filter and displays backend rejection on stale/unavailable acceptance.

## Commit

`bfc741c` — `feat(coresearch): add personal Aristotle chat experience` (18 explicitly staged frontend/test/package files). Report remains ignored, not force-added. No merge/push. Post-commit tracked/untracked tree clean.

## Review fix round 1

Addressed exactly the three P2 findings in task-3-review.md:

1. Live agent-profile GET errors render their actual error plus Try again/refetch before loading state. Regression starts from successful cached bootstrap, missing live agent cache, GET500; retry GET200 restores draft UI.
2. Chat local mutation requires a captured monotonic generation, incremented synchronously on scope/path changes, request start and component unmount. Receipt/last-pointer persistence remains under original member/lab. Regression tests cover `/coresearch/new` -> welcome -> draft component reuse and draft -> Agents -> draft actual unmount/remount. Both start a newer send before the old response, asserting no old navigation, draft clearing or newer busy clearing; original accepted IDs still persist.
3. AgentDetail edit generation increments on id/lab/member changes, new save and unmount. New editing sessions reset busy immediately. Save success/error/finally/notification require matching generation. Regression covers pending save A -> B (B enabled) -> A/new save; completing old A cannot clear newer A busy, newer completion clears it.

RED: focused client suite on unchanged production yielded 4 failures/9 passes: profile rendered `Loading Aristotle…`, both away/back sends navigated to `/coresearch/conversations/accepted-old`, and B Save changes remained disabled. An earlier broad GET fixture returned conversation rows to message reads and caused unrelated render errors; corrected URL-specific fixtures, then captured the clean four behavioral failures before production changes.

GREEN: `npx vitest run tests/aristotle-ui.test.ts --environment ./tests/client-environment.ts --config apps/dashboard/vite.config.ts --root . --maxWorkers=1` passed all13 tests (final0.657s). `npm run typecheck`, exact ESLint/Prettier of the three changed files, and `git diff --check` passed. No full-suite repetition, backend/service/e2e changes, merge or push in this fix round.

Fix commit `37daa31` — `fix(coresearch): guard stale chat and agent save responses` (three explicit files). Post-commit tracked/untracked tree clean; ignored report retained.

## Integration fix round 2

Live acceptance found that a single customized starter incorrectly replaced the three required welcome research cards. Welcome now iterates fixed product research defaults; draft buttons continue to use the live agent's ordered custom starters (or defaults when empty). No interface changes.

RED: added mounted regression with a live agent containing one custom starter, then ran focused UI suite on unchanged production. Failed expected3 welcome cards, received1 (13 passed/1 failed). GREEN after one-line welcome iterator fix:14 UI tests passed (0.689s). Regression verifies all three fixed labels, absence of custom action on welcome, exact fixed research prompt submitted on welcome card click, then route to draft shows only the custom starter. Exact ESLint/Prettier for the two changed files and `git diff --check` passed. No type/interface changes requiring typecheck; no broad suites repeated. Acceptance worker's uncommitted e2e file left untouched/unstaged.

Fix commit `56ca4c9` — two explicit source/unit files only. Existing acceptance-owned config/e2e changes remain uncommitted and untouched. No merge/push.

## Integration fix round 3: narrow footer dock

Parent/acceptance recorded clean Playwright RED: closed narrow Show navigation FAB overlapped textarea by63 square pixels. Mobile shell and contextual sidebar now end above shared64px footer dock occupied by FAB row. Composer and machine blocker remain in normal flow above it; content-sized mobile textarea grows up to200px. All rules max-width700px, preserving desktop geometry.

GREEN: `PLAYWRIGHT_BROWSERS_PATH=/tmp/coresearch-task10-browser LYK_UI_RENDERER_PORT=1457 ./node_modules/.bin/playwright test --config playwright.coresearch.config.ts tests/e2e/aristotle.spec.ts --grep 'ash|midnight'` with local-server escalation:2/2 passed in3.7s, including zero FAB/textarea intersection for one/four lines, hit-testing, machine notice, no horizontal overflow. Screenshots `/tmp/aristotle-ash-narrow-closed.png` and `/tmp/aristotle-midnight-narrow-closed.png`. Worker and parent viewed updated ash; parent confirms composer/first text line unobscured, FABs in separate footer, Send visible.

Exact ESLint/Prettier on App.vue, RailFabs.vue, AristotleComposer.vue and `git diff --check` passed. No interface changes or broad reruns. Acceptance-owned config/e2e untouched/unstaged.

Fix commit `7e9c560` — productionCSS/component files only (3 files). All remaining working-tree changes belong to acceptance. No merge/push.
