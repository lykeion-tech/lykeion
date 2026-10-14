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
without SMTP the development mailer logs codes.

`/health` reports process health. `/readyz` requires working PostgreSQL and Redis;
startup verifies both before announcing readiness. After startup, Redis loss
allows realtime delivery through database polling, while sign-in rate limiting
fails closed and `/readyz` returns 503. Redis remains required at startup.
SIGINT/SIGTERM close realtime sockets before database pool cleanup and allow
HTTP requests up to ten seconds to finish. HTTP headers have a ten-second timeout.
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
