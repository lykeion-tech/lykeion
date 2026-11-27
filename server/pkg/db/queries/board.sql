-- name: LockCard :one
SELECT * FROM card WHERE id = @id AND workspace_id = @workspace_id FOR NO KEY UPDATE;

-- name: TryLockCard :one
SELECT pg_try_advisory_xact_lock(hashtextextended(sqlc.arg(card_id)::uuid::text, 1)) AS ok;

-- The same lock as TryLockCard, waited for: the engine's write phase must not
-- drop finished scoring because a short read phase holds the lock.
-- name: WaitLockCard :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(card_id)::uuid::text, 1));

-- Sets or clears the card's scoring lease. version is left alone: the lease is
-- the engine's bookkeeping, not a change to the card.
-- name: SetGatingLease :exec
UPDATE card SET gating_until = sqlc.narg(gating_until) WHERE id = @id AND workspace_id = @workspace_id;

-- spent_usd is never written here: F2's settle is its only writer. Leaving a
-- column clears legacy_column, as SaveCardFlow does.
-- name: SaveCardBoard :exec
UPDATE card SET legacy_column = CASE WHEN board_column = @board_column THEN legacy_column ELSE '' END,
  board_column = @board_column, state = @state, flag = @flag, ending = @ending,
  end_reason = @end_reason, rounds = @rounds,
  round = @round, handoff_note = @handoff_note, block_kind = @block_kind, question = @question, version = version + 1
WHERE id = @id AND workspace_id = @workspace_id;

-- name: RoundAgentRuns :many
SELECT * FROM agent_run WHERE workspace_id = @workspace_id AND card_id = @card_id::uuid
  AND board_column = @board_column AND round = @round ORDER BY created_at;

-- A member's run of a column's round that has not ended: queued, dispatched
-- or running. Members are every role but the leader, the auditor, a study's
-- single agent, Validation's system runs (the objector and the rerun) and
-- a finished study's report run.
-- name: InFlightMemberRun :one
SELECT id FROM agent_run WHERE workspace_id = @workspace_id AND card_id = @card_id::uuid
  AND board_column = @board_column AND round = @round
  AND role NOT IN ('', 'leader', 'auditor', 'agent', 'objector', 'rerun', 'reporter') AND status IN ('queued', 'dispatched', 'running')
ORDER BY created_at LIMIT 1;

-- name: MarkQueueOffline :exec
SELECT mark_queue_offline();

-- A study still open asks the question: not stopped, and not done.
-- name: ActiveDuplicate :one
SELECT id, number FROM card
WHERE workspace_id = @workspace_id AND ending = '' AND state <> 'done'
  AND (norm_text(title) = norm_text(@title::text) OR norm_text(question) = norm_text(@question::text))
ORDER BY number LIMIT 1;

-- name: GetCardByNumber :one
SELECT * FROM card WHERE workspace_id = @workspace_id AND number = @number;

-- name: SupersedeRoleKinds :exec
UPDATE record SET superseded_at = now()
WHERE workspace_id = @workspace_id AND card_id = @card_id AND board_column = @board_column
  AND role = @role AND kind = ANY(@kinds::text[]) AND superseded_at IS NULL;

-- name: InsertInboxItem :exec
INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text)
VALUES (@workspace_id, @card_id::uuid, @user_id, @kind, @text);

-- name: InsertEnvironmentHold :exec
INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text, cause)
VALUES (@workspace_id, @card_id::uuid, @user_id, 'waiting_for_machine', @text, 'environment_unavailable');

-- A study's stop is a decision: one open item per study and cause (a needs_decision
-- item), carrying its options, its default and the moment the default applies.
-- name: UpsertStopDecision :exec
INSERT INTO inbox_item (workspace_id, card_id, user_id, kind, text, cause, options, default_option, expires_at)
VALUES (@workspace_id, @card_id::uuid, @user_id, @kind, @text, @cause::text, @options::jsonb, @default_option, @expires_at)
ON CONFLICT (workspace_id, card_id, cause) WHERE kind = 'needs_decision' AND resolved_at IS NULL
DO UPDATE SET text = EXCLUDED.text, options = EXCLUDED.options, default_option = EXCLUDED.default_option,
  expires_at = EXCLUDED.expires_at, reminded_at = NULL;

