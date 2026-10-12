# S00 contract freeze and technical decisions

The initial deployment is an authenticated, self-hosted Go control service with PostgreSQL, independently supervised Go workers and a private Python service. No remote compute or external identity provider is enabled implicitly. A Go administrative CLI will provision a named owner/workspace explicitly and generate an expiring single-use installation code; the service never starts with a seeded owner, default password or public bootstrap endpoint. This supports local development and self-hosted teams; hosted OIDC remains a later identity adapter with explicit configuration.

- Go patch toolchain: 1.26.9; module versions and checksums in `backend/go.mod`/`go.sum`.
- Node: 25.2.1; dependency resolution locked in `package-lock.json`.
- Python: 3.14.6; direct constraints and complete resolution in `science/pyproject.toml`/`uv.lock`.
- PostgreSQL: isolated development instance; test image must be pinned by digest when acquired. No reference-app database or credentials are reused.
- Public contract: `contracts/control.openapi.json`, closed schemas for common records and the first S01 commands. Endpoint schema presence is not runtime support; capabilities advertise implemented commands only. JSON is also a valid OpenAPI serialization.
- Workspace frames: `contracts/realtime.protocol.json`. Scientific mutations and document edits remain HTTP commands; frames synchronize committed state and ephemeral presence. Worker/science concrete endpoint schemas are frozen with their respective implementation slices, before dispatch is enabled.
- jev: optional model classifications are sealed evidence inputs. `backend/internal/policy` deterministically evaluates recorded structured facts and versioned ordered rules. No live model call or prose argument enters replay; no vendor credential is required for replay tests. An actual provider classifier is a later approved adapter, not simulated by this engine.
- Collaboration spike: independently authored server-authoritative stable-atom rebasing within named text blocks. Positions are Unicode code points; concurrent insert/delete/format operations resolve against the specified base revision. Each edit retains author and operation ID, duplicates have one effect, undo removes only that author's contribution, old compacted bases require merge, and sealing rejects edits. Stable atom rebasing specializes the plan's bounded operation-transformation option. This is a bounded in-memory algorithm spike, not the production persistent collaboration service or a claim of complete rich-text editor support.
- Budget, WIP, lease and scientific profile parameters have no invented production defaults; required policy manifests must exist before their dependent commands can execute. A schema reference is never proof of an accepted policy.

## Desktop reference receipt

Fresh native captures in `research/visual-baseline/s00/` show the installed app's board and Members filter. Selecting Members shows the empty task state without creating work; All was restored afterward. The reference has Blocked as a column and no Backlog/Done in that view. The agreed new scientific board intentionally differs, while layout and controls remain interaction references. No reference user data is seeded into the rebuild.

## Exit accounting

S00 freezes the initial API substrate and verifies gate replay and bounded edit convergence. It does not freeze every later feature payload blindly. S01 starts only after generated types, schema validation and spike tests pass. Remaining full live coediting, authorization, database transactions and feature UI checks belong to S01/S06 and must pass there before capability enablement.
