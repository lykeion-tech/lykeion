# S01 control foundation

This checkpoint implements explicit local owner provisioning, single-use login-code exchange, cookie sessions, CSRF and origin checks, workspace-scoped reads, a transactional preference command and durable workspace event replay. WebSocket delivery reads the same PostgreSQL event log across control instances. Encrypted cursors bind replay to the workspace, user and authorization epoch.

This is not the completed S01 acceptance slice. Invitations, membership commands, frontend authentication, encrypted desktop session recovery, programme creation and programme grants are implemented. Workspace viewing presence is enabled; document collaboration is not. The capability response advertises only implemented commands. The installed desktop application remains the interaction reference; its data and source are not used by the new service.

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

The event dispatcher uses transactional PostgreSQL notification hints and periodic durable-log catch-up. Notifications carry no resource payload and never authorize delivery. Shared request limits and expiring connection leases bound abuse; production load testing remains outstanding. Programme creation consumes sealed configuration references and a dimensioned ceiling. Programme grants filter reads before pagination; leaving or removal revokes grants and rejoining does not restore them. The document algorithm remains an in-memory spike, not a production collaborative editor. These boundaries must remain visible in capability declarations and later implementation receipts.


## Session, realtime and CLI continuation

Desktop session cookies stay in the main process and are encrypted with the operating system credential facility. Unavailable encryption (including Linux basic_text) fails closed. The credential file is private and endpoint-bound. Logout/expired sessions clear it. Only the nonsecret endpoint is stored in the renderer. The approved synthetic macOS session was recovered after quitting and restarting the preview; the installed reference credentials were not accessed.

WebSockets support workspace viewing presence only, expiring after 30 seconds. They enforce 8 connections/account, 256/workspace, 60 incoming frames/10 seconds, 64 KiB frames, a bounded incoming queue and a two-second write timeout. More than 500 pending events triggers resynchronization. Revocation and authorization epochs are checked during delivery; presence queries exclude expired or revoked sessions. Reconnect uses bounded backoff and HTTP replay. POST authentication exchange is capped at 12/peer/minute and authenticated requests at 300/account/minute across instances. Reverse proxies must apply their own public-edge limits; untrusted forwarded addresses are never used for identity.

CLI sessions are dedicated eight-hour bearer credentials; browser cookies cannot be used as CLI bearer tokens and CLI credentials cannot be used as browser cookies. The CLI rejects non-HTTPS origins other than loopback, redirects, insecure credential-file permissions and endpoint changes. Examples (private files are not committed):

```sh
lykeion login --endpoint http://127.0.0.1:18431 --code-file /private/path/code --credentials /private/path/new-session.json
lykeion get --credentials /private/path/new-session.json --path /v1/workspaces
lykeion command --credentials /private/path/new-session.json --workspace WORKSPACE_ID --name preference.update --body-file /private/path/pinned-command.json --key STABLE_RETRY_KEY
lykeion logout --credentials /private/path/new-session.json
```

`command` takes the complete generated command envelope, including expected versions. `request` provides POST/PUT for upload and invitation routes using the same authenticated boundary. Neither command invents revisions or retries a changed request.

## Sealed configurations: bounded S02 prerequisite

Set `LYKEION_ARTIFACT_ROOT` to an absolute, dedicated local store directory. Without it, configuration/programme commands are not advertised. The initial upload interface accepts one JSON file up to 64 KiB, verifies its declared SHA-256 and byte length, then seals a manifest through `artifact.submit`. The content-addressed store detects tampering on reads. This is not the general scientific artifact or large-dataset service.

Supported authored kinds are `domain-profile`, `gate-policy` and `grant-changes`; their wire shapes are in the generated contract. Semantic validation additionally rejects invalid policies, duplicate recipients/capabilities and access plans inconsistent with current versions. `programme.create` pins profile/policies and records the initial budget ceiling atomically. It does not allocate funds, admit inquiries or approve scientific evidence. Owner-authorized `access.change` requires a sealed plan and current programme revision. A workspace viewer cannot gain edit permission. Grant changes invalidate connected authorization epochs.

Socket expiry, unauthorized presence, replay overflow and invalid-cursor recovery have executable coverage. Remaining acceptance: write-timeout/load stress and snapshot/replay race scenarios, UI resource sharing/demotion/owner-transfer coverage and end-to-end CLI workflow. The scientific policy/editor UX, role bindings and reviewer independence remain S02 work. The current explicit configuration-file creation form is an interim setup surface, not a claim of final pixel-identical Researches interactions. Preserve the reference shell and replace this setup mechanism with validated programme configuration controls during S02.
