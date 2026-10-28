# Final review fix: welcome history recovery

Addressed final-review.md single P2 in commit `f340ffc` (`fix(coresearch): expose welcome history loading and retry`). Exactly AristotleChat.vue and tests/aristotle-ui.test.ts explicitly staged. No merge/push, agents, backend changes, services, or broad suites.

Saved conversation history loading/error renders before the empty welcome decision. GET errors expose their real message and Retry; explicit successful retry restores existing transcript. Composer remains editable and retains draft while required saved history is pending/error, but Send is disabled and the handler guards programmatic/keyboard sends. Unsaved draft bypasses saved-history pending/error gating, preserving the disabled query's normal pending state. Fixed welcome cards after confirmed empty and Projects independence remain covered by existing focused regressions. No enum/snapshot/request-ID changes.

RED on unchanged production: focused UI suite 1 failed/15 passed. Regression with successful cached bootstrap/list/profile and uncached welcome messages exposed fresh welcome/cards while pending and after GET500, missing actual history error, enabled Send. Second RED captured all four assertions. Initial GREEN attempt exposed test-tree textOf omission of rendered Markdown innerHTML; corrected assertion to inspect actual rendered existing comment innerHTML. Production behavior not changed for this harness issue.

Final verification (all exit 0):
- `npx vitest run tests/aristotle-ui.test.ts --environment ./tests/client-environment.ts --config apps/dashboard/vite.config.ts --root . --maxWorkers=1`: 16/16 passed, 720ms overall /136ms tests. New regression observes pending state, guarded send, GET500 real error, explicit Retry GET200 restoring existing comment history, and preserved draft/enabled Send on recovery.
- `npm run typecheck`: pass.
- `npx eslint apps/dashboard/src/coresearch/AristotleChat.vue tests/aristotle-ui.test.ts`: pass.
- `npx prettier --check apps/dashboard/src/coresearch/AristotleChat.vue tests/aristotle-ui.test.ts`: pass.
- `git diff --check`: pass.

Initial Git staging/commit hit sandbox index.lock permission; authorized escalated staging and commit succeeded. Report is ignored and not staged. Parent owns broader final acceptance checks.
