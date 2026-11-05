ALTER TABLE card ADD COLUMN priority text NOT NULL DEFAULT 'none' CHECK (priority IN ('none', 'urgent', 'high', 'medium', 'low'));
ALTER TABLE research ADD COLUMN start_date date, ADD COLUMN due_date date;
ALTER TABLE study_draft_request ADD COLUMN priority text NOT NULL DEFAULT 'none' CHECK (priority IN ('none', 'urgent', 'high', 'medium', 'low'));
