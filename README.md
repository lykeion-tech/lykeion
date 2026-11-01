# Lykeion

Lykeion is a research lab where AI agents do the scientific work and people steer it. Each question becomes a study, and the study moves across a board of four stages. Agents do each stage's work, and a gate decides when the study moves on. Every claim an agent keeps must cite a recorded source.

![A Lykeion lab board with studies in progress](apps/web/public/lab-board.png)

## How it works

- **The board.** A workspace holds studies, and each study moves through four columns: **Grounding**, **Hypotheses**, **Experimentation** and **Validation**.
- **Studies.** A study is worked either by a **Team**, which has one agent per column, or by a single agent. The four stage Teams ship with Lykeion, and labs can add their own.
- **Gates.** A study advances only when the server's checks pass. Where a stage uses it, Jev (TypeSafe) also scores the work.
- **Evidence.** Sources come through Lykeion's sources server (OpenAlex, Europe PMC and arXiv). Attachments and the files agents save with `lyk output put` are recorded too, so every conclusion can be traced back.
- **Agents run on your machine.** The `lyk` daemon runs the agent CLIs you already use (Claude Code and Codex) inside the operating system's sandbox: Seatbelt on macOS, bubblewrap on Linux. It uses your own sign-in. If there is no sandbox, nothing runs. A lab machine can also serve colleagues' studies.
- **Around the board:**
  - **Autoresearch:** scheduled or triggered agent work.
  - **Coresearch:** shared projects with an Experiment mode (files, notebook kernels and recorded runs) and a Cowrite mode (a LaTeX paper edited together with agents).
  - **Aristotle:** the workspace's memory.
  - Grounding criteria the researcher writes, an Inbox, and scientific skills and data connectors.

## Repository layout

| Path                           | What it is                                                                                                          |
| ------------------------------ | ------------------------------------------------------------------------------------------------------------------- |
| `server/`                      | Go module: the API server, the `lyk` CLI and daemon, migrations, and the agent adapters (`pkg/agent`)               |
| `server/cmd/server`            | The HTTP, WebSocket and MCP server (chi, PostgreSQL 17 with row-level security per workspace, Redis 7)              |
| `server/cmd/lyk`               | The `lyk` CLI, used by people and agents: sign-in, workspaces, the board, the daemon, outputs, Teams and Coresearch |
| `server/cmd/migrate`           | Applies the embedded SQL migrations in `server/migrations`                                                          |
| `server/internal/pack/shipped` | The four built-in stage Teams                                                                                       |
| `apps/dashboard/`              | The Vue 3 + Vite + TypeScript dashboard                                                                             |
| `apps/web/`                    | The landing page, which links signed-in visitors into the dashboard                                                 |
| `apps/desktop/`                | The Electron shell around the dashboard, which also manages the local daemon                                        |
| `tests/`                       | Vitest unit tests, plus Playwright end-to-end tests in `tests/e2e`                                                  |
| `scripts/`                     | Development launchers and asset generators                                                                          |

## Requirements

- Go 1.26
- Node.js with npm
- PostgreSQL 17 and Redis 7
- For agent runs: Claude Code or Codex signed in on your machine

## Run locally

### 1. The server

The server needs two database roles:

- The **owner** role runs migrations.
- `lykeion_app` serves requests. The server refuses superusers and roles that bypass row-level security.

Migrations create `lykeion_app` without a login, so give it one yourself:

```sh
cd server
export MIGRATE_DATABASE_URL='postgres://owner:owner@127.0.0.1:5432/lykeion?sslmode=disable'
GOMAXPROCS=2 go run -p 1 ./cmd/migrate
psql "$MIGRATE_DATABASE_URL" -c "ALTER ROLE lykeion_app LOGIN PASSWORD 'app'"
```

Then start the server:

```sh
export DATABASE_URL='postgres://lykeion_app:app@127.0.0.1:5432/lykeion?sslmode=disable'
export REDIS_URL='redis://127.0.0.1:6379/0'
export JWT_SECRET="$(openssl rand -hex 32)"
export STORAGE='local:/absolute/path/to/blobs'   # or STORAGE=s3 with S3_BUCKET and S3_REGION
export LISTEN_ADDR='127.0.0.1:8080'
export PUBLIC_URL='http://127.0.0.1:8080'
export COOKIE_SECURE=false                       # plain HTTP, local only
GOMAXPROCS=2 go run -p 1 ./cmd/server
```

