-- A decision item is visible to every member who can open its study, and to
-- the person a studyless item (a machine, Autoresearch) was raised for.
-- may_answer is whether the viewer may answer it: the study's owner, a
-- workspace owner or admin, or the person a studyless item was raised for.

-- name: ListDecisions :many
SELECT sqlc.embed(i),
  (jsonb_array_length(i.options) > 0
    AND (@is_admin::bool OR c.owner_id = @viewer::uuid OR (i.card_id IS NULL AND i.user_id = @viewer::uuid)))::bool AS may_answer
FROM inbox_item i LEFT JOIN card c ON c.id = i.card_id
WHERE i.workspace_id = @workspace_id AND (i.card_id IS NOT NULL OR i.user_id = @viewer::uuid)
  AND ((i.status = 'open' AND i.resolved_at IS NULL AND i.archived_at IS NULL) = NOT @history::bool)
ORDER BY
  CASE WHEN @history::bool THEN NULL ELSE i.snoozed_until > now() END NULLS FIRST,
  CASE WHEN @history::bool THEN 0 ELSE CASE i.decide_by WHEN 'today' THEN 0 WHEN 'this_week' THEN 1 WHEN 'date' THEN 2 WHEN '' THEN 3 ELSE 4 END END,
  CASE WHEN @history::bool THEN NULL ELSE i.decide_by_date END,
  i.created_at DESC, i.id DESC
LIMIT 200;

-- name: GetDecision :one
SELECT sqlc.embed(i),
  (jsonb_array_length(i.options) > 0
    AND (@is_admin::bool OR c.owner_id = @viewer::uuid OR (i.card_id IS NULL AND i.user_id = @viewer::uuid)))::bool AS may_answer
FROM inbox_item i LEFT JOIN card c ON c.id = i.card_id
WHERE i.id = @id AND i.workspace_id = @workspace_id AND (i.card_id IS NOT NULL OR i.user_id = @viewer::uuid);

-- name: CountDecisions :one
SELECT count(*) FROM inbox_item i LEFT JOIN card c ON c.id = i.card_id
WHERE i.workspace_id = @workspace_id AND (i.card_id IS NOT NULL OR i.user_id = @viewer::uuid)
  AND i.status = 'open' AND i.resolved_at IS NULL AND i.archived_at IS NULL
  AND jsonb_array_length(i.options) > 0 AND (i.snoozed_until IS NULL OR i.snoozed_until <= now())
  AND (@is_admin::bool OR c.owner_id = @viewer::uuid OR (i.card_id IS NULL AND i.user_id = @viewer::uuid));

-- name: SetDecisionTriage :exec
UPDATE inbox_item SET decide_by = @decide_by, decide_by_date = @decide_by_date, snoozed_until = @snoozed_until, touched_at = now()
WHERE id = @id AND workspace_id = @workspace_id;

-- name: SetDecisionKept :exec
UPDATE inbox_item SET kept = true, touched_at = now() WHERE id = @id AND workspace_id = @workspace_id;

-- name: SetDecisionArchived :exec
UPDATE inbox_item SET archived_at = CASE WHEN @archived::bool THEN now() ELSE NULL END, touched_at = now()
WHERE id = @id AND workspace_id = @workspace_id;

-- Reading an item that only informs also resolves and dismisses it.
-- name: MarkDecisionRead :one
UPDATE inbox_item SET read_at = COALESCE(read_at, now()), touched_at = now(),
  resolved_at = CASE WHEN kind IN ('done', 'middle_score', 'no_tool', 'autoresearch_card', 'autoresearch_report') THEN COALESCE(resolved_at, now()) ELSE resolved_at END,
  status = CASE WHEN kind IN ('done', 'middle_score', 'no_tool', 'autoresearch_card', 'autoresearch_report') AND status = 'open' THEN 'dismissed' ELSE status END
WHERE id = @id AND workspace_id = @workspace_id AND user_id = @user_id
  AND (read_at IS NULL OR (kind IN ('done', 'middle_score', 'no_tool', 'autoresearch_card', 'autoresearch_report') AND resolved_at IS NULL))
RETURNING *;

-- name: LockDecision :one
SELECT * FROM inbox_item WHERE id = @id AND workspace_id = @workspace_id FOR UPDATE;

-- Answering resolves the item too, so the code that reads resolved_at sees
-- it closed.
-- name: DecideItem :one
UPDATE inbox_item SET status = 'decided', chosen_option = @chosen_option, guidance = @guidance, decided_by = @decided_by::uuid,
  decided_at = now(), resolved_at = now(), touched_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- A stopped study's open decisions are cancelled: nobody answers them any more.
-- name: CancelCardDecisions :many
UPDATE inbox_item SET status = 'cancelled', resolved_at = now(), touched_at = now()
WHERE workspace_id = @workspace_id AND card_id = @card_id::uuid AND status = 'open' AND resolved_at IS NULL
  AND jsonb_array_length(options) > 0
RETURNING id;
