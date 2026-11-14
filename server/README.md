# Lykeion control server

Requires Go 1.26, PostgreSQL 17, and Redis 7. Run commands from `server/`.
SQL queries are generated with the pinned sqlc Go tool: `GOMAXPROCS=2 go tool sqlc generate`.
Commit generated files under `pkg/db/generated` with query/schema changes.

## Database credentials

Use separate credentials. `MIGRATE_DATABASE_URL` connects as the database/schema
owner and applies embedded migrations before serving. `DATABASE_URL` connects
as `lykeion_app`; startup rejects another role, superuser, BYPASSRLS, and public
table ownership privileges (including inherited ownership).

Provision the owner/database through your PostgreSQL administration process.
First run `GOMAXPROCS=2 go run -p 1 ./cmd/migrate` with `MIGRATE_DATABASE_URL` set.
The migration creates `lykeion_app NOLOGIN` without a password. An administrator
must provision its login separately before starting the server, for example in
an interactive psql session using `ALTER ROLE lykeion_app LOGIN` and
`\password lykeion_app`. Do not make it a member of an owning/privileged role.
The server still runs migrations on every start under the separate owner URL.

For deployments that run migrations as a separate step, use the migration
command from the same release before starting any servers. Then set
`MIGRATIONS_MODE=external` for the server and omit `MIGRATE_DATABASE_URL` from
the server process environment. The server checks that every migration in its
release exactly matches the database's recorded versions before becoming ready;
it refuses startup if the check is unavailable, versions differ, or a migration
is in progress at startup. Stop all external-mode servers before running a
migration, then start servers from the matching release; deployment tooling
must enforce this exclusion. The startup check does not prevent a migration
from beginning later while a server is running. An older external-mode server
will not start after a newer release migrates the database. The check does not verify
migration file contents. Keep the owner credential only
in the migration step. The default `MIGRATIONS_MODE=auto` retains automatic
migrations and requires `MIGRATE_DATABASE_URL` on every server start.

Migration `0073_runtime_skill_timeout_sweep` creates an index on
`runtime_skill_request` inside the migration transaction. PostgreSQL blocks
writes to that table while the index is built, so schedule this migration for a
suitable maintenance window, especially when the table is large. The duration
depends on the database and table size.

An administrator can optionally provision a separate serving login on
PostgreSQL 17, without changing the existing login:

```sql
CREATE ROLE lykeion_app_v2 LOGIN;
GRANT lykeion_app TO lykeion_app_v2 WITH INHERIT TRUE, SET FALSE, ADMIN FALSE;
```

Set its password separately, for example using `\password lykeion_app_v2` in
an interactive psql session. Configure the server's `DATABASE_URL` to use
that login, and set `DATABASE_ROLE=lykeion_app_v2` and
`MIGRATIONS_MODE=external`. The v2 login must receive its application access
only through the `lykeion_app` membership, with no additional direct grants,
other role memberships, ownership, or elevated role attributes. Startup checks
the role graph and ownership and grants for common PostgreSQL object classes;
it is not a complete audit of every possible object or privilege. Its authority
still depends on the grants given to `lykeion_app` and `PUBLIC`; administrators
must keep those grants restricted and control role changes after startup. The
check runs at startup only. Provisioning this login does not disable existing
sessions or old replicas and does not establish a fleet cutover.

## Local run

Set these environment variables with your local credentials:

```sh
export MIGRATE_DATABASE_URL='postgres://owner:owner@127.0.0.1:5432/lykeion?sslmode=disable'
export DATABASE_URL='postgres://lykeion_app:app@127.0.0.1:5432/lykeion?sslmode=disable'
export REDIS_URL='redis://127.0.0.1:6379/0'
export JWT_SECRET='0123456789abcdef0123456789abcdef' # development only; use a random secret in production
export LISTEN_ADDR='127.0.0.1:8080'
export PUBLIC_URL='http://127.0.0.1:8080'
export COOKIE_SECURE=false # local HTTP only; defaults to true
GOMAXPROCS=2 go run -p 1 ./cmd/server
```

