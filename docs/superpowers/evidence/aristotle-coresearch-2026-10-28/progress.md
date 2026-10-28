# SDD ledger — plan: docs/superpowers/plans/2026-10-28-aristotle-coresearch.md

Authority: user-approved conversation plan. Existing isolated worktree codex/browser-comments reused. No active AGENTS.md found.

## Preflight
| Tasks | Interface/shared area | Finding |
|---|---|---|
| 1 | Migration/bootstrap/agent API against own tests | Consistent; use stable built-in key rather than display name. |
| 2 | Snapshot/create against own tests | Consistent; preserve ordinary provider defaults. |
| 3 | UI against own tests | Consistent; contextual rail replaces global rail. |
| 4 | Acceptance against own checks | Consistent; sequential heavy verification. |
| 1/2 | DB conversation and agent schema | Task1 report defines schema; Task2 extends queries and execution. Sequential. |
| 1/3 | bootstrap/agent/list API | Task3 consumes Task1 report; workspace/member scoped queries. |
| 2/3 | create/send request IDs and response | Task2 report defines exact contract; UI retains keys on retries. |
| 1/4 | bootstrap/migration acceptance | Fresh and existing lab, duplicate startup required. |
| 2/4 | execution acceptance | Both providers + ordinary defaults. |
| 3/4 | UI/e2e | Existing regression tests may require API fixture updates; preserve assertions. |

Ruling: Bootstrap once per authenticated lab entry via explicit POST, rather than mutating GET — keeps reads side-effect free and retries intentional — costs one startup request.
Ruling: New conversations use a private backing project per conversation — avoids cross-chat file/kernel leakage — adds project rows filtered from ordinary listing.
Ruling: Default persistent shell rail state is per member/lab and browser — matches approved browser persistence — does not sync devices.
Ruling: Reuse current isolated worktree/branch and commit pre-existing corrections separately — protects prior work without moving live frontend — branch name remains browser-comments.

## Tasks
1 pending
2 pending
3 pending
4 pending

Task 1 implementation: 0bedd39, report 4fa500c; affected suites green. Task review changes requested P2 departure/bootstrap orphan-project race. Fix round1 dispatched to original implementer. No Task2 dispatch until gate passes.

Ruling: Apply new conversation rail/FAB shell to Aristotle routes, retaining existing project-detail layout — preserves notebook/file/member controls without double rails — project-detail pages keep current navigation until separately redesigned.
Task1 review race investigation: existing middleware LockWorkspaceForMutation serializes all writes including bootstrap/removal. Deterministic barrier regression passed on unchanged production; retained as evidence for scoped re-review.

Task 1: fix round 1/5 (1 addressed, 0 open — deterministic regression disproved alleged race; existing workspace lock confirmed; commits 4fa500c..9534ded)
Task 1: complete (commits 44b4fc1..9534ded, review clean). Task2 base9534ded.

Task2 implementation1c367ff: all affected packages green; review dispatched. Preview backend built sequentially and refreshed PID36577; private pg_dump backup before-aristotle.sql retained under preview secrets; migration0026/0027 applied by startup; prior JWT/lab data retained.

Task 2: complete (commits 9534ded..1c367ff, review clean). Browser reload confirms prior Local Lab cookie/session retained after API migration. Task3 base1c367ff.

Task 3: fix round 1/5 (3 addressed, 0 open — fetch retry, stale chat response generation, agent save session/busy; commits bfc741c..37daa31)
Task 3: complete (commits 1c367ff..37daa31, review clean). Final npm test482 green; focused UI13 green after fixes; types/lint/format green. Live CUA welcome/draft/customize Settings, Agents presence, dark theme, FAB swap and global New opens study verified. Original Ash theme to restore after remaining checks.
Task4 base37daa31.

Task4 integration RED: after custom starter save, welcome cards become custom list (expected3, actual1). Live CUA independently confirmed. Task4 worker paused edits/runs. Task3 fix round2 dispatched for fixed welcome actions vs configurable draft starters; default local config restored after live save/reflection verification.

Task 3: fix round 2/5 (1 addressed, 0 open — fixed welcome research actions separate from custom draft starters; commits 37daa31..56ca4c9). Scoped review PASS. Task4 resumes at56ca4c9; only test ownership retained.

Task4 full isolated21tests green after explicit bootstrap fixture accounting repairs. Visual inspection found FAB/textarea overlap in closed narrow view; strengthened rectangle test RED63px² for Show navigation. Task4 paused; Task3 fix round3 mobile layout only dispatched.

User clarification: Coresearch and Projects independent. Task5 removes project section/query from conversation rail; keeps separate main navigation feature and legacy direct Coresearch URL compatibility. This overrides original approved plan Projects rail section; no permission needed for explicit user steering. Task4 paused through Task3 mobile review and Task5 implementation.

Task 3: fix round 3/5 (1 addressed, 0 open — closed mobile FAB/text overlap; commits 56ca4c9..7e9c560). Scoped review PASS, desktop CSS unaffected. Task5 explicit user correction base7e9c560 underway.

Task 5: complete (commits 7e9c560..3c16868, review clean). Live CUA confirms accessible rail Conversations/history/+/Settings and no Projects section. Product mainnav untouched. Task4 resumes final acceptance after user correction.

Task 4: complete (commits 3c16868..4ea6812, review clean). Final Playwright21/21 green17.1s incl7Aristotle+14legacy. Root finalnpmtest488 green (309+179), productionbuild+typecheck green with nonblocking Vite chunk-size warning. No backend changes after1c367ff verified binary. Finalreview pending.

Final single fix wave: f340ffc welcome-history error/retry P2 addressed; scoped final re-review PASS, no residual findings.
Final root checks on f340ffc: npmtest489 (309+180), Playwright21/21 (16.1s), productionbuild/types PASS; backend source identical to verified1c367ff. Exact aggregate ESLint/Prettier/gofmt/diff checks passed before final two-file fix; final two-file exact checks passed in fix report. CUA reload actual final code confirmed welcome/conversation-only rail, persisted lab, no connected own machine. No merge/push; retain branch/worktree and live1430/1431 services. All required work complete.
