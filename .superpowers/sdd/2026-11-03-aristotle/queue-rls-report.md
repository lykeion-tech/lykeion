# Queue RLS correction

Enabled tenant RLS policies on `lab_event_delivery` and `lab_fact_dirty` in migration 0052. Both retain their PUBLIC/app privilege revocations; no CRUD grants were added. Table-owner operations and existing SECURITY DEFINER queue helpers continue bypassing RLS intentionally. The down migration already drops both tables and their policies, so needs no modification.

The global tenant-table guard now expects 76. Regression coverage checks actual app SELECT/INSERT/UPDATE/DELETE denial, owner queue writes, definer queue reads/acknowledgements, no-workspace visibility and cross-workspace update rejection under temporary grants rolled back by the test. The migration reversal test checks queue disappearance on down and reinstated policies/owner-only privileges on up.

Verification:
- Before implementation, focused regression failed with both queues missing RLS and policies.
- `go test ./migrations -count=1`: PASS (6.849s).
- `LYK_INDEXER_INTEGRATION=1 go test ./internal/labindex -run 'TestIndexer(DeliveryHelpersBoundedAndPrivileged|DeliversLowerIDCommittedAfterHigherID|FairWorkspacePagesAndNeverAcknowledgesFutureEvents|TracksUserAndAutomaticDecisionMutations|SchedulingMigrationReversible|DecisionDirtyingDoesNotDeadlockCardDeletion)$' -count=1`: PASS (6.428s).

Both successful commands used disposable `codex-queue-rls-pg-1103` and `codex-queue-rls-redis-1103` containers, isolated ports 55301/55303. Those two containers were removed after testing. No preview services were accessed. An initial non-escalated test attempt could not access isolated services due to sandbox networking; rerun with network permission passed.

No remaining concerns. Existing definer queue helpers deliberately operate across workspaces; tenant policies protect hypothetical future direct grants while current privileges prohibit direct app access entirely.
