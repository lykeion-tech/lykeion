# Task 1: Aristotle persistence and bootstrap

Status: complete. No merge, push, preview restart or live database migration performed.

## Interfaces

- `POST /api/coresearch/bootstrap`, existing authenticated workspace header. No body, machine or CLI required. HTTP 200 `{ "agent": Agent, "conversation": CoresearchConversation }`. This transaction takes an advisory lock keyed by workspace/member, ensures the member's builtin agent, and creates the welcome project/chat/conversation exactly once. Repeated bootstrap retains user edits. Builtin default name Aristotle, harness claude, model empty, visibility private, key aristotle, starters `[]` (use product defaults). Existing ordinary agents named Aristotle are retained.
- `GET /api/coresearch/conversations`, existing authenticated workspace header. HTTP 200 array, owner filtered by database RLS and query: `id, workspace_id, owner_id, agent_id, project_id, chat_id, title, is_welcome, snapshot_harness, snapshot_model, snapshot_instructions, created_at, updated_at, preview`. Preview is latest persisted message's first 240 characters or empty; order is updated_at descending then id. GET performs no writes. All three snapshot fields start null for welcome; they must be populated together at the first accepted turn by Task 2. No static welcome message is inserted as an agent reply.
- Existing Agent JSON adds `built_in_key` (empty string for ordinary agents, `aristotle` for builtin) and `conversation_starters` (ordered array of `{label,prompt}`). Create/PATCH validate max 12, nonblank valid UTF-8 labels/prompts without NUL, label max 120 codepoints, prompt max 16384 bytes. `[]` means product defaults; null is rejected. Public nonempty builtin creation and any identity mutation are rejected with 400. Existing edit/read authorization including admin scope remains unchanged. Builtins cannot become workspace-visible (400), or archive (409).
- Ordinary live-agent name uniqueness remains `(workspace_id, lower(name))`; builtin agents are excluded from it. Builtin identity uniqueness is `(workspace_id, owner_id, built_in_key)` with nonempty key. Thus ordinary Aristotle is supported both before and after builtin creation.

## Schema / Task 2 integration

Migration `0026_aristotle.up.sql` adds `agent.built_in_key text NOT NULL DEFAULT ''`, `agent.conversation_starters jsonb NOT NULL DEFAULT '[]'`, builtin-private/nonarchived constraint and indexes. `ArchiveOwnedAgents` explicitly skips builtins, preserving member-removal lifecycle.

`coresearch_project.conversation_owner_id uuid NULL` classifies a private backing project. Ordinary project list excludes all classified projects. Existing URLs keep working for the owner. Project membership/transfer/rename/archive mutations return 403 for classified projects so a welcome cannot disappear or be shared through ordinary project management. `coresearch_can_see` additionally requires matching conversation owner (or the authorized running Coresearch agent identity) for classified projects; ordinary project authorization stays unchanged. Public project bodies cannot forge or downgrade classification (strict unknown-field decoding).

New owner-RLS `coresearch_conversation` table has IDs and fields listed above, one unique project and chat per conversation, one partial unique welcome per workspace/member, workspace-composite foreign keys, and an all-null/all-nonnull snapshot check. Membership removal cascades its conversation; existing project departure archives the inaccessible backing project. Builtin agent itself remains nonarchived.

Generated queries: `LockAristotleOwner`, `EnsureAristotle`, `GetWelcomeConversation`, `MarkConversationProject(ctx, projectID)`, `CreateWelcomeConversation(ctx, {AgentID,ProjectID,ChatID})`, `ListCoresearchConversations`. Existing generated `CoresearchProject`, `Agent`, and flow scans include the new columns. Generated using pinned repo `GOMAXPROCS=2 go tool sqlc generate` from server/.

Task 2 must add normal conversation and request-id receipt queries, lock config/snapshot with acceptance, update conversation title/updated_at on accepted turns as appropriate, and use the existing project/chat IDs for stream/cancel. Existing project turn endpoint must reject classified conversations or apply the same frozen config logic, preventing a bypass. Task 1 deliberately does not change execution.

## Verification evidence

