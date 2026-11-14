-- Backlog order: WIP pull starts the lowest position first, ties broken by
-- number. Existing Backlog cards keep their number order; a card outside
-- Backlog keeps whatever position it had there.
ALTER TABLE card ADD COLUMN position double precision NOT NULL DEFAULT 0;
UPDATE card SET position = o.n
FROM (SELECT id, row_number() OVER (PARTITION BY workspace_id ORDER BY number) AS n
      FROM card WHERE board_column = 'backlog') o
WHERE card.id = o.id;

-- What the lab is and studies. research_context reaches every stage role's
-- prompt as background, never as evidence. An empty card_prefix means the
-- prefix is derived from the slug. logo_blob is the storage key of the logo.
ALTER TABLE workspace
  ADD COLUMN description text NOT NULL DEFAULT '',
  ADD COLUMN research_context text NOT NULL DEFAULT '',
  ADD COLUMN card_prefix text NOT NULL DEFAULT '' CHECK (card_prefix ~ '^([A-Z]{2,6})?$'),
  ADD COLUMN logo_blob text NOT NULL DEFAULT '';

-- onboarded_at stays NULL until the person finishes or skips onboarding.
ALTER TABLE "user"
  ADD COLUMN career_stage text NOT NULL DEFAULT '' CHECK (career_stage IN
    ('', 'undergraduate', 'masters', 'phd', 'postdoc', 'research_staff', 'faculty', 'industry', 'other')),
  ADD COLUMN onboarded_at timestamptz;

-- An inbox item is open until it is answered; resolved items are the archive.
ALTER TABLE inbox_item ADD COLUMN resolved_at timestamptz;

-- The agent CLI's own version, as its daemon last reported it at registration;
-- empty when the daemon could not read it.
ALTER TABLE runtime ADD COLUMN cli_version text NOT NULL DEFAULT '';
