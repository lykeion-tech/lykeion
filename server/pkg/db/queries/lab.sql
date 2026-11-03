-- All relations are explicitly workspace-scoped in addition to RLS. These are
-- lab facts only: no Coresearch relation or run transcript is read.
-- name: LabFact :one
SELECT c.title::text AS title, jsonb_build_object('study',to_jsonb(c),
 'attachments',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',a.id,'name',a.name,'content_type',a.content_type,'size_bytes',a.size_bytes,'record_key',a.record_key)) FROM attachment a WHERE a.workspace_id=c.workspace_id AND a.card_id=c.id),'[]'::jsonb),
 'runs',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',r.id,'kind',r.kind,'role',r.role,'board_column',r.board_column,'round',r.round,'status',r.status,'created_at',r.created_at)) FROM agent_run r WHERE r.workspace_id=c.workspace_id AND r.card_id=c.id AND r.kind IN ('role','rerun')),'[]'::jsonb),
 'teams',COALESCE((SELECT jsonb_agg(jsonb_build_object('column',ct.board_column,'name',p.name,'manifest',p.manifest)) FROM card_team ct JOIN field_pack p ON p.workspace_id=ct.workspace_id AND p.id=ct.pack_id WHERE ct.workspace_id=c.workspace_id AND ct.card_id=c.id),'[]'::jsonb))::text AS text,
 c.version::text AS version, false AS superseded,
 COALESCE((SELECT jsonb_agg(x.ref ORDER BY x.ref) FROM (
 SELECT 'record:'||r.id::text AS ref FROM record r WHERE r.workspace_id=c.workspace_id AND r.card_id=c.id
 UNION ALL SELECT 'comment:'||m.id::text FROM comment m WHERE m.workspace_id=c.workspace_id AND m.card_id=c.id
 UNION ALL SELECT 'decision:'||i.id::text FROM inbox_item i WHERE i.workspace_id=c.workspace_id AND i.card_id=c.id AND (jsonb_array_length(i.options)>0 OR i.question_set IS NOT NULL)
 ) x),'[]'::jsonb)::jsonb AS links, '[]'::jsonb AS cites
FROM card c WHERE @kind::text='study' AND c.workspace_id=sqlc.arg(workspace_id)::uuid AND c.id=sqlc.arg(id)::uuid
UNION ALL
SELECT r.kind, to_jsonb(r)::text, CASE WHEN r.superseded_at IS NULL THEN 'live' ELSE 'superseded' END, r.superseded_at IS NOT NULL,
 jsonb_build_array('study:'||r.card_id::text), '[]'::jsonb
FROM record r JOIN card c ON c.workspace_id=r.workspace_id AND c.id=r.card_id
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
FROM lesson l WHERE @kind='lesson' AND l.workspace_id=sqlc.arg(workspace_id)::uuid AND l.id=sqlc.arg(id)::uuid AND l.status='active'
UNION ALL
SELECT w.name, jsonb_build_object('research_context',w.research_context,
 'workflows',COALESCE((SELECT jsonb_agg(DISTINCT jsonb_build_object('workflow',c.workflow,'release',c.workflow_release)) FROM card c WHERE c.workspace_id=w.id AND c.workflow IS NOT NULL),'[]'::jsonb),
 'teams',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',p.id,'name',p.name,'manifest',p.manifest)) FROM field_pack p WHERE p.workspace_id=w.id AND EXISTS(SELECT 1 FROM card_team ct WHERE ct.workspace_id=p.workspace_id AND ct.pack_id=p.id)),'[]'::jsonb))::text,
 md5(w.research_context), false, '[]'::jsonb, '[]'::jsonb
FROM workspace w WHERE @kind='workspace' AND w.id=sqlc.arg(workspace_id)::uuid AND (sqlc.arg(id)::uuid='00000000-0000-0000-0000-000000000000'::uuid OR w.id=sqlc.arg(id)::uuid);