- RED: `GOMAXPROCS=2 go test -p 1 ./internal/handler -run TestAristotleBootstrap -count=1`: new test failed because POST bootstrap returned 404 before production edits. First invocation hit Go cache permission and was rerun with escalation; RED observed against real isolated PostgreSQL/Redis containers.
- GREEN initial: same command passed (`ok .../internal/handler 3.053s`).
- GREEN expanded: `GOMAXPROCS=2 go test -p 1 ./internal/handler -run 'TestAristotle|TestConversationStarter' -count=1` passed (`2.933s`). Tests cover repeated/concurrent bootstrap, two members, cross-workspace, ordinary collision, no machine, retained customization, private conversations/backing projects, starter validation/roundtrip/defaults, archive/visibility/identity restrictions.
- First wider run `GOMAXPROCS=2 go test -p 1 ./internal/handler ./migrations ./internal/team ./internal/flow -count=1` found three fixture/catalog updates: route walker must scope id-less bootstrap/list routes to outsider workspace; migration 0025 rollback must roll back 0026 first; tenant table count increases from 50 to 51. Team (`3.476s`) and flow (`7.867s`) passed. Corrected those fixtures and reran focused handler/migration coverage successfully (`3.515s`, `2.205s`). Added member-removal/ordinary-after-builtin and UTF-8/length boundary tests.
- Final wider rerun: `GOMAXPROCS=2 go test -p 1 ./internal/handler ./migrations -count=1` passed (handler `110.662s`, migrations `5.230s`).
- Final focused rerun after null/UTF-8 boundary and archive error-message refinements: `GOMAXPROCS=2 go test -p 1 ./internal/handler -run 'TestAristotle|TestConversationStarter' -count=1` passed (`2.803s`).
- `git diff --check` passed. sqlc-generated changes were included explicitly, including dependent flow scans.

## Commits

- `0bedd39`: implementation, generated queries and regression tests.
- This report is recorded in the following documentation commit. No unrelated files staged.

All database tests use task-owned testcontainers; existing preview PostgreSQL/Redis state and services were untouched. Heavy checks run sequentially with GOMAXPROCS=2 and -p 1.

## Round 1 review: bootstrap/departure serialization

The alleged HTTP race is already prevented by existing middleware; no production change was needed. `server/internal/middleware/middleware.go:117-130` acquires `LockWorkspaceForMutation` for every non-GET/HEAD request before `LockMember`, then retains that scoped transaction while invoking the handler. The query in `server/pkg/db/queries/workspace.sql:65-69` locks the workspace row `FOR UPDATE` and then reads/locks membership `FOR SHARE`. Bootstrap POST and member-removal DELETE therefore share serialization before bootstrap creation or departure enumeration, and membership is checked after the shared lock is acquired. This preserves the established workspace-before-membership lock order without adding a second redundant lifecycle lock.

Added deterministic `TestAristotleBootstrapSerializesWithDeparture`: an owner-connection advisory barrier pauses an archive-update trigger during `CoresearchDepart`'s existing-project enumeration. Bootstrap is started while departure is paused. The test observes its PostgreSQL lock wait on `LockWorkspaceForMutation`, releases departure, then verifies removal returns 204, bootstrap returns 404 after membership disappears, and no unarchived classified project survives without membership/conversation. Ordinary project departure is included in the fixture.

The first two barrier attempts waited specifically for bootstrap's Aristotle advisory lock and timed out: bootstrap was correctly blocked earlier on the workspace row lock. Those attempts did not establish the reported race. The retained test observes the actual workspace wait and passes against unchanged production code. Review ruling from parent: retain regression/evidence and request scoped re-review; do not add redundant production locking.

- `GOMAXPROCS=2 go test -p 1 ./internal/handler -run TestAristotleBootstrapSerializesWithDeparture -count=1`: PASS (`2.710s`).
- `GOMAXPROCS=2 go test -p 1 ./internal/handler -run 'TestAristotle|TestWorkspaceDeparturePreservesProjectOwnership|TestEmptyProjectArchivesOnWorkspaceDeparture|TestAdministrativeDepartureChoosesOnlyRequiredExistingSuccessor|TestALeavingMemberTakesTheirSharingWithThem|TestLastOwnerIsProtected|TestConcurrentLastOwnerMutations' -count=1`: PASS (`7.679s`).
- No full-suite rerun, preview mutation, merge or push. Only regression and report changed in this review round.
