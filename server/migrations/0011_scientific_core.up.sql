-- Field packs: a versioned bundle of checks and skills a researcher's Team uses.
-- A shipped pack has no uploader; an uploaded one belongs to the person who sent it.
CREATE TABLE field_pack (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  name text NOT NULL,
  version text NOT NULL,
  sha256 text NOT NULL,
  manifest jsonb NOT NULL,
  blob_key text NOT NULL,
  source text NOT NULL CHECK (source IN ('shipped', 'uploaded')),
  uploaded_by uuid REFERENCES "user",
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((source = 'shipped') = (uploaded_by IS NULL)),
  UNIQUE (workspace_id, name, version),
  UNIQUE (workspace_id, id)
);

CREATE TABLE field_pack_approval (
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  pack_id uuid NOT NULL,
  approved_by uuid NOT NULL REFERENCES "user",
  approved_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (pack_id, approved_by),
  FOREIGN KEY (workspace_id, pack_id) REFERENCES field_pack (workspace_id, id) ON DELETE CASCADE
);

ALTER TABLE field_pack ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON field_pack USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
ALTER TABLE field_pack_approval ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON field_pack_approval USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, UPDATE, DELETE ON field_pack, field_pack_approval TO lykeion_app;

-- The researcher's Team: the name of the field pack their studies use.
ALTER TABLE "user" ADD COLUMN pack_name text NOT NULL DEFAULT '';

-- cap is how many route-backs, redos and revisions a card gets before Blocked;
-- route_backs counts uses of it. block_kind says why a card sits in Blocked.
ALTER TABLE card
  ADD COLUMN pack_id uuid,
  ADD COLUMN start_kind text NOT NULL DEFAULT '' CHECK (start_kind IN ('', 'question', 'dataset', 'claim')),
  ADD COLUMN cap int NOT NULL DEFAULT 3 CHECK (cap >= 1),
  ADD COLUMN block_kind text NOT NULL DEFAULT ''
    CHECK (block_kind IN ('', 'cap', 'infra', 'no_feasible_test', 'human_run', 'too_small_to_split', 'drift')),
  ADD COLUMN reopens int NOT NULL DEFAULT 0,
  ADD CONSTRAINT card_pack_fkey FOREIGN KEY (workspace_id, pack_id) REFERENCES field_pack (workspace_id, id);

ALTER TABLE inbox_item DROP CONSTRAINT inbox_item_kind_check;
ALTER TABLE inbox_item ADD CONSTRAINT inbox_item_kind_check CHECK (kind IN
  ('escalated', 'out_of_budget', 'released', 'publish_request', 'autoresearch_card', 'autoresearch_report',
   'revised', 'reopened'));

ALTER TABLE autoresearch DROP CONSTRAINT autoresearch_execution_mode_check;
ALTER TABLE autoresearch ADD CONSTRAINT autoresearch_execution_mode_check CHECK (execution_mode IN
  ('create_card', 'run_only', 'watch_released'));
