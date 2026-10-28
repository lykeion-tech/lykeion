# Task 2: Atomic conversations and frozen execution

Implementation and affected verification complete. No merge/push, live database migration, preview restart or preview service/database access performed.

## Exact acceptance interface

- `POST /api/coresearch/conversations`: strict JSON `{ "body": "...", "request_id": "UUID" }`. Body must be nonblank valid UTF-8, at most 262144 bytes; nonzero UUID required. Unknown fields (including agent_id, harness and snapshot fields) rejected400.
- `POST /api/coresearch/conversations/{conversation}/turns`: identical body, owner/workspace constrained; welcome snapshots first accepted turn. Both return201 `{ "conversation": CoresearchConversation, "run": AgentRun }`. Conversation includes existing `project_id`, `chat_id`; run includes `id` for the existing message/step/cancel paths. New title is first80 Unicode codepoints of trimmed first message. New welcome title is `Getting started with Aristotle`.
- Exact accepted request retry returns the originally persisted JSON and201 before consulting present configuration, chat busy state or runtime availability. Request UUIDs are scoped to workspace/member across both acceptance endpoints. Reusing with changed body or changed target returns409. A failed unavailable acceptance writes nothing; the same UUID can succeed later.
- No available online own runtime matching frozen harness (and bound daemon, if any) returns409 with `No available own machine with <harness>; connect a machine in Settings`. Runtime freshness matches existing sweep default60 seconds; stale-online runtimes rejected. Shared/other-owner runtimes do not qualify. Missing/cross-member/cross-workspace conversation404.
- A busy chat returns409. Bootstrap retains its existing200 interface; no static intro is inserted as a generated message. Settings changes affect only future acceptance snapshots.
- Classified project legacy chat creation and legacy turn acceptance return403, closing execution without snapshots. Legacy owner message reads, run step reads, cancellation and existing stream paths retain their existing handlers/IDs. Ordinary project routes unchanged.

## Persistence and execution

Migration0027 adds owner-RLS `coresearch_turn_receipt` keyed `(workspace_id,owner_id,request_id)` with target/body/original JSON. It is committed atomically with backing private project/chat, server-resolved Aristotle config snapshot, real user turn message and queued run. Membership departure cascades receipts. Existing middleware workspace-before-membership write serialization remains; no redundant lifecycle lock added. Project-before-chat locks retained for existing conversations.

SQL generated with `GOMAXPROCS=2 go tool sqlc generate`. Snapshot updates all three fields together and switches welcome chat.cli to the frozen harness. Instructions prefix the actual stored execution prompt; explicit frozen model is persisted in `agent_run.requested_model`. Claim retains model and derives `coresearch.aristotle` from server-side backing-project classification. Daemon maps that authorized marker and requested model into `ExecOptions`; Claude emits `--model`, Codex emits `-m`. Empty model emits neither flag and uses provider default. Ordinary Coresearch still passes no requested model to adapters and ignores direct Model overrides, preserving prior defaults/tool restrictions. Sandbox, sources MCP, cancellation and stream plumbing unchanged.

## TDD and focused verification

- Initial sandbox test invocation could not reach Docker socket. Reran with authorized escalation, dedicated testcontainers only.
- RED `GOMAXPROCS=2 go test -p 1 ./internal/handler -run TestAristotleAcceptanceUnavailableIsAtomic -count=1`: expected409, got405 (missing acceptance route), real isolated PostgreSQL/Redis.
- GREEN same command: handler2.964s after atomic implementation.
- RED `GOMAXPROCS=2 go test -p 1 ./pkg/agent -run TestAristotleCoresearchHonorsConfiguredModel -count=1`: both Claude/Codex recorded commands lacked frozen model. First implementation exposed additional Claude entry-point clearing; fixed that restriction only for Aristotle.
- GREEN `GOMAXPROCS=2 go test -p 1 ./pkg/agent ./internal/daemon -run 'TestAristotle|TestCoresearchUsesProviderDefault|TestCoresearchCodexDefault|TestTurnUsesChatFolderAndNoDoor' -count=1`: agent1.248s, daemon0.716s.
- GREEN `GOMAXPROCS=2 go test -p 1 ./internal/handler -run 'TestAristotle' -count=1`:3.713s. Covers unavailable atomic rollback, other-owner runtime rejection, accepted config/claim propagation, exact offline replay after settings/harness change, changed-payload/target conflicts, cross-member/lab404, legacy bypass403, preserved reads/step/cancel paths, frozen followup, failed fresh acceptance retry after codex comes online, welcome no-fake-response/null snapshot, claude-to-codex first welcome acceptance, concurrent welcome replay, concurrent create single conversation, untrusted client config400.
- Affected suite command: `GOMAXPROCS=2 go test -p 1 ./internal/handler ./internal/coresearch ./internal/service ./internal/daemon ./pkg/agent ./migrations -count=1` initial run found `TestWorkspaceRouteIsolation`: new conversation turns route returned400 because walker supplied no valid body/resource substitution. Handler125.108s; coresearch0.550s and service12.825s passed. Daemon98.434s, agent48.044s and migrations5.608s also passed. No production regressions were observed in those packages.
- Additional RED `GOMAXPROCS=2 go test -p 1 ./internal/handler -run TestAristotleRejectsStaleOnlineRuntime -count=1`: a65-second-old online runtime was accepted201 with the initial90-second predicate. Aligned SQL to existing runtime-sweep60-second default, regenerated, and repaired route walker with real welcome conversation and valid body/UUID.
- GREEN `GOMAXPROCS=2 go test -p 1 ./internal/handler -run 'TestAristotleRejectsStaleOnlineRuntime|TestWorkspaceRouteIsolation' -count=1`:2.894s. Stale acceptance rolls back and same UUID succeeds after fresh heartbeat.
- Final full handler rerun `GOMAXPROCS=2 go test -p 1 ./internal/handler -count=1`: PASS112.598s. Other affected packages not repeated because they passed and did not change.
- `git diff --check` passed before final handler rerun.

## Boundaries and concerns

Provider execution verification uses actual adapter command generation with harmless executable fixtures; daemon execution uses existing isolated fake runtime/HTTP fixtures. No live paid Claude/Codex scientific run was performed. Runtime availability is an acceptance-time observation; a machine can disconnect after acceptance, and existing durable queued-run behavior handles that. Receipts intentionally return original queued run state rather than current status; frontend should use retained run IDs and existing stream/read paths for progress.

## Commits

`1c367ff` — `feat(coresearch): accept idempotent Aristotle turns with frozen execution` (20 explicitly staged backend/query/migration/test files). Report left ignored at parent request. No merge/push. `git diff --check` passed; post-commit tracked/untracked working tree clean (ignored report retained).
