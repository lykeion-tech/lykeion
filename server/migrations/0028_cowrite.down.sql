CREATE OR REPLACE FUNCTION public.coresearch_expired_workspaces() RETURNS SETOF uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
BEGIN
 IF public.app_user() IS DISTINCT FROM '00000000-0000-0000-0000-000000000000'::uuid THEN RAISE EXCEPTION 'maintenance identity required' USING ERRCODE='42501'; END IF;
 RETURN QUERY SELECT workspace_id FROM public.coresearch_cell WHERE status='running' AND (lease_expires_at IS NULL OR lease_expires_at<=now())
 UNION SELECT workspace_id FROM public.coresearch_run WHERE status='running' AND (lease_expires_at IS NULL OR lease_expires_at<=now())
 UNION SELECT workspace_id FROM public.coresearch_setup WHERE status='running' AND (lease_expires_at IS NULL OR lease_expires_at<=now()) ORDER BY 1;
END $$;

CREATE OR REPLACE FUNCTION public.coresearch_expire() RETURNS TABLE(kind text,project_id uuid,chat_id uuid,work_id uuid)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
DECLARE c public.coresearch_chat; item uuid;
BEGIN
 IF public.app_user()<>'00000000-0000-0000-0000-000000000000'::uuid AND NOT EXISTS(SELECT 1 FROM public.member m WHERE m.workspace_id=public.app_workspace() AND m.user_id=public.app_user()) THEN RAISE EXCEPTION 'workspace authority required' USING ERRCODE='42501'; END IF;
 FOR c IN SELECT x.* FROM public.coresearch_chat x WHERE x.workspace_id=public.app_workspace() AND (public.app_user()='00000000-0000-0000-0000-000000000000'::uuid OR public.coresearch_can_see(x.project_id)) ORDER BY x.project_id,x.id FOR UPDATE LOOP
 FOR item IN UPDATE public.coresearch_cell q SET status='interrupted',failure_reason='runtime_lost',finished_at=now(),outputs='[{"type":"error","ename":"RuntimeLost","evalue":"execution lease expired; code was not replayed"}]'::jsonb WHERE q.workspace_id=c.workspace_id AND q.chat_id=c.id AND q.status='running' AND (q.lease_expires_at IS NULL OR q.lease_expires_at<=now()) RETURNING q.id LOOP
 RETURN QUERY SELECT 'cell'::text,c.project_id,c.id,item; END LOOP;
 FOR item IN UPDATE public.coresearch_run q SET status='failed',failure_reason='runtime_lost',finished_at=now(),facts=jsonb_build_object('error','execution lease expired; code was not replayed') WHERE q.workspace_id=c.workspace_id AND q.chat_id=c.id AND q.status='running' AND (q.lease_expires_at IS NULL OR q.lease_expires_at<=now()) RETURNING q.id LOOP
 RETURN QUERY SELECT 'run'::text,c.project_id,c.id,item; END LOOP;
 FOR item IN UPDATE public.coresearch_setup q SET status='failed',failure_reason='runtime_lost',finished_at=now(),log='execution lease expired; setup was not replayed' WHERE q.workspace_id=c.workspace_id AND q.chat_id=c.id AND q.status='running' AND (q.lease_expires_at IS NULL OR q.lease_expires_at<=now()) RETURNING q.id LOOP
 RETURN QUERY SELECT 'setup'::text,c.project_id,c.id,item; END LOOP;
 END LOOP;
END $$;

ALTER TABLE agent_run DROP COLUMN coresearch_build_id;
CREATE OR REPLACE FUNCTION public.coresearch_blob_referenced(sha text) RETURNS boolean
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM public.member m WHERE m.workspace_id = public.app_workspace() AND m.user_id = public.app_user()) AND NOT EXISTS (SELECT 1 FROM public.agent_run r JOIN public.coresearch_chat c ON c.workspace_id=r.workspace_id AND c.id=r.coresearch_chat_id JOIN public.coresearch_member m ON m.workspace_id=c.workspace_id AND m.project_id=c.project_id AND m.user_id=c.owner_id JOIN public.member w ON w.workspace_id=m.workspace_id AND w.user_id=m.user_id WHERE r.workspace_id=public.app_workspace() AND r.id=public.app_user() AND r.kind='coresearch' AND r.status='running') THEN
    RAISE EXCEPTION 'workspace membership required' USING ERRCODE = '42501';
  END IF;
  RETURN EXISTS (SELECT 1 FROM public.coresearch_file_version v WHERE v.workspace_id = public.app_workspace() AND v.sha256 = sha);
END
$$;

