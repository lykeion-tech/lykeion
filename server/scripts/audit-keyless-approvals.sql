-- Run under a role and workspace context authorized to read the study.
-- One row per approved stage. The historical match uses the transaction
-- timestamp shared by DecideItem and the outcome inserted by that decision.
BEGIN READ ONLY;

SELECT r.workspace_id,
       c.id AS study_id,
       c.number AS study_number,
       c.title AS study_title,
       r.board_column AS stage,
       r.round,
       r.created_at AS approved_at,
       i.id AS decision_id,
       i.decided_by AS approved_by
FROM record AS r
JOIN card AS c ON c.workspace_id = r.workspace_id AND c.id = r.card_id
JOIN inbox_item AS i ON i.workspace_id = r.workspace_id
                    AND i.card_id = r.card_id
                    AND i.kind = 'needs_decision'
                    AND i.cause = 'no_jev'
                    AND i.chosen_option = 'approve'
                    AND i.status = 'decided'
                    AND i.decided_by::text = r.body->>'by'
                    AND (
                      i.id::text = r.body->>'decision_id'
                      OR (
                        r.body->>'decision_id' IS NULL
                        AND i.decided_at = r.created_at
                      )
                    )
WHERE r.kind = 'outcome'
  AND r.role = 'server'
  AND r.body->>'verdict' = 'approved_by_owner'
ORDER BY r.created_at DESC, r.workspace_id, c.number, r.round;

ROLLBACK;
