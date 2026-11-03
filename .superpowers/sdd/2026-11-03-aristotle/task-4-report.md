# Task 4 — final scoped review fixes

This report supersedes the earlier cursor and throughput descriptions. Previous commit `0aefb4a4` established transactional delivery, independent index maintenance, bounded pages, one-connection support, optional Search recovery, orphan cleanup and expanded event coverage. This change addresses the remaining throughput/fairness and unobserved-decision-write findings.

## Throughput, fairness and exact acknowledgement

- `Index.Put` now atomically writes its discovery hash and adds its ref to a workspace-local Redis set; `Delete` atomically removes both. These manifests contain refs only, outside the Search hash prefix. Existing unchanged source entries are enrolled during backfill.
- Per-event global Redis SCAN is removed. Source enumeration and **workspace-local SSCAN** pruning share a 32-fact budget. Small workspaces finish both phases in one tick, instead of unconditionally waiting for another interval.
- Up to **200 observed committed event IDs** for one workspace are coalesced into a reconciliation job. Only that captured list is acknowledged after the whole job succeeds; events arriving during a job are left for a new job, even when their IDs are lower than previously acknowledged IDs.
- Migration `0054_lab_index_scheduling` adds workspace IDs and an index to the durable delivery queue. `lab_pending_workspace_events(consumer,after_workspace,lim)` selects the next pending workspace by indexed UUID keyset, wraps, and returns at most 200 metadata rows. The scheduler persists its last workspace, so a busy workspace does not hide another workspace behind thousands of earlier event IDs.
- Workspace jobs persist bounded page progress and their captured IDs in Redis with a renewable 24-hour TTL; interrupted jobs retry safely. A new model discards old-model job progress. Epoch reset after Redis flush still starts source backfill; PostgreSQL pending rows survive Redis loss.
- Event workspace pages and recovery/backfill pages alternate. Global legacy/orphan discovery runs one incremental page **after** event work, independently of event-job completion. Its unfinished database-wide scan cannot hold up an acknowledgement. Old live entries are enrolled into manifests; stale entries are removed. Existing engine/autoresearch scheduling remains independent.

## Decision changes are now durable

A transactional inbox UPDATE trigger appends a separate fact-dirty row for changed shared decisions, covering the indexed `to_jsonb(inbox_item)` projection. It acquires no workspace event advisory lock and does not insert an event under the inbox row lock. This covers user triage, keep, archive and revive and the privileged automatic archive function without the event-lock/cascade deadlock. Details and current combined delivery APIs are recorded in the final correction section below.

## Durable API for Task 9

- `lab_pending_events(consumer,lim)` and `lab_pending_workspace_events(consumer,after_workspace,lim)` return only `(id,workspace_id,type,card_id)`, clamped to 0..200.
- `lab_ack_event(consumer,event_id)` deletes that exact delivery row. Capture IDs before reconciliation; do not add future IDs to a partially processed job or acknowledge a `MAX(id)` high-water mark.
- Acknowledged rows are removed; pending rows cascade with their source events. Queue/consumer tables remain owner-only; narrow security-definer functions have fixed search paths, fully qualified sources, and no PUBLIC execute grant.
- Only `indexer` is currently registered. Task 9 must register its own consumer/backfill under a producer-blocking lock in one migration transaction, e.g. `LOCK TABLE public.event IN SHARE ROW EXCLUSIVE MODE; INSERT INTO public.lab_event_consumer VALUES ('lessons'); INSERT INTO public.lab_event_delivery(consumer,event_id,workspace_id) SELECT 'lessons',id,workspace_id FROM public.event ON CONFLICT DO NOTHING;`. The trigger then maintains independent deliveries for registered consumers.
- Old `lab_events_after`/`lab_advance` remain compatible for existing callers but are not commit-safe across workspaces and must not be used for freshness-sensitive Task 9 delivery.

## Exact focused verification

Task-owned PostgreSQL 17 service: `lyk-ari-task4-throughput-pg-01a1a1cf` (port 52277). Task-owned Redis 8 service: `lyk-ari-task4-throughput-redis-01a1a1cf` (port 52279). No preview services/data were touched.