-- Restore the pre-Cowrite maintenance functions before removing their table.
CREATE OR REPLACE FUNCTION public.coresearch_revoke(target uuid, project uuid)
RETURNS TABLE(kind text, project_id uuid, chat_id uuid, work_id uuid)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
DECLARE c public.coresearch_chat; item uuid;
BEGIN
  IF project IS NULL THEN
    IF NOT EXISTS(SELECT 1 FROM public.member m WHERE m.workspace_id=public.app_workspace() AND m.user_id=public.app_user() AND (m.user_id=target OR m.role IN ('owner','admin'))) THEN
      RAISE EXCEPTION 'workspace membership removal authority required' USING ERRCODE='42501';
    END IF;
  ELSIF NOT EXISTS(SELECT 1 FROM public.coresearch_member m JOIN public.member w ON w.workspace_id=m.workspace_id AND w.user_id=m.user_id WHERE m.workspace_id=public.app_workspace() AND m.project_id=project AND m.user_id=public.app_user() AND m.role='owner') THEN
    RAISE EXCEPTION 'project ownership required' USING ERRCODE='42501';
  END IF;
  FOR c IN SELECT x.* FROM public.coresearch_chat x WHERE x.workspace_id=public.app_workspace() AND x.owner_id=target AND (project IS NULL OR x.project_id=project) ORDER BY x.project_id,x.id FOR UPDATE LOOP
    FOR item IN UPDATE public.coresearch_cell q SET status='cancelled',failure_reason='coresearch_access_revoked',finished_at=now() WHERE q.workspace_id=c.workspace_id AND q.chat_id=c.id AND q.status IN ('queued','running') RETURNING q.id LOOP
      RETURN QUERY SELECT 'cell'::text,c.project_id,c.id,item;
    END LOOP;
    FOR item IN UPDATE public.coresearch_run q SET status='cancelled',failure_reason='coresearch_access_revoked',finished_at=now() WHERE q.workspace_id=c.workspace_id AND q.chat_id=c.id AND q.status IN ('queued','running') RETURNING q.id LOOP
      RETURN QUERY SELECT 'run'::text,c.project_id,c.id,item;
    END LOOP;
    FOR item IN UPDATE public.coresearch_setup q SET status='cancelled',failure_reason='coresearch_access_revoked',finished_at=now() WHERE q.workspace_id=c.workspace_id AND q.chat_id=c.id AND q.status IN ('queued','running') RETURNING q.id LOOP
      RETURN QUERY SELECT 'setup'::text,c.project_id,c.id,item;
    END LOOP;
    UPDATE public.coresearch_chat x SET kernel_request=NULL,kernel_state='stopped' WHERE x.workspace_id=c.workspace_id AND x.id=c.id;
    RETURN QUERY SELECT 'chat'::text,c.project_id,c.id,c.id;
    FOR item IN SELECT a.id FROM public.agent_run a WHERE a.workspace_id=c.workspace_id AND a.coresearch_chat_id=c.id AND a.kind='coresearch' AND a.status IN ('queued','dispatched','running') FOR UPDATE LOOP
      RETURN QUERY SELECT 'turn'::text,c.project_id,c.id,item;
    END LOOP;
  END LOOP;
END
$$;
REVOKE ALL ON FUNCTION public.coresearch_revoke(uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.coresearch_revoke(uuid,uuid) TO lykeion_app;

CREATE OR REPLACE FUNCTION public.coresearch_depart(target uuid) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path=public AS $$
DECLARE p public.coresearch_project; successor uuid;
BEGIN
 IF NOT EXISTS(SELECT 1 FROM public.member m WHERE m.workspace_id=public.app_workspace() AND m.user_id=public.app_user() AND (m.user_id=target OR m.role IN ('owner','admin'))) THEN
  RAISE EXCEPTION 'workspace membership removal authority required' USING ERRCODE='42501';
 END IF;
 FOR p IN SELECT x.* FROM public.coresearch_project x JOIN public.coresearch_member m ON m.workspace_id=x.workspace_id AND m.project_id=x.id
 WHERE x.workspace_id=public.app_workspace() AND m.user_id=target ORDER BY x.id FOR UPDATE OF x LOOP
  successor := NULL;
  SELECT m.user_id INTO successor FROM public.coresearch_member m WHERE m.workspace_id=p.workspace_id AND m.project_id=p.id AND m.user_id<>target ORDER BY m.created_at,m.user_id LIMIT 1;
  IF successor IS NULL THEN
   UPDATE public.coresearch_project SET archived_at=coalesce(archived_at,now()),updated_at=now() WHERE workspace_id=p.workspace_id AND id=p.id;
  ELSIF NOT EXISTS(SELECT 1 FROM public.coresearch_member m WHERE m.workspace_id=p.workspace_id AND m.project_id=p.id AND m.user_id<>target AND m.role='owner') THEN
   IF public.app_user()=target THEN RETURN false; END IF;
   UPDATE public.coresearch_member SET role='owner' WHERE workspace_id=p.workspace_id AND project_id=p.id AND user_id=successor;
  END IF;
 END LOOP;
 RETURN true;
END
$$;
REVOKE ALL ON FUNCTION public.coresearch_depart(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.coresearch_depart(uuid) TO lykeion_app;


DROP TABLE coresearch_version,coresearch_build,coresearch_comment,coresearch_change,coresearch_proposal,coresearch_doc_step,coresearch_doc;
DROP FUNCTION refuse_coresearch_doc_step_update();
ALTER TABLE runtime DROP COLUMN models,DROP COLUMN metrics;
ALTER TABLE coresearch_chat
  DROP CONSTRAINT coresearch_chat_project_mode_fkey,
  DROP CONSTRAINT coresearch_chat_mode_check,
  ADD CONSTRAINT coresearch_chat_mode_check CHECK (mode IN ('experiment')),
  DROP CONSTRAINT coresearch_chat_kernel_request_check,
  ADD CONSTRAINT coresearch_chat_kernel_request_check CHECK (kernel_request IN ('interrupt','restart')),
  DROP COLUMN kernel_metrics;
ALTER TABLE coresearch_project DROP CONSTRAINT coresearch_project_workspace_id_id_mode_key,DROP COLUMN mode;

ALTER TABLE coresearch_conversation ADD CONSTRAINT coresearch_conversation_project_id_key UNIQUE (project_id);

UPDATE coresearch_turn_receipt SET body=body::jsonb->>'Body';

ALTER TABLE coresearch_file DROP COLUMN deleted_at;
