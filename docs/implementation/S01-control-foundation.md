# S01 control foundation

This checkpoint implements explicit local owner provisioning, single-use login-code exchange, cookie sessions, CSRF and origin checks, workspace-scoped reads, a transactional preference command and durable workspace event replay. WebSocket delivery reads the same PostgreSQL event log across control instances. Encrypted cursors bind replay to the workspace, user and authorization epoch.

This is not the completed S01 acceptance slice. Invitations, membership commands and frontend authentication are now implemented in the membership checkpoint. Resource grants and persistent desktop credential storage remain pending. Presence and document frames are specified but are not enabled by this service. The capability response advertises only implemented commands. The installed desktop application remains the interaction reference; its data and source are not used by the new service.

## Local development

Requirements: Docker, the Go toolchain pinned in `backend/go.mod`, and Python 3. From the repository root, create a private, ignored environment file:

```sh
python3 - <<'PY'
from pathlib import Path
import os, secrets
p = Path('.local-state')
p.mkdir(exist_ok=True)
fd = os.open(p / 'development.env', os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, 'w') as f:
    f.write('LYKEION_DEV_DB_PASSWORD=' + secrets.token_hex(32) + '\n')
PY
docker compose --env-file .local-state/development.env -f deploy/compose.dev.yaml up -d --wait
python3 scripts/test-backend.py
```

The test runner connects only to the dedicated development database on loopback port 15439. Integration tests create randomly named schemas and remove their own schemas afterward. Other applications' databases are not involved. Without `LYKEION_TEST_DATABASE_URL`, direct Go test invocations skip database integration tests; use the wrapper above for this checkpoint's acceptance checks. Run suites sequentially.

Running the control executable additionally requires `LYKEION_DATABASE_URL`, `LYKEION_SIGNING_KEY_FILE` (at least 32 random bytes) and an explicit comma-separated `LYKEION_ALLOWED_ORIGINS`. Local HTTP requires `LYKEION_ALLOW_LOOPBACK_HTTP=1`; it must bind loopback. Production requires a TLS reverse proxy and secure cookies. No default account is created. `login-code --user ID --code-file PATH` issues a new single-use code for an existing installation account using administrative database access. The CLI's `init --owner NAME --workspace NAME --code-file PATH` explicitly provisions an owner and writes a ten-minute single-use code to a new private file.

## Verification and limits

Race-enabled Go tests cover policy replay, the bounded collaboration spike, PostgreSQL command atomicity and idempotency, access isolation, sessions, replay, two-instance delivery and membership removal. WebSocket input rejects unknown fields, trailing JSON, binary frames and malformed identifiers.

The event dispatcher currently polls PostgreSQL; a database notification adapter is still pending. The service does not yet provide production abuse limits or resource authorization lifecycle APIs. Programme creation is unavailable. The document algorithm remains an in-memory spike, not a production collaborative editor. These boundaries must remain visible in capability declarations and later implementation receipts.