1. `LYK_TEST_PG='postgres://owner:owner@127.0.0.1:52277/postgres?sslmode=disable' LYK_TEST_REDIS='redis://127.0.0.1:52279' LYK_INDEXER_INTEGRATION=1 go test ./internal/labindex ./internal/labmemory -p 1 -run 'Indexer|TestReadMaintenanceRequiresInternalScopedTransaction' -count=1` — **PASS**, 20 indexer tests plus maintenance authorization/projection test.
2. `LYK_TEST_PG='postgres://owner:owner@127.0.0.1:52277/postgres?sslmode=disable' LYK_TEST_REDIS='redis://127.0.0.1:52279' go test ./migrations -run 'Aristotle|LabChatIsOwnerOnly|LabEventsAfterAdvances' -count=1` — **PASS**, eight existing migration compatibility tests.
3. `LYK_INDEX_INTEGRATION=1 go test ./internal/labindex -run 'TestSearchStaysInItsWorkspaceAndFindsNearestFirst|TestPutRequiresVersion' -count=1 -v` — **PASS**, using the test helper's own UUID-named disposable Redis container, removed by its cleanup.
4. Directly affected existing handler tests: `go test ./internal/handler -run 'TestTriageOrdersAndSnoozes|TestKeepArchiveRevive' -count=1` with the same isolated service variables.
5. `git diff --check` — **PASS**.

New regression evidence:

- **200 relevant events complete in one tick despite 32,000 unrelated Redis keys**, while the global cleanup cursor is still unfinished. This removes the six-events/minute ceiling and unrelated-Redis-size dependency.
- An 80-record workspace job remains incomplete while **another workspace is indexed and acknowledged on the next tick**. Its captured 200 IDs later finish together; an event committed after job creation is explicitly asserted still pending until a new reconciliation.
- Triage, keep, archive and revive use the real generated transactional write primitives, without manually emitting events; every resulting indexed decision version changes. The actual privileged automatic archive function also changes the indexed version without a manual emit.
- New scheduling/dirty-trigger migration down/up is exercised successfully.
- Earlier tests continue to cover lower-ID late commits, TEI failure/retry, one-connection pool/blocked embeddings/cancellation, deleted study/workspace cleanup, Redis flush/schema recovery with no events, transient startup Redis error, model changes, reminders, and Coresearch/nil-card lifecycle exclusion.

## Remaining limits

Reconciliation still reads the workspace's shared facts over bounded pages; a genuinely large workspace can require many visits. It no longer monopolizes other workspaces or waits for an unrelated global Redis scan. Redis SCAN/SSCAN COUNT is a hint; surplus keys are retained for later bounded processing. Individual fact projections can be large. Global legacy/orphan cleanup is incremental and may lag with a large shared Redis database, independently of event acknowledgements. Pending delivery can grow while optional indexing is disabled/unavailable. Unrecognized/no-event mutations outside the now-covered decision paths still rely on later relevant events or recovery/backfill; exhaustive producer-by-producer acceptance is not claimed. All search candidates require live authorized re-reads; no source text is stored in Redis, and Coresearch/chats/private Team definitions remain excluded.

Final handoff: both directly affected decision handler tests passed. Both task-owned throughput containers were removed successfully; the separate Search test's disposable container was cleaned by its helper. Final diff check passed. Preview resources and pre-existing untracked `node_modules` were untouched.

# Final lock-order / acknowledgement correction (migration 0055)

This section supersedes the preceding description of the decision UPDATE trigger emitting an event. It now appends a **separate transactional fact-dirty row**, and acquires no workspace event/advisory lock. The dirty queue deliberately has no workspace/card/inbox/event FK: a source-row mutation cannot wait for event-lock acquisition or a cascading source deletion, and pending invalidation survives deletion of the source. Its only FK is to the internal consumer registry. All registered consumers receive independent pending dirty rows. Rows contain workspace/ref/card IDs only and are deleted on successful acknowledgement.

`lab_pending_workspace_work(consumer,after_workspace,lim)` round-robins over the union of event deliveries and dirty refs and returns at most 200 `(source,id,workspace_id,type,card_id)` metadata rows. `source` distinguishes event IDs from dirty IDs; clients must retain that distinction because the sequences are independent. The indexer coalesces only its exact observed IDs of each source and persists both lists with the workspace job. The old event-only helper APIs remain compatible.

`lab_ack_work(consumer,event_ids bigint[],dirty_ids bigint[])` acknowledges both exact lists in **one database call and transaction**, rejecting a combined cardinality above 200. Already-deleted IDs are harmless. This removes the per-ID network round trips and prevents a timeout/retry from repeatedly paying for an already-acknowledged prefix. Skipped irrelevant event batches also use the bounded batch helper. No future or uncaptured IDs are deleted.

