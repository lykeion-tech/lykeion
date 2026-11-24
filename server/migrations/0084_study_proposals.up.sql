CREATE TABLE study_proposal (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  id uuid NOT NULL,
  owner_id uuid NOT NULL REFERENCES "user" ON DELETE CASCADE,
  -- Launched receipts keep their original project.
  project_id uuid,
  description text CHECK (length(btrim(description)) > 0),
  stage text CHECK (stage IN ('grounding','hypotheses','experimentation','validation')),
  assignee jsonb CHECK (jsonb_typeof(assignee) = 'object'),
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  discarded_at timestamptz,
  last_save_revision bigint,
  operation_id uuid,
  launch_revision bigint,
  -- Keep the receipt after card deletion. A lost response must never restart it.
  card_id uuid,
  card_number integer,
  PRIMARY KEY (workspace_id,id),
  UNIQUE (workspace_id,owner_id,operation_id),
  CHECK ((discarded_at IS NULL AND num_nonnulls(description,stage,assignee)=3) OR
         (discarded_at IS NOT NULL AND num_nonnulls(description,stage,assignee,project_id,card_id)=0)),
  CHECK (num_nonnulls(operation_id,launch_revision,card_id,card_number) IN (0,4))
);
CREATE INDEX study_proposal_board ON study_proposal(workspace_id,created_at DESC,id DESC) WHERE card_id IS NULL AND discarded_at IS NULL;
CREATE INDEX study_proposal_project ON study_proposal(workspace_id,project_id,created_at DESC,id DESC) WHERE card_id IS NULL AND discarded_at IS NULL;
ALTER TABLE study_proposal ENABLE ROW LEVEL SECURITY;
CREATE POLICY proposal_workspace ON study_proposal AS RESTRICTIVE
  USING (workspace_id=app_workspace() AND EXISTS (
    SELECT 1 FROM member WHERE workspace_id=app_workspace() AND user_id=app_user()
  ));
CREATE POLICY proposal_read ON study_proposal FOR SELECT USING (true);
CREATE POLICY proposal_add ON study_proposal FOR INSERT WITH CHECK (owner_id=app_user());
CREATE POLICY proposal_change ON study_proposal FOR UPDATE USING (owner_id=app_user());
GRANT SELECT,INSERT,UPDATE ON study_proposal TO lykeion_app;

-- Membership and project deletion can be performed by someone other than the
-- proposal owner. Restrict these definer writes to the deleted parent row.
CREATE FUNCTION retire_departed_proposals() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  UPDATE public.study_proposal SET description=NULL, stage=NULL, assignee=NULL, project_id=NULL, discarded_at=now(), updated_at=now(), revision=revision+1, last_save_revision=NULL
  WHERE workspace_id=OLD.workspace_id AND owner_id=OLD.user_id AND card_id IS NULL AND discarded_at IS NULL;
  RETURN OLD;
END $$;
CREATE TRIGGER retire_departed_proposals AFTER DELETE ON member
  FOR EACH ROW EXECUTE FUNCTION retire_departed_proposals();

CREATE FUNCTION detach_project_proposals() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  UPDATE public.study_proposal SET project_id=NULL, updated_at=now(), revision=revision+1, last_save_revision=NULL
  WHERE workspace_id=OLD.workspace_id AND project_id=OLD.id AND card_id IS NULL AND discarded_at IS NULL;
  RETURN OLD;
END $$;
CREATE TRIGGER detach_project_proposals AFTER DELETE ON research
  FOR EACH ROW EXECUTE FUNCTION detach_project_proposals();
REVOKE ALL ON FUNCTION retire_departed_proposals(), detach_project_proposals() FROM PUBLIC;