`JWT_SECRET` must be at least 32 bytes. `LISTEN_ADDR` defaults to `:8080`.
Optional Google sign-in uses `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, and
`PUBLIC_URL` plus `/auth/google/callback` as the registered redirect URI.
Optional email uses `SMTP_ADDR`, `SMTP_FROM`, `SMTP_USER`, and `SMTP_PASS`;
without SMTP the development mailer logs codes. SMTP dialing and the complete
conversation have a five-second limit; request cancellation closes the socket.
STARTTLS is used when advertised. When credentials are configured, AUTH must be
advertised; legacy HELO-only servers cannot silently bypass configured credentials.
With both SMTP_USER and SMTP_PASS empty, unauthenticated delivery is allowed.

`/health` reports process health. `/readyz` requires working PostgreSQL and Redis;
startup verifies both before announcing readiness. After startup, Redis loss
allows realtime delivery through database polling, while sign-in rate limiting
fails closed and `/readyz` returns 503. Redis remains required at startup.
SIGINT/SIGTERM close realtime sockets before database pool cleanup and allow
HTTP requests up to ten seconds to finish. Shutdown cancels request contexts,
including SMTP and database work, before pool cleanup. HTTP headers have a
ten-second timeout; idle keep-alive connections expire after one minute. Ordinary
HTTP requests have a ten-second context deadline. Bodies are fully read before
workspace transactions and locks, have a two-second network read deadline, and
are limited to 1 MiB (413 on oversize, 408 on timeout). WebSocket lifetimes use
the hub's own cancellation and I/O bounds. Google sign-in permits 30 starts and
60 callbacks per IP per hour, then five sign-ins per verified email per hour;
Redis errors return 503 before provider calls at the IP check, or before session
creation at the identity check.
Browser/dashboard transport integration is deferred; the existing frontend is
not connected to this server yet.

## Verification

Integration tests use real services. With Docker available, test utilities start
PostgreSQL and Redis containers. Alternatively set owner/admin and Redis URLs:

```sh
export LYK_TEST_PG='postgres://owner:owner@127.0.0.1:15437/postgres?sslmode=disable'
export LYK_TEST_REDIS='redis://127.0.0.1:16387/0'
GOMAXPROCS=2 go test -p 1 ./cmd/server/ -count=1
GOMAXPROCS=2 go test -p 1 ./... -count=1 -timeout 90s
GOMAXPROCS=2 go build -p 1 ./...
```

Run each command sequentially and wait for its exit before the next. Use
isolated test services: tests create/drop databases, change the test app role,
and flush Redis. The PostgreSQL test administrator needs role management and
CREATE DATABASE privileges (the unsafe-role tests also require superuser).

## People's CLI

Build the CLI from `server/` with `GOMAXPROCS=2 go build -p 1 -o /tmp/lyk ./cmd/lyk`.
Sign in with an emailed code entered in the terminal, or Google in the browser:

```sh
/tmp/lyk login --email you@lab.org --server http://127.0.0.1:8080
# Alternatively: /tmp/lyk login --google --server http://127.0.0.1:8080
/tmp/lyk whoami
/tmp/lyk workspace create 'Smith Lab' smith-lab
/tmp/lyk workspace list
/tmp/lyk workspace switch smith-lab
/tmp/lyk workspace members
/tmp/lyk workspace invite colleague@lab.org --role member
# The invited person signs in, then runs: /tmp/lyk workspace accept TOKEN
/tmp/lyk card new 'Does X raise Y?' --title 'X and Y' --usd 20
/tmp/lyk card list
/tmp/lyk card comment 1 'Focus on the fasting state.'
/tmp/lyk card show 1
/tmp/lyk decisions
/tmp/lyk logout
```

`--server` is required on first login and is remembered. Successful login stores a
90-day personal token, clears the selected workspace, and requires a workspace
switch before card or Inbox commands. `logout` forgets the local token. Configuration
uses `LYK_CONFIG` when set, otherwise `<UserConfigDir>/lyk/config.json`; writes
replace the file atomically with mode 0600 with newly created config directories using 0700. Existing directory permissions
are preserved, including when `LYK_CONFIG` points into a shared directory.
Google sign-in requires configured Google credentials and a local browser; the CLI
waits up to five minutes on an ephemeral loopback listener and accepts only the
correlated callback. Launcher startup errors are returned immediately. A launcher
that starts but later exits unsuccessfully can leave the CLI waiting for the
callback timeout; some platform launchers remain attached to the browser lifetime.

## Agent runs on your machine: continuity and private mode

By default (`workspace_continuity`) `lyk daemon` runs Claude Code and Codex as your
own CLI: your sign-in, history, settings, permission rules, skills, instructions,
hooks, plugins and personal MCP servers all apply, and nothing is copied. Lykeion
adds its own MCP entries, always named with a `lyk_` prefix, and continues the
same CLI session for the next run of the same scope (for a board role,
`card:<id>/role:<role>`). A run the server marks `run_private`, any harness other
than `claude` and `codex`, and any workspace switched off below run in private
mode: an isolated config home holding only a copy of the sign-in file.

```sh
/tmp/lyk workspace continuity status    # mode, retention, recorded sessions
/tmp/lyk workspace continuity disable   # private mode for this workspace
/tmp/lyk workspace continuity enable
/tmp/lyk workspace continuity reset     # forget Lykeion's policy and session records
```

The policy lives in the daemon's state directory and applies to runs claimed
afterwards. Reset never touches your CLI history or sign-in.

Accepted limits of continuity mode: personal stdio MCP servers and hooks run inside
the agent's environment and inherit its run-scoped `LYK_TOKEN`, and the agent can
read your own `lyk` login file in your real `HOME`. Runs that must not see either
are marked `run_private`.
