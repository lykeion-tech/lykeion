# Repository Instructions

Lykeion is a research lab where agents carry out scientific work and people steer it. These instructions apply to coding agents working in this repository. Follow the user's current instructions when they differ from this file.

## Product and architecture

- A workspace contains studies. Studies can move through Grounding, Hypotheses, Experimentation and Validation; server checks and, where configured, Jev review govern progression. A study can use stage Teams or a single agent. Preserve stage, gate, review and completion behavior when changing adjacent features.
- Evidence and provenance are part of the product: use the sources server and recorded sources, attachments and outputs where the existing workflow requires them. Do not invent citations or bypass evidence checks.
- The Go server owns workspace data and authorization. PostgreSQL row-level security isolates workspaces; Redis supports realtime and other services. The `lyk` CLI and local daemon run supported agent CLIs on users' machines.
- Claude Code and Codex run inside the operating system sandbox. Preserve the no-sandbox, no-run boundary and the distinction between a user's machine and shared-machine access.
- Autoresearch, Aristotle, Decisions, Teams, Workflows and Coresearch are connected parts of the lab. Check their callers and affected data when changing shared contracts.

## Repository map

| Location | Responsibility |
| --- | --- |
| `server/cmd/server/` | HTTP, WebSocket and MCP server |
| `server/cmd/lyk/` | CLI and daemon entry point |
| `server/internal/` | Server, daemon, scientific workflows, authorization and storage |
| `server/pkg/agent/` | Agent CLI adapters and execution isolation |
| `server/migrations/` | PostgreSQL migrations |
| `server/pkg/db/queries/` and `server/pkg/db/generated/` | sqlc queries and generated Go code |
| `apps/dashboard/` | Vue 3, Vite and TypeScript application |
| `apps/desktop/` | Electron shell, local daemon management and desktop integration |
| `apps/web/` | Next.js landing page and public documentation |
| `packages/contracts/`, `packages/cli-icons.ts` | Shared contracts and CLI icon paths |
| `tests/`, `scripts/`, `deploy/` | Cross-cutting tests, development tooling and deployment |

Use `README.md`, `server/README.md`, `apps/desktop/README.md` and the relevant package scripts for setup and current commands. Some historical documentation can lag the implementation; verify consequential claims against the owning code and tests.

## Boundaries and security

- Scope workspace reads, writes, events and cache keys to the correct workspace. Keep membership and authorization checks at the server boundary; UI restrictions alone do not grant or deny access.
- Preserve the workspace transaction and RLS model. The server's serving role must not own tables or bypass RLS; migrations use separate credentials. See `server/README.md` before changing database roles or migration handling.
- `TYPESAFE_API_KEY` stays on the server. Connector secrets are sealed on the server. Never pass a person's own `lyk_` token to an agent, print credentials, or commit secrets.
- Preserve sandboxing, machine ownership, shared-run isolation and evidence boundaries. Do not widen access or weaken isolation as a convenience for a UI change.
- Inspect data deletion, cancellation and revocation paths together. Avoid irreversible actions against the user's data or services without authorization.
- Respect licenses when using third-party assets or code; check `NOTICE` and the upstream license before adding or copying material.

## State, APIs and UI

- Server data is fetched and cached through the dashboard's API and TanStack Query layers in `apps/dashboard/src/api/`. Keep workspace IDs in workspace-scoped keys and update realtime invalidation when mutations or events change cached data.
- Keep client-owned drafts, preferences and navigation state in the existing state and storage modules. Preserve their scope across account and workspace switches, and do not persist secrets in browser storage.
- Preserve API compatibility for installed desktop clients. When a response or request changes, update affected types, consumers and tests; handle absent or unknown values where mixed versions can encounter them.
- Reuse existing Vue components, styles, icons, routes and desktop bridges. Keep Electron-specific behavior in the desktop layer and web-specific behavior in `apps/web/`.
- Preserve keyboard access, focus, accessible names, loading and error states, and layouts at narrow widths. Keep user-facing descriptions focused on information needed to make a decision.
- Update public documentation in `apps/web/src/app/docs/` when a user-visible change makes it inaccurate. `docs/` and `research/` are currently ignored local directories; don't force-add local material without the user's direction.

## Database changes

- Add migrations under `server/migrations/` using the next available number after checking the current tree. Consider upgrades, rollback and existing data before changing a schema or constraint.
- For changes to sqlc queries or schemas, run `GOMAXPROCS=2 go tool sqlc generate` from `server/` and include the relevant generated changes. Review the generated diff.
- Maintain workspace predicates, transactions and RLS policies. Test cross-workspace access and the relevant failure paths when changing data boundaries.

## Development and verification

Use the existing checkout and preserve unrelated changes. Start with the narrowest meaningful tests, then broaden where the change warrants it. Report actual results and any checks not run.

| Scope | Useful checks |
| --- | --- |
| Dashboard and desktop types | `npm run typecheck` |
| Frontend unit tests | `npm test`; use focused Vitest files while iterating |
| Frontend lint | `npm run lint` |
| Browser flows | `npm run test:e2e`; use the relevant Playwright config for focused work |
| Dashboard and desktop build | `npm run build` |
| Website | `npm --prefix apps/web run lint`, `npm --prefix apps/web run typecheck`, `npm run web:build` |
| Go server | From `server/`: `GOMAXPROCS=2 go vet ./...` and `GOMAXPROCS=2 go test -p 1 ./... -count=1` |

Go integration tests can start disposable PostgreSQL and Redis containers or use `LYK_TEST_PG` and `LYK_TEST_REDIS`. They create and drop test databases, modify the test app role and flush Redis: use isolated test services, never production or an unrelated existing container. See `server/README.md` and `server/internal/testutil/`.

Use test-created fake or missing agent CLI paths in ordinary tests. Run a real agent CLI, connect a real account, deploy, push, publish or send messages only when the user has authorized that action. Never stop or remove containers or services belonging to another task.

## Changes and delivery

- Keep changes scoped and use existing patterns before adding dependencies or abstractions. Read the connected code, tests and callers before changing behavior.
- Test behavior that can fail, including authorization and data isolation where relevant. Do not claim a test, deployment or live check passed without evidence.
- Keep comments concise and useful for non-obvious constraints; remove stale comments when changing behavior. Keep generated files and their source in sync.
- Respect the user's requested branch, commit and delivery workflow. Preserve unrelated work, and do not push or deploy without authorization.
- Keep these instructions current when architecture, commands or boundaries change. `CLAUDE.md` imports this file so the shared rules have one source of truth.
