-- All relations are explicitly workspace-scoped in addition to RLS. These are
-- lab facts only: no Coresearch relation or run transcript is read.
-- Shared citation baseline: assigned Team id/name/stage/column are visible in
-- the study directory to every member, regardless of definition visibility.
-- Only active shipped/workspace-shared Team definitions enter memory. No
-- owner-only supplement may be added: Read and maintenance Version must identify
-- the same evidence. Private definitions need a separate future scope/reference
-- contract and must never feed workspace-shared lessons.
-- name: LabFact :one
WITH readable_records AS NOT MATERIALIZED (
 SELECT r.* FROM record r
 WHERE r.workspace_id=sqlc.arg(workspace_id)::uuid AND
 (COALESCE(r.body->>'server','') <> 'attachments' OR EXISTS (
   SELECT 1 FROM attachment a WHERE a.workspace_id=r.workspace_id AND a.card_id=r.card_id
   AND a.record_key=r.key AND a.kind='file' AND a.blob_key=a.workspace_id::text||'/attachments/'||a.id::text
   AND (r.blob_key='' OR r.blob_key=a.blob_key||'.text')))
)
SELECT c.title::text AS title, jsonb_build_object('study',jsonb_build_object('id',c.id,'number',c.number,'title',c.title,'question',c.question,'owner_id',c.owner_id,'deadline',c.deadline,'start_date',c.start_date,'board_column',c.board_column,'state',c.state,'flag',c.flag,'ending',c.ending,'end_reason',c.end_reason,'rounds',c.rounds,'round',c.round,'version',c.version,'created_at',c.created_at,'handoff_note',c.handoff_note,'research_id',c.research_id,'assignee_type',c.assignee_type,'assignee_id',c.assignee_id,'origin',c.origin,'block_kind',c.block_kind,'scope_from',c.scope_from,'scope_to',c.scope_to,'redos',c.redos,'workflow',c.workflow,'workflow_release',c.workflow_release),
 'attachments',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',a.id,'name',a.name,'content_type',a.content_type,'size_bytes',a.size_bytes,'record_key',a.record_key) ORDER BY a.id) FROM attachment a WHERE a.workspace_id=c.workspace_id AND a.card_id=c.id AND a.kind='file' AND a.blob_key NOT LIKE c.workspace_id::text||'/coresearch-%'),'[]'::jsonb),
 'runs',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',r.id,'kind',r.kind,'role',r.role,'board_column',r.board_column,'round',r.round,'status',r.status,'created_at',r.created_at) ORDER BY r.id) FROM agent_run r WHERE r.workspace_id=c.workspace_id AND r.card_id=c.id AND r.kind IN ('role','rerun')),'[]'::jsonb),
 'teams',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',p.id,'column',ct.board_column,'name',p.name,'stage',p.stage) || CASE WHEN p.deleted_at IS NULL AND (p.source='shipped' OR p.visibility='workspace') THEN jsonb_build_object('manifest',p.manifest) ELSE '{}'::jsonb END ORDER BY ct.board_column,p.id) FROM card_team ct JOIN field_pack p ON p.workspace_id=ct.workspace_id AND p.id=ct.pack_id WHERE ct.workspace_id=c.workspace_id AND ct.card_id=c.id),'[]'::jsonb))::text AS text,
 c.version::text AS version, false AS superseded,
 COALESCE((SELECT jsonb_agg(x.ref ORDER BY x.ref) FROM (
 SELECT 'record:'||r.id::text AS ref FROM readable_records r WHERE r.workspace_id=c.workspace_id AND r.card_id=c.id
 UNION ALL SELECT 'comment:'||m.id::text FROM comment m WHERE m.workspace_id=c.workspace_id AND m.card_id=c.id
 UNION ALL SELECT 'decision:'||i.id::text FROM inbox_item i WHERE i.workspace_id=c.workspace_id AND i.card_id=c.id AND (jsonb_array_length(i.options)>0 OR i.question_set IS NOT NULL)
 ) x),'[]'::jsonb)::jsonb AS links, '[]'::jsonb AS cites
FROM card c WHERE @kind::text='study' AND c.workspace_id=sqlc.arg(workspace_id)::uuid AND c.id=sqlc.arg(id)::uuid
UNION ALL
SELECT r.kind, to_jsonb(r)::text, CASE WHEN r.superseded_at IS NULL THEN 'live' ELSE 'superseded' END, r.superseded_at IS NOT NULL,
 jsonb_build_array('study:'||r.card_id::text), '[]'::jsonb
FROM readable_records r JOIN card c ON c.workspace_id=r.workspace_id AND c.id=r.card_id
WHERE @kind='record' AND r.workspace_id=sqlc.arg(workspace_id)::uuid AND r.id=sqlc.arg(id)::uuid
UNION ALL
SELECT 'Study comment', to_jsonb(c)::text, CASE WHEN EXISTS(SELECT 1 FROM comment_cancellation x WHERE x.workspace_id=c.workspace_id AND x.comment_id=c.id) THEN 'cancelled' ELSE 'live' END,
 EXISTS(SELECT 1 FROM comment_cancellation x WHERE x.workspace_id=c.workspace_id AND x.comment_id=c.id), jsonb_build_array('study:'||c.card_id::text), '[]'::jsonb
FROM comment c JOIN card s ON s.workspace_id=c.workspace_id AND s.id=c.card_id
WHERE @kind='comment' AND c.workspace_id=sqlc.arg(workspace_id)::uuid AND c.id=sqlc.arg(id)::uuid
UNION ALL
SELECT 'Decision', to_jsonb(i)::text, i.status, false, jsonb_build_array('study:'||i.card_id::text), '[]'::jsonb
FROM inbox_item i JOIN card c ON c.workspace_id=i.workspace_id AND c.id=i.card_id
WHERE @kind='decision' AND i.workspace_id=sqlc.arg(workspace_id)::uuid AND i.id=sqlc.arg(id)::uuid AND (jsonb_array_length(i.options)>0 OR i.question_set IS NOT NULL)
UNION ALL
SELECT 'Lab lesson', l.text, l.written_at::text, false, '[]'::jsonb, l.cites
FROM lesson l WHERE @kind='lesson' AND l.workspace_id=sqlc.arg(workspace_id)::uuid AND l.id=sqlc.arg(id)::uuid AND l.status='active' AND l.checked_at IS NOT NULL
UNION ALL
SELECT w.name, jsonb_build_object('research_context',w.research_context,
 'workflows',COALESCE((SELECT jsonb_agg(DISTINCT jsonb_build_object('workflow',c.workflow,'release',c.workflow_release) ORDER BY jsonb_build_object('workflow',c.workflow,'release',c.workflow_release)) FROM card c WHERE c.workspace_id=w.id AND c.workflow IS NOT NULL),'[]'::jsonb),
 'teams',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',p.id,'name',p.name,'manifest',p.manifest) ORDER BY p.id) FROM field_pack p WHERE p.workspace_id=w.id AND p.deleted_at IS NULL AND (p.source='shipped' OR p.visibility='workspace') AND EXISTS(SELECT 1 FROM card_team ct WHERE ct.workspace_id=p.workspace_id AND ct.pack_id=p.id)),'[]'::jsonb))::text,
 md5(w.research_context), false, '[]'::jsonb, '[]'::jsonb
FROM workspace w WHERE @kind='workspace' AND w.id=sqlc.arg(workspace_id)::uuid AND (sqlc.arg(id)::uuid='00000000-0000-0000-0000-000000000000'::uuid OR w.id=sqlc.arg(id)::uuid);

-- Bind the claimed asker to the actual transaction identity and workspace.
-- name: LabAsker :one
SELECT EXISTS(SELECT 1 FROM member WHERE workspace_id=sqlc.arg(workspace_id)::uuid
 AND workspace_id=app_workspace() AND user_id=sqlc.arg(user_id)::uuid AND user_id=app_user())::boolean;

-- name: AppendLabRead :exec
INSERT INTO lab_read(workspace_id,agent_run_id,ref,via,read_version,attempt) VALUES(@workspace_id,@agent_run_id,@ref,@via,@read_version,
 (SELECT r.attempt FROM agent_run r WHERE r.workspace_id=sqlc.arg(workspace_id)::uuid AND r.id=sqlc.arg(agent_run_id)::uuid));

-- The run may discover only its own requester before switching identity.
-- name: LabRunRequester :one
SELECT r.requester_id FROM agent_run r JOIN lab_chat c
  ON c.workspace_id=r.workspace_id AND c.id=r.lab_chat_id AND c.owner_id=r.requester_id
WHERE r.workspace_id = @workspace_id AND r.id = @id AND r.kind='aristotle' AND r.status='running';

-- Navigation only. This directory is not part of the workspace fact or its
-- citation version. Each candidate is re-read and logged under the asker.
-- name: LabStudyDirectory :many
SELECT id FROM card WHERE workspace_id = @workspace_id AND id > @after_id::uuid ORDER BY id LIMIT @page_size::integer;

-- name: LabLessonDirectory :many
SELECT id,directory_key FROM lesson WHERE workspace_id = @workspace_id AND status='active'
  AND checked_at IS NOT NULL AND directory_key > @after_key::uuid
  ORDER BY directory_key LIMIT @page_size::integer;

-- name: LabStudyOpen :one
SELECT EXISTS(SELECT 1 FROM card WHERE workspace_id = @workspace_id AND id = @card_id)::boolean;

-- Keep traversal bounded even for a study with thousands of records. Each
-- table supplies at most the page size before the three result sets are merged.
-- name: LabStudyLinks :many
SELECT link.kind::text AS kind, link.id::uuid AS id FROM (
 SELECT * FROM (SELECT 'record'::text AS kind,r.id FROM record r
   WHERE r.workspace_id = @workspace_id AND r.card_id = @card_id
   AND (r.id,'record'::text) > (@after_id::uuid,@after_kind::text)
   AND (COALESCE(r.body->>'server','') <> 'attachments' OR EXISTS (
     SELECT 1 FROM attachment a WHERE a.workspace_id=r.workspace_id AND a.card_id=r.card_id
     AND a.record_key=r.key AND a.kind='file' AND a.blob_key=a.workspace_id::text||'/attachments/'||a.id::text
     AND (r.blob_key='' OR r.blob_key=a.blob_key||'.text')))
   ORDER BY r.id LIMIT @page_size::integer) records
 UNION ALL SELECT * FROM (SELECT 'comment'::text AS kind,c.id FROM comment c
   WHERE c.workspace_id = @workspace_id AND c.card_id = @card_id
   AND (c.id,'comment'::text) > (@after_id::uuid,@after_kind::text)
   ORDER BY c.id LIMIT @page_size::integer) comments
 UNION ALL SELECT * FROM (SELECT 'decision'::text AS kind,i.id FROM inbox_item i
   WHERE i.workspace_id = @workspace_id AND i.card_id = @card_id
   AND (i.id,'decision'::text) > (@after_id::uuid,@after_kind::text)
   AND (jsonb_array_length(i.options)>0 OR i.question_set IS NOT NULL)
   ORDER BY i.id LIMIT @page_size::integer) decisions
) link ORDER BY link.id,link.kind LIMIT @page_size::integer;

-- name: CreateLabChat :one
INSERT INTO lab_chat(workspace_id,owner_id) VALUES(app_workspace(),app_user()) RETURNING *;

-- name: GetPrivateLabChat :one
SELECT * FROM lab_chat WHERE workspace_id=app_workspace() AND owner_id=app_user() AND id= @id;

-- name: ListPrivateLabChats :many
SELECT * FROM lab_chat WHERE workspace_id=app_workspace() AND owner_id=app_user()
AND (sqlc.narg(before_time)::timestamptz IS NULL OR (updated_at,id)<(sqlc.narg(before_time),@before_id::uuid))
ORDER BY updated_at DESC,id DESC LIMIT @page_size::int;

-- name: TouchLabChat :exec
UPDATE lab_chat SET updated_at=clock_timestamp(),title=CASE WHEN title='' THEN @title::text ELSE title END
WHERE workspace_id=app_workspace() AND owner_id=app_user() AND id= @id;

-- name: ListPrivateLabMessages :many
SELECT * FROM lab_message WHERE workspace_id=app_workspace() AND owner_id=app_user() AND chat_id= @chat_id
AND (sqlc.narg(before_time)::timestamptz IS NULL OR (created_at,id)<(sqlc.narg(before_time),@before_id::uuid))
ORDER BY created_at DESC,id DESC LIMIT @page_size::int;

-- name: GetLabQuestion :one
SELECT * FROM lab_message WHERE workspace_id=app_workspace() AND owner_id=app_user()
AND agent_run_id= @run_id::uuid AND kind='question';

-- name: GetPrivateAnswerSources :one
SELECT m.id,m.kind,q.launch_sources,''::text AS question,m.statements,m.evidence,COALESCE(m.history_safe,false)::boolean AS history_safe
FROM lab_message m JOIN lab_message q ON q.workspace_id=m.workspace_id AND q.chat_id=m.chat_id
AND q.owner_id=m.owner_id AND q.agent_run_id=m.agent_run_id AND q.kind='question'
WHERE m.workspace_id=app_workspace() AND m.owner_id=app_user() AND m.chat_id= @chat_id AND m.id= @id AND m.kind='answer';

-- name: FlagLabLesson :one
UPDATE lesson SET status='set_aside',set_aside_reason='flagged'
WHERE workspace_id=app_workspace() AND id= @id RETURNING id;

-- name: RecordLabFlag :exec
INSERT INTO lesson_flag(workspace_id,lesson_id,user_id,reason)
VALUES(app_workspace(),@lesson_id,app_user(),@reason);

-- name: QueueLabFlagRewrite :exec
INSERT INTO lesson_job(workspace_id,lesson_id,triggers,due_at)
SELECT app_workspace(),@lesson_id::uuid,ARRAY['flag'],now()
WHERE NOT EXISTS(SELECT 1 FROM lesson_job WHERE workspace_id=app_workspace() AND lesson_id= @lesson_id AND status='queued' AND 'flag'=ANY(triggers));
