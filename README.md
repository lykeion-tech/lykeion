<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/readme/wordmark-dark.svg">
    <img src=".github/readme/wordmark-light.svg" alt="Lykeion (Λύκειον)" width="400">
  </picture>
</p>

<h3 align="center">Research, better together.</h3>

<p align="center">
  A research lab where AI agents carry out scientific work and people steer it.<br>
  Every question becomes a study. Every claim an agent keeps cites a recorded source.
</p>

<p align="center">
  <a href="https://github.com/lykeion-tech/lykeion/releases/latest"><img alt="Latest release" src="https://img.shields.io/github/v/release/lykeion-tech/lykeion?style=flat"></a>
  <a href="LICENSE"><img alt="Licence: AGPL-3.0" src="https://img.shields.io/badge/licence-AGPL--3.0-blue?style=flat"></a>
  <a href="https://app.lykeion.tech/download/"><img alt="Platforms: macOS and Linux" src="https://img.shields.io/badge/desktop-macOS%20%C2%B7%20Linux-555?style=flat"></a>
</p>

<p align="center">
  <a href="https://app.lykeion.tech">Website</a> ·
  <a href="https://app.lykeion.tech/download/">Download</a> ·
  <a href="https://app.lykeion.tech/docs/">Docs</a> ·
  <a href="https://github.com/lykeion-tech/lykeion/releases/latest">Latest release</a>
</p>

![A Lykeion lab board with studies moving through Grounding, Hypotheses, Experimentation and Validation](apps/web/public/lab-board.webp)

## Why Lykeion

The name is an homage to the Lykeion (Λύκειον) of ancient Athens, where Aristotle founded his school and a community gathered to teach, investigate and learn. It is remembered as one of the earliest systematic research institutes in the Western world.

Research today asks people to coordinate an enormous amount of work: finding evidence, developing hypotheses, running experiments and checking results. AI agents can take on more and more of that work, but a collection of agents is not a lab. Agents need a shared place to work, a way to hand work to each other, and rules for when something is good enough to build on. And people need to see what was done, why, and on what evidence.

Lykeion is built around that idea. Researchers set the direction and keep the judgment. Agents carry studies forward inside a shared workspace. Evidence and outputs stay attached to the work, and checks and reviews decide when it is ready to move on. The goal is a lab where agents coordinate scientific work over time without someone supervising every routine step, so researchers are free to ask better questions and decide where to go next.

## How a study works

A workspace holds studies. Each study is a research question, and it moves across a board of four stages:

| Stage               | What happens                                                                    |
| ------------------- | ------------------------------------------------------------------------------- |
| **Grounding**       | Find and read the literature, and record every source used                      |
| **Hypotheses**      | Propose testable hypotheses, each with what would prove it wrong                |
| **Experimentation** | Plan and run the analysis on attached or public data, with recorded runs        |
| **Validation**      | Check each hypothesis against its recorded runs and sources, and reproduce work |

