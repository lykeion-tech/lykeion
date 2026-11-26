-- Run with psql -X -v ON_ERROR_STOP=1 and PGOPTIONS='-c app.workspace_id=<uuid>'.
-- Use an authorized read-only role with access to that workspace. Missing
-- workspace context returns no rows, even for a role that can bypass RLS.
-- Week is the study's creation cohort (Monday UTC). Each run is a current
-- snapshot of that cohort, including later corrections. Totals are per cohort and across the workspace.
BEGIN READ ONLY;

WITH RECURSIVE
studies AS (
    SELECT workspace_id, id AS card_id, board_column, scope_to, state, round,
           date_trunc('week', created_at AT TIME ZONE 'UTC')::date AS week
    FROM card
    WHERE workspace_id = app_workspace()
),
history AS (
    SELECT r.*
    FROM record r
    JOIN studies s USING (workspace_id, card_id)
    WHERE r.kind IN ('hand_in', 'value', 'assessment', 'review', 'server_checks',
                     'outcome', 'hypothesis', 'withdrawn', 'validation_status', 'rerun', 'run')
),
runs AS (
    SELECT a.*
    FROM agent_run a
    JOIN studies s USING (workspace_id, card_id)
),
hand_ins AS (
    SELECT h.*
    FROM history h
    JOIN runs a ON (a.workspace_id, a.card_id, a.id, a.board_column, a.round, a.role)
                 = (h.workspace_id, h.card_id, h.agent_run_id, h.board_column, h.round, h.role)
    WHERE h.kind = 'hand_in' AND h.role IN ('leader', 'agent')
      AND h.board_column IN ('experimentation', 'validation')
      AND h.body #>> '{hand_in,status}' = 'done'
      AND h.body->>'attempt' = a.attempt::text AND a.status = 'completed'
      AND (h.superseded_at IS NULL OR a.completed_at < h.superseded_at)
),
submitted AS (
    SELECT r.*, h.id AS hand_in_id,
           btrim(regexp_replace(lower(r.body->>'name'), '[[:space:]]+', ' ', 'g')) AS name,
           jsonb_typeof(r.body->'field') = 'string'
               AND jsonb_typeof(r.body->'measure') = 'string' AS complete_binding
    FROM hand_ins h
    JOIN history r USING (workspace_id, card_id, board_column, round, agent_run_id, role)
    WHERE r.kind IN ('value', 'assessment') AND (
        h.body->'converted_records' ? r.id::text
        -- Early hand-ins did not store converted IDs. Match a live sibling
        -- from the same completed lead run and submission transaction.
        OR (NOT h.body ? 'converted_records' AND r.created_at = h.created_at
            AND r.superseded_at IS NOT DISTINCT FROM h.superseded_at)
    )
),
numeric_values AS (
    SELECT v.*
    FROM submitted v
    WHERE v.kind = 'value' AND v.board_column = 'experimentation'
      AND octet_length(btrim(v.body->>'value')) <= 256
      AND btrim(v.body->>'value') ~ '^-?[0-9]+([.][0-9]+)?([eE][+-]?0*[0-9]{1,4})?$'
),
value_checks AS (
    SELECT r.workspace_id, r.card_id, r.round, r.kind, r.body->>'decision' AS decision, r.created_at,
           i->>'key' AS value_key, i->>'identity' AS identity, i->>'label' AS label,
           i->>'status' AS status,
           checks.bound_fail, checks.measure_fail, checks.scale_fail, checks.binding_pass
    FROM history r
    CROSS JOIN LATERAL jsonb_array_elements(COALESCE(
        NULLIF(r.body->'items', 'null'), NULLIF(r.body #> '{checks,items}', 'null'), '[]')) i
    CROSS JOIN LATERAL (
        SELECT bool_or(c->>'id' = 'bound' AND c->>'status' = 'fail') AS bound_fail,
               bool_or(c->>'id' = 'measure' AND c->>'status' = 'fail') AS measure_fail,
               bool_or(c->>'id' = 'scale' AND c->>'status' = 'fail') AS scale_fail,
               bool_or(c->>'id' = 'bound' AND c->>'status' = 'pass')
                 AND bool_or(c->>'id' = 'measure' AND c->>'status' = 'pass')
                 AND bool_and(c->>'status' = 'pass') AS binding_pass
        FROM jsonb_array_elements(COALESCE(NULLIF(i->'checks', 'null'), '[]')) c
        WHERE c->>'by' = 'server' AND c->>'id' IN ('bound', 'measure', 'scale')
          AND c->>'warning' IS DISTINCT FROM 'true'
    ) checks
    WHERE r.board_column = 'experimentation' AND r.kind IN ('review', 'server_checks')
      AND r.role = 'server' AND i->>'kind' = 'value'
),
-- Follow only a settled lead's explicit corrected reply to the immediately
-- preceding final review, with a uniquely identified replacement of the
-- same name in that hand-in. Match the stored identity, label, name or alias.
replacements AS (
    SELECT DISTINCT n.workspace_id, n.card_id, n.id AS replacement_id, linked.old_id
    FROM numeric_values n
    JOIN hand_ins h ON h.id = n.hand_in_id AND h.workspace_id = n.workspace_id
    CROSS JOIN LATERAL (
        SELECT (array_agg(DISTINCT old.id))[1] AS old_id
        FROM value_checks c
        JOIN numeric_values old USING (workspace_id, card_id)
        CROSS JOIN LATERAL jsonb_array_elements(
            COALESCE(NULLIF(h.body #> '{hand_in,replies}', 'null'), '[]')) reply
        WHERE (c.workspace_id, c.card_id, c.round) = (n.workspace_id, n.card_id, n.round - 1)
          AND c.kind = 'review' AND c.status = 'fail' AND c.value_key = old.key
          AND old.round < n.round AND old.name = n.name AND reply->>'action' = 'corrected'
          AND btrim(regexp_replace(lower(reply->>'item'), '[[:space:]]+', ' ', 'g')) IN (
              old.name,
              btrim(regexp_replace(lower(c.identity), '[[:space:]]+', ' ', 'g')),
              btrim(regexp_replace(lower(c.label), '[[:space:]]+', ' ', 'g')),
              CASE WHEN length(c.identity) > 2000 THEN 'retry-ref-' || left(encode(sha256(
                  convert_to(c.card_id::text, 'UTF8') || decode('00', 'hex') ||
                  convert_to('experimentation', 'UTF8') || decode('00', 'hex') ||
                  convert_to(c.round::text, 'UTF8') || decode('00', 'hex') ||
                  convert_to(old.key, 'UTF8') || decode('00', 'hex') ||
                  convert_to(c.identity, 'UTF8')), 'hex'), 32) END)
        HAVING count(DISTINCT old.id) = 1
    ) linked
    WHERE NOT EXISTS (
        SELECT FROM numeric_values other
        WHERE other.workspace_id = n.workspace_id AND other.hand_in_id = n.hand_in_id
          AND other.name = n.name AND other.id <> n.id
    )
),
value_lineage AS (
    SELECT v.workspace_id, v.card_id, v.id AS root, v.id AS member
    FROM numeric_values v
    WHERE NOT EXISTS (SELECT FROM replacements r WHERE r.workspace_id = v.workspace_id AND r.replacement_id = v.id)
    UNION ALL
    SELECT l.workspace_id, l.card_id, l.root, r.replacement_id
    FROM value_lineage l
    JOIN replacements r ON (r.workspace_id, r.card_id, r.old_id) = (l.workspace_id, l.card_id, l.member)
),
value_events AS (
    SELECT l.workspace_id, l.card_id, l.root,
           min(c.round) FILTER (WHERE c.decision IN ('redo', 'block') AND c.status = 'fail'
               AND (c.bound_fail OR c.measure_fail OR c.scale_fail)) AS blocked_round,
           bool_or(c.bound_fail) FILTER (WHERE c.decision IN ('redo', 'block') AND c.status = 'fail') AS bound_block,
           bool_or(c.measure_fail) FILTER (WHERE c.decision IN ('redo', 'block') AND c.status = 'fail') AS measure_block,
           bool_or(c.scale_fail) FILTER (WHERE c.decision IN ('redo', 'block') AND c.status = 'fail') AS scale_block,
           max(c.round) FILTER (WHERE c.binding_pass AND v.complete_binding
               AND (l.root <> l.member OR c.round > v.round)) AS binding_pass_round,
           max(c.round) FILTER (WHERE c.kind = 'review' AND c.status = 'withdrawn') AS withdrawn_round
    FROM value_lineage l
    JOIN numeric_values v ON (v.workspace_id, v.card_id, v.id) = (l.workspace_id, l.card_id, l.member)
    LEFT JOIN value_checks c ON (c.workspace_id, c.card_id, c.value_key) = (v.workspace_id, v.card_id, v.key)
    GROUP BY l.workspace_id, l.card_id, l.root
),
value_totals AS (
    SELECT e.workspace_id, e.card_id,
           count(*) FILTER (WHERE v.complete_binding) AS bound,
           count(*) FILTER (WHERE v.complete_binding IS NOT TRUE) AS unbound,
           count(*) FILTER (WHERE e.blocked_round IS NOT NULL) AS binding_blocked,
           count(*) FILTER (WHERE e.bound_block) AS blocked_by_bound,
           count(*) FILTER (WHERE e.measure_block) AS blocked_by_measure,
           count(*) FILTER (WHERE e.scale_block) AS blocked_by_scale,
           count(*) FILTER (WHERE e.binding_pass_round > e.blocked_round) AS binding_blocked_then_corrected,
           count(*) FILTER (WHERE e.withdrawn_round > e.blocked_round
               AND (e.binding_pass_round > e.blocked_round) IS NOT TRUE) AS binding_blocked_then_withdrawn,
           count(*) FILTER (WHERE e.blocked_round IS NOT NULL
               AND (e.binding_pass_round > e.blocked_round) IS NOT TRUE
               AND (e.withdrawn_round > e.blocked_round) IS NOT TRUE) AS binding_blocked_unresolved
    FROM value_events e
    JOIN numeric_values v ON (v.workspace_id, v.card_id, v.id) = (e.workspace_id, e.card_id, e.root)
    GROUP BY e.workspace_id, e.card_id
),
-- Use the most recent Hypotheses decision as of the assessment, including
-- carried and keyless-approved declarations. Use pre-objector code statuses
-- to measure the comparison itself, even when judgment has not settled.
frozen_assessments AS (
    SELECT DISTINCT ON (a.workspace_id, a.card_id, a.round, h.key)
           a.workspace_id, a.card_id, a.round, h.key AS hypothesis_key, a.body, a.created_at
    FROM submitted a
    CROSS JOIN LATERAL (
        SELECT o.round, o.created_at, o.body
        FROM history o
        WHERE (o.workspace_id, o.card_id) = (a.workspace_id, a.card_id)
          AND o.board_column = 'hypotheses' AND o.kind = 'outcome' AND o.role = 'server'
          AND o.round < a.round AND o.created_at <= a.created_at
          AND (o.superseded_at IS NULL OR o.superseded_at > a.created_at)
        ORDER BY o.round DESC, o.created_at DESC, o.id DESC LIMIT 1
    ) o
    CROSS JOIN LATERAL (
        SELECT CASE WHEN r.kind = 'review' THEN r.body ELSE r.body->'checks' END AS result
        FROM history r
        WHERE (r.workspace_id, r.card_id, r.round) = (a.workspace_id, a.card_id, o.round)
          AND r.board_column = 'hypotheses' AND r.role = 'server'
          AND r.created_at <= o.created_at AND (r.superseded_at IS NULL OR r.superseded_at > a.created_at)
          AND ((o.body->>'decision_id' IS NULL AND r.kind = 'review')
            OR (r.kind = 'server_checks' AND r.body->>'complete' = 'true'
                AND r.body->>'approval_decision' = o.body->>'decision_id'))
        ORDER BY r.created_at DESC, r.id DESC LIMIT 1
    ) review
    CROSS JOIN LATERAL jsonb_array_elements(COALESCE(NULLIF(review.result->'items', 'null'), '[]')) item
    JOIN history h ON (h.workspace_id, h.card_id, h.key) = (a.workspace_id, a.card_id, item->>'key')
    WHERE a.kind = 'assessment' AND a.board_column = 'validation'
      AND o.body->>'verdict' IN ('pass', 'carry', 'approved_by_owner')
      AND item->>'kind' = 'hypothesis' AND item->>'status' IN ('pass', 'unclear')
      AND h.board_column = 'hypotheses' AND h.kind = 'hypothesis' AND h.role IN ('leader', 'agent')
      AND h.created_at <= o.created_at AND (h.superseded_at IS NULL OR h.superseded_at > a.created_at)
      AND jsonb_typeof(h.body->'comparison') = 'object'
      AND btrim(h.body->>'text') = btrim(a.body->>'hypothesis')
      AND NOT EXISTS (
          SELECT FROM history w
          WHERE (w.workspace_id, w.card_id, w.board_column) = (h.workspace_id, h.card_id, h.board_column)
            AND w.kind = 'withdrawn' AND w.body->>'item' = h.key AND w.created_at <= a.created_at
            AND (w.superseded_at IS NULL OR w.superseded_at > a.created_at))
    ORDER BY a.workspace_id, a.card_id, a.round, h.key, a.created_at DESC, a.id DESC
),
validation AS (
    SELECT a.*, COALESCE(status.code, 'pending') AS code,
           replay.body #>> ARRAY['unavailable', a.body->>'measurement'] = 'true'
               OR replay.body #>> ARRAY['unavailable', a.body->>'control'] = 'true' AS legacy_unavailable
    FROM frozen_assessments a
    LEFT JOIN LATERAL (
        SELECT s.body->>'status' AS code
        FROM history s
        WHERE (s.workspace_id, s.card_id, s.round) = (a.workspace_id, a.card_id, a.round)
          AND s.board_column = 'validation' AND s.kind = 'validation_status' AND s.role = 'server'
          AND s.body->>'final' = 'false' AND s.created_at >= a.created_at
          AND btrim(s.body->>'hypothesis') = btrim(a.body->>'hypothesis')
        ORDER BY s.created_at DESC, s.id DESC LIMIT 1
    ) status ON true
    LEFT JOIN LATERAL (
        SELECT r.body
        FROM history r
        WHERE (r.workspace_id, r.card_id, r.round) = (a.workspace_id, a.card_id, a.round)
          AND r.board_column = 'validation' AND r.kind = 'rerun' AND r.role = 'server'
        ORDER BY r.created_at DESC, r.id DESC LIMIT 1
    ) replay ON true
),
validation_totals AS (
    SELECT v.workspace_id, v.card_id,
           count(*) FILTER (WHERE v.code IN ('supported', 'refuted', 'caveated')) AS comparison_valid,
           count(*) FILTER (WHERE v.code = 'invalid') AS comparison_invalid,
           count(*) FILTER (WHERE v.code = 'invalid' AND EXISTS (
               SELECT FROM validation later
               WHERE (later.workspace_id, later.card_id, later.hypothesis_key)
                   = (v.workspace_id, v.card_id, v.hypothesis_key)
                 AND later.round > v.round AND later.code IN ('supported', 'refuted', 'caveated')
           )) AS comparison_invalid_then_corrected,
           count(*) FILTER (WHERE v.code = 'inconclusive') AS comparison_inconclusive,
           count(*) FILTER (WHERE v.code = 'inconclusive' AND v.legacy_unavailable) AS comparison_inconclusive_older_daemon,
           count(*) FILTER (WHERE v.code = 'pending') AS comparison_pending
    FROM validation v GROUP BY v.workspace_id, v.card_id
),
run_totals AS (
    SELECT a.workspace_id, a.card_id,
           count(DISTINCT a.round) FILTER (WHERE a.board_column = 'experimentation') AS experimentation_rounds,
           count(DISTINCT a.round) FILTER (WHERE a.board_column = 'validation') AS validation_rounds,
           max(array_position(ARRAY['grounding','hypotheses','experimentation','validation'],
               a.board_column)) AS stage_index,
           count(*) FILTER (WHERE a.kind = 'rerun' AND a.status = 'queued'
               AND a.requires_output_attestation) AS queued_attestation_replays,
           count(*) FILTER (WHERE a.status = 'completed' AND a.board_column = 'experimentation' AND EXISTS (
               SELECT FROM history r
               WHERE (r.workspace_id, r.card_id, r.agent_run_id) = (a.workspace_id, a.card_id, a.id)
                 AND r.kind = 'run' AND r.body->>'tool' = 'run_script'
                 AND r.body->'output_hashes' <> '{}'::jsonb AND jsonb_typeof(r.body->'output_hashes') = 'object'
           )) AS attested_experimentation_runs
    FROM runs a GROUP BY a.workspace_id, a.card_id
),
replay_totals AS (
    SELECT r.workspace_id, r.card_id,
           count(*) FILTER (WHERE r.body->>'scoped_outputs' = 'true') AS scoped_replays,
           count(*) FILTER (WHERE r.body->>'scoped_outputs' IS DISTINCT FROM 'true') AS legacy_or_unmarked_replays
    FROM history r
    JOIN runs a ON (a.workspace_id, a.card_id, a.id) = (r.workspace_id, r.card_id, r.agent_run_id)
    WHERE r.kind = 'rerun' AND r.role = 'server' AND r.board_column = 'validation'
      AND a.kind = 'rerun' AND a.status = 'completed'
    GROUP BY r.workspace_id, r.card_id
)
SELECT s.workspace_id, s.week, s.card_id AS study_id,
       CASE WHEN grouping(s.week) = 1 THEN 'workspace'
            WHEN grouping(s.card_id) = 1 THEN 'workspace_week' ELSE 'study' END AS row_kind,
       count(*) AS studies,
       CASE WHEN grouping(s.card_id) = 0 THEN max(s.board_column) END AS current_stage,
       CASE WHEN grouping(s.card_id) = 0 THEN
           (ARRAY['grounding','hypotheses','experimentation','validation'])[max(greatest(u.stage_index,
               array_position(ARRAY['grounding','hypotheses','experimentation','validation'], CASE WHEN s.board_column = 'done' AND s.state = 'done' THEN s.scope_to ELSE s.board_column END)))]
       END AS stage_reached,
       CASE WHEN grouping(s.card_id) = 0 THEN max(s.round) END AS round,
       coalesce(sum(u.experimentation_rounds), 0)::bigint AS experimentation_rounds,
       coalesce(sum(u.validation_rounds), 0)::bigint AS validation_rounds,
       NULL::text AS daemon_version, -- daemon binary release is not recorded per run
       coalesce(sum(u.attested_experimentation_runs), 0)::bigint AS attested_experimentation_runs,
       coalesce(sum(u.queued_attestation_replays), 0)::bigint AS queued_attestation_replays,
       coalesce(sum(r.scoped_replays), 0)::bigint AS scoped_replays,
       coalesce(sum(r.legacy_or_unmarked_replays), 0)::bigint AS legacy_or_unmarked_replays,
       sum((NOT EXISTS (SELECT FROM hand_ins h WHERE (h.workspace_id, h.card_id) = (s.workspace_id, s.card_id)
           AND h.board_column = 'experimentation'))::int) AS studies_without_settled_experimentation,
       coalesce(sum(v.bound), 0)::bigint AS bound,
       coalesce(sum(v.unbound), 0)::bigint AS unbound,
       coalesce(sum(v.binding_blocked), 0)::bigint AS binding_blocked,
       coalesce(sum(v.blocked_by_bound), 0)::bigint AS blocked_by_bound,
       coalesce(sum(v.blocked_by_measure), 0)::bigint AS blocked_by_measure,
       coalesce(sum(v.blocked_by_scale), 0)::bigint AS blocked_by_scale,
       coalesce(sum(v.binding_blocked_then_corrected), 0)::bigint AS binding_blocked_then_corrected,
       coalesce(sum(v.binding_blocked_then_withdrawn), 0)::bigint AS binding_blocked_then_withdrawn,
       coalesce(sum(v.binding_blocked_unresolved), 0)::bigint AS binding_blocked_unresolved,
       coalesce(sum(t.comparison_valid), 0)::bigint AS comparison_valid,
       coalesce(sum(t.comparison_invalid), 0)::bigint AS comparison_invalid,
       coalesce(sum(t.comparison_invalid_then_corrected), 0)::bigint AS comparison_invalid_then_corrected,
       coalesce(sum(t.comparison_inconclusive), 0)::bigint AS comparison_inconclusive,
       coalesce(sum(t.comparison_inconclusive_older_daemon), 0)::bigint AS comparison_inconclusive_older_daemon,
       coalesce(sum(t.comparison_pending), 0)::bigint AS comparison_pending
FROM studies s
LEFT JOIN value_totals v USING (workspace_id, card_id)
LEFT JOIN validation_totals t USING (workspace_id, card_id)
LEFT JOIN run_totals u USING (workspace_id, card_id)
LEFT JOIN replay_totals r USING (workspace_id, card_id)
GROUP BY GROUPING SETS ((s.workspace_id, s.week, s.card_id), (s.workspace_id, s.week), (s.workspace_id))
ORDER BY s.week NULLS LAST, s.card_id NULLS LAST;

ROLLBACK;