-- One open offline notice per machine; there is no unique index for it, so the
-- sweep, which runs alone, checks first.
-- name: InsertMachineOffline :many
INSERT INTO inbox_item (workspace_id, user_id, kind, text, runtime_id, cause)
SELECT @workspace_id, @user_id, 'machine_offline', @text, @runtime_id, 'offline'
WHERE NOT EXISTS (SELECT 1 FROM inbox_item WHERE workspace_id = @workspace_id AND runtime_id = @runtime_id
  AND kind = 'machine_offline' AND resolved_at IS NULL)
RETURNING id;

-- name: ResolveMachineOffline :exec
UPDATE inbox_item SET resolved_at = now(), status = 'dismissed', touched_at = now()
WHERE workspace_id = @workspace_id AND runtime_id = @runtime_id AND kind = 'machine_offline' AND resolved_at IS NULL;

-- name: BoardCandidates :many
SELECT workspace_id::uuid AS workspace_id, card_id::uuid AS card_id, board_column::text AS board_column
FROM board_candidates();

-- name: SetWorkspaceSettings :exec
UPDATE workspace SET settings = @settings WHERE id = @id;

-- name: CardInflightAgentRuns :many
SELECT id FROM agent_run
WHERE workspace_id = @workspace_id AND card_id = @card_id
  AND status IN ('queued', 'dispatched', 'running')
ORDER BY created_at, id;

-- The live records of one kind on a card, in every column, oldest first.
-- name: CardKindRecords :many
SELECT * FROM record WHERE workspace_id = @workspace_id AND card_id = @card_id AND kind = @kind
  AND superseded_at IS NULL ORDER BY created_at, id;

-- name: LiveCardRecord :one
SELECT * FROM record WHERE workspace_id = @workspace_id AND card_id = @card_id AND id = @id
  AND superseded_at IS NULL;

-- name: LiveCardRecordByKey :one
SELECT * FROM record WHERE workspace_id = @workspace_id AND card_id = @card_id AND key = @key
  AND superseded_at IS NULL;

-- name: SupersedeRecord :exec
UPDATE record SET superseded_at = now() WHERE workspace_id = @workspace_id AND id = @id AND superseded_at IS NULL;

-- Every file a card's records keep in the blob store: a record's own blob, a
-- run's whole stderr, and a run's package list.
-- name: CardBlobKeys :many
SELECT DISTINCT k::text AS blob_key FROM record r,
  unnest(ARRAY[
    r.blob_key,
    CASE WHEN r.kind = 'run' THEN coalesce(r.body->>'error_blob', '') ELSE '' END,
    CASE WHEN r.kind = 'run' THEN coalesce(r.workspace_id::text || '/' || r.card_id::text || '/' || (r.body->>'packages_sha256'), '') ELSE '' END
  ]) AS k
WHERE r.workspace_id = @workspace_id AND r.card_id = @card_id AND k <> '';

-- One open item per machine and cause: later waits on it add nothing.
-- name: UpsertMachineWait :exec
INSERT INTO inbox_item (workspace_id, user_id, kind, text, runtime_id, cause)
VALUES (@workspace_id, @user_id, 'waiting_for_machine', @text, @runtime_id, @cause)
ON CONFLICT (workspace_id, runtime_id, cause) WHERE kind = 'waiting_for_machine' AND resolved_at IS NULL DO NOTHING;

-- A machine's waits end when it recovers, as its offline notice does.
-- name: ResolveMachineWaits :exec
UPDATE inbox_item SET resolved_at = now(), status = 'dismissed', touched_at = now()
WHERE workspace_id = @workspace_id AND runtime_id = @runtime_id AND kind = 'waiting_for_machine' AND resolved_at IS NULL;

-- A machine's wait, which its owner resolves by saying the cause is dealt with.
-- name: ResolveOpenOwnerItem :one
UPDATE inbox_item SET resolved_at = now(), status = 'dismissed', touched_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND user_id = @user_id AND resolved_at IS NULL
  AND kind = 'waiting_for_machine'
RETURNING *;