For Task 9, consume the combined work helper to observe both event and decision invalidations. New consumer registration/backfill must block both producer tables in one migration transaction (`LOCK TABLE public.event,public.inbox_item IN SHARE ROW EXCLUSIVE MODE`), register the consumer, backfill `lab_event_delivery(consumer,event_id,workspace_id)` from current events, and, if a complete initial decision baseline is needed, enqueue current supported study decision refs into `lab_fact_dirty(consumer,workspace_id,ref,card_id)` from current inbox rows. Future updates are then captured transactionally by the dirty trigger. Both queues and all new helpers remain privileged; no global source text is returned.

## Latest exact tests and cleanup

Own PostgreSQL 17: `lyk-ari-task4-lock-pg-01a1a1cf`, port 53172. Own Redis 8: `lyk-ari-task4-lock-redis-01a1a1cf`, port 53174.

`LYK_TEST_PG='postgres://owner:owner@127.0.0.1:53172/postgres?sslmode=disable' LYK_TEST_REDIS='redis://127.0.0.1:53174' LYK_INDEXER_INTEGRATION=1 go test ./internal/labindex -run Indexer -count=1 -v` — **PASS, all 22 tests**.

New regression details:

- `TestIndexerDecisionDirtyingDoesNotDeadlockCardDeletion/triage` and `/automatic_archive` force the deleter to hold the real workspace event advisory lock and wait on an inbox row held by the mutator (verified via `pg_stat_activity`). The actual triage write and the actual privileged automatic archive function then complete and commit without acquiring that event lock, allowing cascading deletion to finish. Pending dirty work survives source deletion and removes stale indexed decisions.
- `TestIndexerBatchAcknowledgementProgressWithSlowDatabaseAndDeletedPrefix` replays a completed 200-ID job with its first 100 IDs already deleted, injects **100 ms per delivery DELETE statement**, and requires progress under a three-second deadline. It observes **exactly one delivery DELETE**, no pending rows, and removal of the replay job. The old 200-request approach would exceed the deadline even while repeatedly revisiting the deleted prefix.
- All prior throughput, fairness, future-event non-acknowledgement, cross-workspace commit interleaving, TEI retry, one-connection pool, Redis recovery, deletion cleanup, user/automatic decision freshness and Coresearch exclusion tests still pass. Migration reversibility now exercises 0055 down, 0054 down/up, then 0055 up so the final state contains the safe dirty trigger.

Directly affected migration compatibility and handler checks are recorded in the final handoff below. No broad test suite was run.

Final handoff checks: the eight existing Aristotle migration compatibility tests passed with 0055 applied (`go test ./migrations -run 'Aristotle|LabChatIsOwnerOnly|LabEventsAfterAdvances' -count=1`); both directly affected decision handler tests passed (`go test ./internal/handler -run 'TestTriageOrdersAndSnoozes|TestKeepArchiveRevive' -count=1`), with the same isolated PG/Redis variables above. Final `git diff --check` passed. Both task-owned lock-order test containers were removed successfully. No preview resources or pre-existing untracked `node_modules` were changed.

## Migration allocation correction

All Aristotle schema, queue helpers and triggers are now owned by the reversible 0052 migration. The 0053–0055 allocations referenced above describe intermediate development commits only. A clean database now records a single Aristotle migration. A database previously using these intermediate migration versions would need its development migration history reconciled before those numbers are reused.

In two isolated databases, PostgreSQL 17 schema-only dumps matched for all three comparisons (normalizing pg_dump's random restrict token): 0051 vs 0052 rolled back, first vs second 0052 application, and consolidated 0052 vs the former 0052–0055 chain. This covers the schema, functions, triggers, indexes and ACLs. The indexer reversibility test now verifies pre-existing card events are backfilled on reapplication.

Focused checks ran sequentially against disposable PostgreSQL 17 and Redis 8:
- `go test ./migrations -run 'Aristotle|LabChatIsOwnerOnly|LabEventsAfterAdvances' -count=1` passed.
- `LYK_INDEXER_INTEGRATION=1 go test ./internal/labindex -run Indexer -count=1` passed.
- `go test ./internal/handler -run 'TestTriageOrdersAndSnoozes|TestKeepArchiveRevive' -count=1` passed.

Removed only the task-owned containers `lyk-ari-fold52-01a1-pg` and `lyk-ari-fold52-01a1-redis`.
