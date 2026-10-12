ALTER TABLE sessions ADD COLUMN client_kind text NOT NULL DEFAULT 'browser' CHECK(client_kind IN ('browser','cli'));