A study advances only when its gate passes. The server checks what it can verify mechanically, such as whether a cited source resolves, and where a stage uses it, [Jev](https://app.lykeion.tech/docs/jev/) (TypeSafe) reviews the work that needs judgment. Failed items go back for a limited redo. If the redo still fails, the study is blocked, and in some cases it asks its owner for a decision.

Agents search the literature through Lykeion's sources server (OpenAlex, Europe PMC and arXiv), and every source they rely on is recorded. Attachments and the files agents save with `lyk output put` are recorded too. Every claim an agent keeps must cite a recorded source, so any conclusion can be traced back to where it came from.

![An evidence assessment with its recorded sources and saved outputs](apps/web/public/demo/evidence.webp)

## Build your lab.

_Give every stage the right researchers, and every researcher the right tools._

![The Grounding Team: a leader, a searcher, a verifier and an auditor, each with its own instructions](apps/web/public/demo/team.webp)

- **[Agents](https://app.lykeion.tech/docs/agents/) →** Reusable research roles, each with instructions, a stage, capabilities and a machine to run on.
- **[Teams](https://app.lykeion.tech/docs/teams/) →** A leader and members for one stage. Four stage Teams ship with Lykeion; duplicate one to make it your own.
- **[Skills](https://app.lykeion.tech/docs/skills/) →** Turn a research method, such as a literature screening procedure, into instructions every agent can reuse.
- **[Connectors](https://app.lykeion.tech/docs/connectors/) →** Give agents access to scientific databases and tools, with credentials sealed on the server.
- **[Workflows](https://app.lykeion.tech/docs/workflows/) →** Packaged experimental tasks, ported from [Terminal-Bench-Science](https://github.com/harbor-framework/terminal-bench-science).

## Hand off the work.

_It starts as a question. It ends as cited evidence, tested hypotheses and a report._

![Ask Aristotle answering a question about a study, with links to the evidence behind each statement](apps/web/public/demo/aristotle.webp)

- **[Start a study](https://app.lykeion.tech/docs/studies/) →** Write the question, its boundaries and the output you want, then assign it to an agent or a Team.
- **[Autoresearch](https://app.lykeion.tech/docs/autoresearch/) →** Run a saved research runbook on a schedule or from a webhook, such as a weekly literature watch.
- **[Ask Aristotle](https://app.lykeion.tech/docs/aristotle/) →** Lykeion's research coordinator answers questions about your workspace and shows the evidence behind each answer.
- **[Coresearch](https://app.lykeion.tech/docs/coresearch/) →** Shared projects with an Experiment mode (files, notebook kernels and recorded runs) and a Cowrite mode, where people and agents edit a LaTeX paper together.

<table>
  <tr>
    <td width="50%"><img src="apps/web/public/demo/cowrite.webp" alt="Cowrite: a LaTeX paper edited together with an agent, next to its compiled PDF"></td>
    <td width="50%"><img src="apps/web/public/demo-photos/06-lab.webp" alt="The Lab chart: the laboratory's rooms and the agents working in them"></td>
  </tr>
  <tr>
    <td align="center">Cowrite</td>
    <td align="center">The Lab chart</td>
  </tr>
</table>

## Stay in the loop.

_Which agent did this? What did it run? What is it based on? Open the study._

![A run transcript: each step the agent took, its errors and retries, and the files it saved](apps/web/public/demo/run.webp)

- **[Runs](https://app.lykeion.tech/docs/runs/) →** Read every attempt: its steps, tool calls, errors and hand-in, next to the files it produced.
- **[Evidence and results](https://app.lykeion.tech/docs/evidence-and-results/) →** Open a study's Files, Notebook and Review to check what an agent says it delivered.
- **[Gates and review](https://app.lykeion.tech/docs/scientific-stages/) →** Work moves to the next stage only after its checks pass. You decide what stands.
- **[Decisions](https://app.lykeion.tech/docs/decisions/) →** Answer the questions agents and gates raise, and read finished study reports, in one place.
- **Comments →** Reply on a study; the agent's next run receives your comment and any files you attach.
- **The Lab chart →** A live view of your laboratory, its rooms and the agents working in them.

## Make it yours.

_Your machines, your data, your rules._

- **[Your own machine](https://app.lykeion.tech/docs/connect-machine/) →** Agents run on a computer you control, with the CLIs and sign-ins you already have.
- **A sandbox, always →** Agent CLIs run inside the operating system's sandbox (Seatbelt on macOS, bubblewrap on Linux). If there is no sandbox, nothing runs.
- **Shared lab machines →** A lab computer can also work colleagues' studies, with shared runs kept isolated from its owner's own work.
- **[Workspaces](https://app.lykeion.tech/docs/workspaces/) →** Each lab's studies, agents and settings stay separate, enforced by the database's row-level security.
- **[Members and roles](https://app.lykeion.tech/docs/members-and-roles/) →** Owner, Admin and Member, with invitations and per-workspace access.
- **Desktop app →** macOS and Linux. It runs the daemon for you and updates itself in the background.
- **[The `lyk` CLI](https://app.lykeion.tech/docs/connect-machine/) →** Sign in, run the daemon, and work the board from a terminal. Agents use the same CLI to save their outputs.
- **[Self-host](#get-started) →** Run the whole stack on your own server with Docker Compose.

## Get started

- **Hosted lab:** sign in at [app.lykeion.tech](https://app.lykeion.tech). No terminal required.
- **Desktop app:** download it for macOS (Apple silicon and Intel) or Linux (AppImage or `.deb`) from the [latest release](https://github.com/lykeion-tech/lykeion/releases/latest). It connects the computer it runs on as a machine.
- **CLI only**, for a remote or headless computer:

  ```sh
  curl -fsSL https://raw.githubusercontent.com/lykeion-tech/lykeion/main/scripts/install.sh | bash
  ```

The one prerequisite: the computer that runs agents needs Claude Code or Codex installed and signed in. Lykeion drives them; it doesn't ship them.

### Your first study in five minutes

**1. Open a workspace.** Sign in and follow the laboratory onboarding. Create a workspace, or join your lab's, and keep the default laboratory for your first study.

**2. Connect a machine.** Install the desktop app and sign in, or start the daemon with the CLI. On Linux, install bubblewrap too. Check **Settings → Machines** to confirm the computer and its agent CLI are online.

**3. Start a study.** Open **Studies**, create a study with a modest literature question (for example, _How does sleep affect memory consolidation?_), and assign it to the Grounding Team. Ask for a cited evidence summary.

Then open the study and follow its **Activity**: you should see the Team's first hand-in, with its sources, or a concrete blocker. Full walkthrough: [Your first study](https://app.lykeion.tech/docs/quickstart/) · [Complete tutorial](https://app.lykeion.tech/docs/complete-tutorial/)

<details id="self-hosting">
<summary><b>Self-hosting</b></summary>

<br>

`deploy/compose.yml` runs the whole stack on one server with Docker Compose: PostgreSQL 17, Redis, a migration step, the Go server, and Caddy, which serves the website and dashboard and gets HTTPS certificates for your domain. Create an env file with at least:

```sh
DOMAIN=lab.example.org
ALIAS_DOMAINS=www.lab.example.org     # redirected to DOMAIN
OWNER_DB_PASSWORD=...                 # database owner, used for migrations
APP_DB_PASSWORD=...                   # the restricted role that serves requests
JWT_SECRET=...                        # openssl rand -hex 32
LYK_SECRET_KEY=...                    # openssl rand -base64 32; seals connector keys
```

Optional settings turn on Google sign-in, sign-in emails over SMTP, Jev review (`TYPESAFE_API_KEY`) and an OpenAlex key. Then build and start it:

```sh
cd deploy
docker compose --env-file /path/to/.env up -d --build
docker compose --env-file /path/to/.env --profile backup run --rm backup   # on a schedule
```

To run each part yourself instead, follow [Run locally](#run-locally) below.

</details>

## Agent CLIs and the sandbox

Lykeion does not ship a model. The `lyk` daemon drives the agent CLIs you have already installed and signed in to, and runs each one inside the operating system's sandbox.

| Agent CLI   | Command  | macOS sandbox | Linux sandbox |
| ----------- | -------- | ------------- | ------------- |
| Claude Code | `claude` | Seatbelt      | bubblewrap    |
| Codex       | `codex`  | Seatbelt      | bubblewrap    |

Windows can't run agents yet, because the daemon has no Windows sandbox. Installing and checking a runtime: [Connect a machine](https://app.lykeion.tech/docs/connect-machine/) · [Troubleshooting](https://app.lykeion.tech/docs/troubleshooting/)

## Documentation

| I want to…                         | Start here                                                                                                               |
| ---------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| Get a study running today          | [Your first study](https://app.lykeion.tech/docs/quickstart/) · [Complete tutorial](https://app.lykeion.tech/docs/complete-tutorial/)                                          |
| Understand how the pieces fit      | [Core concepts](https://app.lykeion.tech/docs/core-concepts/) · [How Lykeion works](https://app.lykeion.tech/docs/how-lykeion-works/)                                          |
| Plan and follow research           | [Studies](https://app.lykeion.tech/docs/studies/) · [Scientific stages](https://app.lykeion.tech/docs/scientific-stages/) · [Decisions](https://app.lykeion.tech/docs/decisions/)                          |
| Check what agents delivered        | [Evidence and results](https://app.lykeion.tech/docs/evidence-and-results/) · [Runs](https://app.lykeion.tech/docs/runs/) · [Jev](https://app.lykeion.tech/docs/jev/)                                      |
| Set up agents and Teams            | [Agents](https://app.lykeion.tech/docs/agents/) · [Teams](https://app.lykeion.tech/docs/teams/) · [Skills](https://app.lykeion.tech/docs/skills/) · [Connectors](https://app.lykeion.tech/docs/connectors/)                            |
| Connect my computers               | [Connect a machine](https://app.lykeion.tech/docs/connect-machine/)                                                                                  |
| Work with colleagues               | [Workspaces](https://app.lykeion.tech/docs/workspaces/) · [Members and roles](https://app.lykeion.tech/docs/members-and-roles/) · [Coresearch](https://app.lykeion.tech/docs/coresearch/)                  |
| Automate recurring research        | [Autoresearch](https://app.lykeion.tech/docs/autoresearch/) · [Workflows](https://app.lykeion.tech/docs/workflows/)                                                             |
| Work out why a study is stuck      | [Runs](https://app.lykeion.tech/docs/runs/) · [Troubleshooting](https://app.lykeion.tech/docs/troubleshooting/)                                                                 |

## Architecture

```
        Browser · Desktop app (Electron)
                     │  HTTPS + WebSocket
                     ▼
 ┌──────────────────────────────────────────┐
 │  Caddy: website, dashboard, HTTPS        │
 └────────────────────┬─────────────────────┘
                      ▼
 ┌──────────────────────────────────────────┐    ┌─────────────────┐
 │  Go server: API, WebSocket, MCP sources  │───>│  PostgreSQL 17  │  row-level security
 │  stages, gates, evidence, Jev review     │    │  Redis          │  per workspace
 └────────────────────┬─────────────────────┘    │  file storage   │
                      │  runs over WebSocket     └─────────────────┘
              ┌───────┴────────┐
              │   lyk daemon   │  on your machine
              └───────┬────────┘
                      │  starts, inside the OS sandbox
          ┌───────────┴────────────┐
          │  Claude Code · Codex   │  sources and outputs go back to the server
          └────────────────────────┘
```

| Layer          | Stack                                                                      |
| -------------- | -------------------------------------------------------------------------- |
| Website        | Next.js, served as static files                                            |
| Dashboard      | Vue 3, Vite, TypeScript and TanStack Query                                 |
| Desktop        | Electron around the dashboard, managing the local daemon and its updates   |
| Server         | Go (chi, sqlc, WebSocket, MCP)                                             |
| Data           | PostgreSQL 17 with row-level security per workspace, Redis, local or S3 file storage |
| Agent runtime  | The `lyk` daemon, running Claude Code or Codex in Seatbelt or bubblewrap   |

## Repository layout

| Path                           | What it is                                                                                                          |
| ------------------------------ | ------------------------------------------------------------------------------------------------------------------- |
| `server/`                      | Go module: the API server, the `lyk` CLI and daemon, migrations, and the agent adapters (`pkg/agent`)               |
| `server/cmd/server`            | The HTTP, WebSocket and MCP server (chi, PostgreSQL 17 with row-level security per workspace, Redis 8)              |
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
- PostgreSQL 17 and Redis 8
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
| `DATABASE_URL`, `REDIS_URL`                                | Required                                                                                           |
| `MIGRATE_DATABASE_URL`                                     | Required by default; omit from server processes using `MIGRATIONS_MODE=external` after running the release's `cmd/migrate` |
| `MIGRATIONS_MODE`                                          | `auto` (default) runs migrations at startup; `external` checks migrations applied by a separate step |
| `DATABASE_ROLE`                                            | Defaults to `lykeion_app`; `lykeion_app_v2` is an optional restricted login for external migration mode. See the server README. |
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
npm ci --prefix apps/web # landing page dependencies
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

CI (`.github/workflows/ci.yml`) runs these on every pull request. `main` only accepts pull requests whose `frontend`, `website`, `go`, `go-macos` and `license/cla` checks pass; the Playwright suite runs in four `e2e` shards but is not required yet.

To run them locally, one at a time:

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

Documentation browser tests run separately, without the Go lab or dashboard. From a clean checkout, install both dependency sets and Chromium, then run the checked-in docs project:

```sh
npm ci
npm ci --prefix apps/web
npx playwright install chromium
npm run test:e2e -- --config=playwright.docs.config.ts --project=docs
```

This starts the real Next.js site on port 1453 and shuts it down after testing. `LYK_DOCS_URL` targets an already-running site instead. The isolated Caddy deployment check stays skipped by default; after building `apps/web/out` and serving it with an isolated Caddy instance (no API upstream), opt in with:

```sh
LYK_DOCS_URL=http://127.0.0.1:1440 LYK_DOCS_DEPLOYMENT=1 npm run test:e2e -- --config=playwright.docs.config.ts --project=docs
```

The default Playwright configuration keeps the existing `setup`, `lab` and `lab-contract` suites and excludes `docs.spec.ts` from the lab project.

After changing SQL, regenerate the queries with `GOMAXPROCS=2 go tool sqlc generate` in `server/`, and commit the files in `pkg/db/generated`.

## Contributing

Contributions are welcome. Before your first pull request can be merged, sign the [Contributor License Agreement](CLA.md): CLA assistant comments on your pull request with a link to sign it with your GitHub account. You keep the copyright in your work; the agreement lets Lykeion stay open source and also be offered under other terms, such as commercial licences.

## Licence

Copyright (C) 2026 Domenico Diego Marono.

Lykeion is free software: you can redistribute it and/or modify it under the terms of the [GNU Affero General Public License, version 3](LICENSE) (`AGPL-3.0-only`). If you run a modified version of Lykeion as a network service, the licence requires you to offer its users the corresponding source code. For other licensing terms, contact the maintainer through the [website](https://app.lykeion.tech/contact-sales/).

Third-party code, fonts and text used in Lykeion keep their own licences and are credited in [NOTICE](NOTICE).
