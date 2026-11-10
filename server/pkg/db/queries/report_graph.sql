-- name: InsertReportBuild :one
INSERT INTO report_build (workspace_id, card_id, agent_run_id, status, checks_status)
VALUES (@workspace_id, @card_id, sqlc.narg(agent_run_id), @status, @checks_status)
RETURNING *;

-- name: GetReportBuild :one
SELECT * FROM report_build WHERE workspace_id = @workspace_id AND card_id = @card_id;

-- A build that settles (done, failed or skipped) is stamped finished.
-- name: SetReportBuildStatus :execrows
UPDATE report_build SET status = @status,
  finished_at = CASE WHEN @status::text IN ('done', 'failed', 'skipped') THEN now() ELSE finished_at END
WHERE workspace_id = @workspace_id AND card_id = @card_id;

-- name: SetReportChecksStatus :execrows
UPDATE report_build SET checks_status = @checks_status
WHERE workspace_id = @workspace_id AND card_id = @card_id;

-- Builds a restart left unfinished: judging with a settled run, or checks
-- still pending, once they are two minutes old.
-- name: ReportBuildsToResume :many
SELECT b.* FROM report_build b
LEFT JOIN agent_run r ON r.id = b.agent_run_id AND r.workspace_id = b.workspace_id
WHERE b.workspace_id = @workspace_id AND b.created_at < now() - interval '2 minutes'
  AND ((b.status = 'judging' AND r.status IN ('completed', 'failed', 'cancelled'))
    OR b.checks_status = 'pending')
ORDER BY b.created_at;

-- name: DeleteReportConcepts :exec
DELETE FROM report_concept WHERE workspace_id = @workspace_id AND card_id = @card_id;

-- name: DeleteReportFacts :exec
DELETE FROM report_fact WHERE workspace_id = @workspace_id AND card_id = @card_id;

-- name: DeleteReportLinks :exec
DELETE FROM report_link WHERE workspace_id = @workspace_id AND card_id = @card_id;

-- name: DeleteReportChecks :exec
DELETE FROM report_check WHERE workspace_id = @workspace_id AND card_id = @card_id;

-- name: InsertReportConcept :exec
INSERT INTO report_concept (workspace_id, card_id, key, label, type)
VALUES (@workspace_id, @card_id, @key, @label, @type);

-- name: InsertReportFact :exec
INSERT INTO report_fact (workspace_id, card_id, subject, relation, object, quote, source_node, p)
VALUES (@workspace_id, @card_id, @subject, @relation, @object, @quote, @source_node, @p);

-- name: InsertReportLink :exec
INSERT INTO report_link (workspace_id, card_id, start_byte, end_byte, node_id, p)
VALUES (@workspace_id, @card_id, @start_byte, @end_byte, @node_id, @p);

-- name: InsertReportCheck :exec
INSERT INTO report_check (workspace_id, card_id, node_id, check_id, question, choice, p, status, evidence)
VALUES (@workspace_id, @card_id, @node_id, @check_id, @question, @choice, @p, @status, @evidence);

-- name: ListReportConcepts :many
SELECT * FROM report_concept WHERE workspace_id = @workspace_id AND card_id = @card_id ORDER BY key;

-- name: ListReportFacts :many
SELECT * FROM report_fact WHERE workspace_id = @workspace_id AND card_id = @card_id ORDER BY subject, relation, object, id;

-- name: ListReportLinks :many
SELECT * FROM report_link WHERE workspace_id = @workspace_id AND card_id = @card_id ORDER BY start_byte;

-- name: ListReportChecks :many
SELECT * FROM report_check WHERE workspace_id = @workspace_id AND card_id = @card_id ORDER BY node_id, check_id;

-- A report run is like a role run, in the final run's column, queued for the
-- card owner on the runtime that ran the study's final run.
-- name: InsertReportAgentRun :one
INSERT INTO agent_run (workspace_id, kind, card_id, responsible_user_id, agent_id, board_column, round, role, harness,
  pinned_runtime_id, prompt, skill, continuity_scope)
SELECT c.workspace_id, 'report', c.id, c.owner_id, sqlc.narg(agent_id), @board_column, @round, 'reporter', @harness,
  @pinned_runtime_id, @prompt, @skill, 'report:' || c.id::text
FROM card c WHERE c.workspace_id = @workspace_id AND c.id = @card_id
RETURNING *;
