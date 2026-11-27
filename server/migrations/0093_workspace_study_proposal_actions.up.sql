-- Keep proposal ownership checks for editing and starting. Expose only the
-- two collaborative board actions to workspace members.
CREATE FUNCTION public.change_study_proposal_project_or_discard(
  proposal uuid, expected_revision bigint, project uuid, discard boolean
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM public.member
    WHERE workspace_id=public.app_workspace() AND user_id=public.app_user()
  ) THEN RETURN false; END IF;

  IF NOT discard AND project IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM public.research WHERE workspace_id=public.app_workspace() AND id=project
  ) THEN RETURN false; END IF;

  UPDATE public.study_proposal SET
    project_id=CASE WHEN discard THEN NULL ELSE project END,
    discarded_at=CASE WHEN discard THEN now() ELSE discarded_at END,
    description=CASE WHEN discard THEN NULL ELSE description END,
    stage=CASE WHEN discard THEN NULL ELSE stage END,
    assignee=CASE WHEN discard THEN NULL ELSE assignee END,
    updated_at=now(), last_save_revision=NULL, revision=revision+1
  WHERE workspace_id=public.app_workspace() AND id=proposal
    AND revision=expected_revision AND card_id IS NULL AND discarded_at IS NULL;
  RETURN FOUND;
END $$;
REVOKE ALL ON FUNCTION public.change_study_proposal_project_or_discard(uuid,bigint,uuid,boolean) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.change_study_proposal_project_or_discard(uuid,bigint,uuid,boolean) TO lykeion_app;
