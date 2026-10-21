-- One draw per study column, made when a Team-worked column first finishes.
-- An offered draw is shown to the study's owner, who may rate it 1 to 4.
-- Ratings outlive their study: deleting the study only clears card_id.
CREATE TABLE team_rating (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace ON DELETE CASCADE,
  card_id uuid,
  board_column text NOT NULL CHECK (board_column IN ('grounding', 'hypotheses', 'experimentation', 'validation', 'reporting')),
  pack_id uuid NOT NULL,
  user_id uuid NOT NULL REFERENCES "user",
  offered boolean NOT NULL,
  drawn_at timestamptz NOT NULL,
  rating smallint CHECK (rating BETWEEN 1 AND 4),
  rated_at timestamptz,
  dismissed_at timestamptz,
  UNIQUE (card_id, board_column),
  FOREIGN KEY (workspace_id, card_id) REFERENCES card (workspace_id, id) ON DELETE SET NULL (card_id),
  FOREIGN KEY (workspace_id, pack_id) REFERENCES field_pack (workspace_id, id),
  CHECK (offered OR (rating IS NULL AND dismissed_at IS NULL))
);

CREATE INDEX team_rating_offers ON team_rating (workspace_id, user_id, drawn_at) WHERE offered;

ALTER TABLE team_rating ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON team_rating USING (workspace_id = app_workspace()) WITH CHECK (workspace_id = app_workspace());
GRANT SELECT, INSERT, UPDATE, DELETE ON team_rating TO lykeion_app;