The server also applies migrations each time it starts.

| Variable                                                    | Purpose                                                                                            |
| ----------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `MIGRATE_DATABASE_URL`, `DATABASE_URL`, `REDIS_URL`         | Required                                                                                           |
| `JWT_SECRET`                                                | Required, at least 32 bytes                                                                        |
| `STORAGE`                                                   | `local:/abs/path` or `s3` (`S3_BUCKET`, `S3_REGION`, optional `S3_ENDPOINT` and AWS keys)          |
| `PUBLIC_URL`                                                | Where people reach the dashboard. Used for invite links, sign-in redirects and the sources MCP URL |
| `LYK_SECRET_KEY`                                            | Base64 of 32 bytes (`openssl rand -base64 32`). Seals connector keys on the server                 |
| `TYPESAFE_API_KEY`                                          | Turns on Jev scoring. It stays on the server                                                       |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`                  | Google sign-in. The redirect URI is `PUBLIC_URL` + `/auth/google/callback`                         |
| `SMTP_ADDR`, `SMTP_FROM`, `SMTP_USER`, `SMTP_PASS`          | Sign-in emails. Without SMTP, the server logs the codes instead                                    |
| `OPENALEX_API_KEY`, `CONTACT_EMAIL`, `SOURCES_EXCLUDE_FILE` | Sources server settings                                                                            |

[server/README.md](server/README.md) covers credentials, timeouts and the server's tests in more detail.

### 2. The dashboard

```sh
npm ci
npm run dev        # http://127.0.0.1:1430
```

Vite forwards `/api`, `/auth` and `/ws` to `LYK_API_URL`, which defaults to `http://127.0.0.1:8080`. It keeps the original Host header so the session cookie works.

### 3. The landing page and the dashboard on one origin

In production, the landing page is served at `/` and the dashboard at `/app/`, both on the same origin as the API. To get the same layout locally, run each of these in its own terminal:

```sh
npm run web:app    # dashboard under /app/ on :1430
npm run web        # landing page on http://127.0.0.1:1440, proxies /app and the API
```

Set `PUBLIC_URL=http://127.0.0.1:1440/app` on the server so sign-in comes back to the dashboard. `npm run dev:stack` starts the server, the dashboard and the landing page together, and stops all three when one exits. It does not start PostgreSQL or Redis.

On the landing page, signed-out visitors see **Get started**, which opens sign-in. Signed-in visitors are sent to onboarding, to workspace creation or to their studies, depending on how far they've got.

### 4. Your machine

Build the CLI, sign in, and start the daemon so agents can work your studies:

```sh
cd server && GOMAXPROCS=2 go build -p 1 -o /tmp/lyk ./cmd/lyk
/tmp/lyk login --email you@lab.org --server http://127.0.0.1:8080
/tmp/lyk workspace switch <slug>
/tmp/lyk daemon start      # or `daemon run` in the foreground; `daemon status` and `daemon logs` to inspect it
```

### Desktop app

```sh
npm run build
npm run desktop:dev
```

The preview uses its own `Lykeion Preview` data directory. It starts the local daemon for you.

## Checks

Run these one at a time:

```sh
npm run typecheck
npm run lint
npm test               # Vitest
npm run test:e2e       # Playwright
npm run build          # dashboard + desktop
npm run web:build      # landing page

cd server
GOMAXPROCS=2 go vet ./...
GOMAXPROCS=2 go test -p 1 ./... -count=1
```

The Go integration tests start PostgreSQL and Redis containers through Docker. Alternatively, set `LYK_TEST_PG` and `LYK_TEST_REDIS` to point at isolated services. These tests create and drop databases and flush Redis.

`npm run test:e2e` builds and starts a throwaway lab (`TestUILab`) and a dashboard on port 1431, then drives them with Chromium. It needs `npx playwright install chromium`, or `PLAYWRIGHT_CHROMIUM_EXECUTABLE` set to a Chromium you already have.

After changing SQL, regenerate the queries with `GOMAXPROCS=2 go tool sqlc generate` in `server/`, and commit the files in `pkg/db/generated`.

## Lineage and licences

Third-party code and text used in Lykeion are credited in [NOTICE](NOTICE).
