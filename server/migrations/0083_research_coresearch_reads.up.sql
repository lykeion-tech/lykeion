-- Project sharing is a read surface of its own. The backing Coresearch
-- project's policies and membership continue to govern its normal routes.
CREATE FUNCTION public.research_coresearch_link(target uuid)
RETURNS TABLE (id uuid, chat_id uuid, project_id uuid, owner_id uuid, title text, mode text,
               created_at timestamptz, updated_at timestamptz, pinned_at timestamptz, archived_at timestamptz)
LANGUAGE sql STABLE SET search_path = pg_catalog, public AS $$
  SELECT c.id, c.chat_id, c.project_id, c.owner_id, c.title, p.mode,
         c.created_at, c.updated_at, c.pinned_at, c.archived_at
  FROM public.coresearch_conversation c
  JOIN public.research r ON r.workspace_id = c.workspace_id AND r.id = c.research_id
  JOIN public.coresearch_project p ON p.workspace_id = c.workspace_id AND p.id = c.project_id
  WHERE r.workspace_id = public.app_workspace() AND r.id = target
    AND EXISTS (SELECT 1 FROM public.member m
      WHERE m.workspace_id = r.workspace_id AND m.user_id = public.app_user())
$$;

CREATE FUNCTION public.research_coresearch_artifacts(backing_project uuid, backing_chat uuid)
RETURNS TABLE (id uuid, path text, kind text, blob_key text, size bigint)
LANGUAGE sql STABLE SET search_path = pg_catalog, public AS $$
  SELECT f.id, f.path, 'file', v.blob_key, v.size
  FROM public.coresearch_file f
  JOIN LATERAL (
    SELECT head.blob_key, head.size FROM public.coresearch_file_version head
    WHERE head.workspace_id = f.workspace_id AND head.file_id = f.id
    ORDER BY head.n DESC LIMIT 1
  ) v ON true
  WHERE f.workspace_id = public.app_workspace() AND f.project_id = backing_project
    AND f.deleted_at IS NULL
  UNION ALL
  SELECT c.id, 'Cell ' || c.seq || ' outputs.json', 'notebook_output', c.outputs_blob, -1::bigint
  FROM public.coresearch_cell c
  WHERE c.workspace_id = public.app_workspace() AND c.chat_id = backing_chat
    AND c.outputs_blob IS NOT NULL
  UNION ALL
  SELECT b.id, 'Manuscript build ' || b.seq || '.pdf', 'manuscript', b.pdf_blob, b.pdf_size
  FROM public.coresearch_build b
  WHERE b.workspace_id = public.app_workspace() AND b.project_id = backing_project
    AND b.status = 'ok' AND b.pdf_blob IS NOT NULL
$$;

CREATE FUNCTION public.list_research_coresearch(target uuid) RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  SELECT coalesce(jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
    'id', c.id, 'title', c.title, 'mode', c.mode, 'created_at', c.created_at,
    'updated_at', c.updated_at, 'pinned_at', c.pinned_at, 'archived_at', c.archived_at,
    'is_owner', c.owner_id = public.app_user(),
    'project_id', CASE WHEN c.owner_id = public.app_user() THEN c.project_id END,
    'chat_id', CASE WHEN c.owner_id = public.app_user() THEN c.chat_id END
  )) ORDER BY (c.pinned_at IS NOT NULL) DESC, c.updated_at DESC, c.id), '[]'::jsonb)
  FROM public.research_coresearch_link(target) c
$$;

CREATE FUNCTION public.get_research_coresearch(target uuid, conversation uuid) RETURNS SETOF jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  SELECT jsonb_build_object(
    'title', c.title, 'mode', c.mode,
    'messages', coalesce((SELECT jsonb_agg(jsonb_build_object(
      'id', m.id, 'kind', m.kind, 'body', m.body, 'created_at', m.created_at
    ) ORDER BY m.created_at, m.id) FROM public.coresearch_message m
      WHERE m.workspace_id = public.app_workspace() AND m.chat_id = c.chat_id), '[]'::jsonb),
    'cells', coalesce((SELECT jsonb_agg(jsonb_build_object(
      'id', x.id, 'code', x.source, 'language', x.kernel, 'position', x.seq, 'outputs', x.outputs
    ) ORDER BY x.seq) FROM public.coresearch_cell x
      WHERE x.workspace_id = public.app_workspace() AND x.chat_id = c.chat_id), '[]'::jsonb),
    'artifacts', coalesce((SELECT jsonb_agg(jsonb_build_object(
      'id', f.id, 'path', f.path, 'kind', f.kind
    ) ORDER BY f.path, f.id) FROM public.research_coresearch_artifacts(c.project_id, c.chat_id) f), '[]'::jsonb)
  )
  FROM public.research_coresearch_link(target) c WHERE c.id = conversation
$$;

CREATE FUNCTION public.get_research_coresearch_artifact(target uuid, conversation uuid, artifact uuid)
RETURNS TABLE (path text, blob_key text, size bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, public AS $$
  SELECT f.path, f.blob_key, f.size
  FROM public.research_coresearch_link(target) c
  CROSS JOIN LATERAL public.research_coresearch_artifacts(c.project_id, c.chat_id) f
  WHERE c.id = conversation AND f.id = artifact
$$;

REVOKE ALL ON FUNCTION public.research_coresearch_link(uuid),
  public.research_coresearch_artifacts(uuid, uuid),
  public.list_research_coresearch(uuid), public.get_research_coresearch(uuid, uuid),
  public.get_research_coresearch_artifact(uuid, uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.list_research_coresearch(uuid),
  public.get_research_coresearch(uuid, uuid),
  public.get_research_coresearch_artifact(uuid, uuid, uuid) TO lykeion_app;
