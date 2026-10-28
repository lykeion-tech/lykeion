# Aristotle Coresearch verification

Implemented and reviewed on `codex/browser-comments`; application source head `f340ffc`. No merge or push. The separate earlier UI corrections remain in commit `1e4ee7d`.

Coresearch and product Projects are independent. The conversation rail contains conversations, +, and Settings; no Projects section or project-list query. Product Projects retains its own main-navigation page. Internal per-chat execution records do not appear in product Projects.

## Final checks

- Frontend: **489 tests passed** (309 + 180).
- Chromium browser: **21 tests passed**, including startup, custom starters, accepted retry receipts, current transcript/cancel IDs, last-chat restoration, rail switching, independent Projects, and no mobile FAB/input overlap for one/four-line drafts.
- Production dashboard/desktop build and typechecking passed. Exact changed-file ESLint, Prettier, Go formatting and diff checks passed.
- Affected backend packages passed: handler, migrations, Coresearch, service, daemon, provider adapters, team and flow. Backend source is unchanged from tested/built `1c367ff`.
- Task reviews and whole-branch review passed after retained RED/GREEN fixes. See task reports, final review and logs in this directory.

## Actual Local Lab verification

The real preview on1431/API1430 retained its lab/session after a private database backup and additive0026/0027 migration. Actual CUA browser checks verified welcome, unsaved draft, portrait, auto-created personal Aristotle in Agents, Settings deep link, real starter save/reflection/restoration, FAB switching, global New study composer/no hint, light/dark themes, reload and independent product Projects. Original Ash theme and empty custom starter list were restored. Preview is left on the welcome conversation with the conversation rail selected.

`welcome-light.jpg`, `starter-light.jpg`, and `projects-independent.jpg` are actual Local Lab screenshots. `fixture-*.png` are isolated HTTP-fixture screenshots, including final dark/mobile layouts.

No own machine is connected in this preview. Sending is honestly blocked; no paid Claude/Codex run was performed. Provider argument propagation and daemon/server integration were verified with executable/HTTP fixtures. A second browser-authenticated member was not created; backend authorization/RLS and mounted scope-race tests cover their stated boundaries.

Nonblocking output: Vite warns about chunks over500kB; legacy HTTP-only browser fixtures log refused WebSocket proxy connections to unused localhost8080. All corresponding checks exit0.

## Rulings I made

- Ruling: Bootstrap once per authenticated lab entry via explicit POST, rather than mutating GET — keeps reads side-effect free and retries intentional — costs one startup request.
- Ruling: New conversations use a private backing project per conversation — avoids cross-chat file/kernel leakage — adds project rows filtered from ordinary listing.
- Ruling: Default persistent shell rail state is per member/lab and browser — matches approved browser persistence — does not sync devices.
- Ruling: Reuse current isolated worktree/branch and commit pre-existing corrections separately — protects prior work without moving live frontend — branch name remains browser-comments.
- Ruling: Apply new conversation rail/FAB shell to Aristotle routes, retaining existing project-detail layout — preserves notebook/file/member controls without double rails — project-detail pages keep current navigation until separately redesigned.

The complete plan-specific workflow is retained in `workflow.tar.gz` with SHA256. Scratch workspace is removed after archiving; source, reports, review rulings and evidence remain recoverable in Git.

Readable log copies normalize trailing whitespace. Byte-for-byte originals are retained in `raw-final-logs.tar.gz` with SHA256.