-- A redone column answers the study's open stops with redo, the guidance
-- being what the person said.
-- name: RedoCardStops :many
UPDATE inbox_item SET status = 'decided', chosen_option = 'redo', guidance = @guidance, decided_by = @decided_by::uuid,
  decided_at = now(), resolved_at = now(), touched_at = now()
WHERE workspace_id = @workspace_id AND card_id = @card_id::uuid AND resolved_at IS NULL
  AND (kind = 'blocked' OR kind = 'needs_decision' AND cause IN ('could_not_do', 'all_withdrawn', 'validation_invalid'))
RETURNING id;

-- A redone column clears its machine-failure and environment holds.
-- name: DismissMachineFailures :many
UPDATE inbox_item SET status = 'dismissed', resolved_at = now(), touched_at = now()
WHERE workspace_id = @workspace_id AND card_id = @card_id::uuid
  AND (kind = 'needs_decision' AND cause = 'machine_failures' OR kind = 'waiting_for_machine' AND cause = 'environment_unavailable')
  AND resolved_at IS NULL
RETURNING id;

-- name: ResolveMachineWaitsExcept :exec
UPDATE inbox_item SET resolved_at = now(), status = 'dismissed', touched_at = now()
WHERE workspace_id = @workspace_id AND runtime_id = @runtime_id AND kind = 'waiting_for_machine' AND resolved_at IS NULL
  AND cause IS DISTINCT FROM @keep::text;

-- The workspace's offline machines with queued runs that only they can take,
-- and the number of those runs.
-- name: OfflineRuntimesWithQueuedRuns :many
SELECT r.id, r.owner_id, COALESCE(r.custom_name, r.device_name)::text AS machine, count(*)::int AS queued
FROM runtime r
JOIN agent_run t ON t.workspace_id = r.workspace_id AND t.status = 'queued' AND t.kind IN ('role', 'rerun', 'report')
  AND (t.harness = r.harness OR t.kind = 'rerun')
JOIN card c ON c.id = t.card_id AND c.workspace_id = t.workspace_id
-- a laboratory agent's runs wait for its bound runtime (a rerun for that
-- runtime's machine), as the claim sends them there
LEFT JOIN agent_run_laboratory(t.workspace_id, t.id) AS la(id, owner_id, runtime_id, name) ON true
WHERE r.workspace_id = @workspace_id AND r.status = 'offline'
  AND ((t.kind <> 'report' AND la.id IS NOT NULL AND (r.id = la.runtime_id
      OR (t.kind = 'rerun' AND r.daemon_id = (SELECT b.daemon_id FROM runtime b WHERE b.workspace_id = t.workspace_id AND b.id = la.runtime_id))))
    OR (t.kind = 'report' AND r.id = t.pinned_runtime_id)
    OR (t.kind <> 'report' AND la.id IS NULL AND (c.machine = r.daemon_id OR (c.machine IS NULL AND t.responsible_user_id = r.owner_id))))
GROUP BY r.id;

-- One open notice per unbound laboratory agent (cause names it), for the
-- workspace owner who binds it.
-- name: UpsertLaboratoryWait :exec
INSERT INTO inbox_item (workspace_id, user_id, kind, text, cause)
SELECT @workspace_id, @user_id, 'waiting_for_machine', @text, @cause::text
WHERE NOT EXISTS (SELECT 1 FROM inbox_item WHERE workspace_id = @workspace_id AND kind = 'waiting_for_machine'
  AND runtime_id IS NULL AND cause = @cause::text AND resolved_at IS NULL);

-- name: ResolveLaboratoryWait :exec
UPDATE inbox_item SET resolved_at = now(), status = 'dismissed', touched_at = now()
WHERE workspace_id = @workspace_id AND kind = 'waiting_for_machine' AND runtime_id IS NULL AND cause = @cause::text AND resolved_at IS NULL;

-- name: MoveLaboratoryWaits :exec
UPDATE inbox_item SET user_id = @user_id, touched_at = now()
WHERE workspace_id = @workspace_id AND kind = 'waiting_for_machine' AND runtime_id IS NULL AND cause LIKE 'unbound:%'
  AND resolved_at IS NULL AND user_id <> @user_id;
