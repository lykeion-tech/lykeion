ALTER TABLE card ADD CONSTRAINT card_distinct_auditor_cli CHECK (study_cli IS NULL OR auditor_cli IS NULL OR study_cli <> auditor_cli);
