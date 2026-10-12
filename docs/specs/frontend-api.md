# Frontend control-plane contract

> Fresh backend design: [API contracts](backend-api.md) and [implementation plan](../plans/backend-implementation.md) supersede the provisional transport below. Existing renderer implementation descriptions remain historical/current-code context, not backend authority.

> Implementation note: [Lykeion product and scientific workflow specification](scientific-workflow.md) v0.2 is authoritative for agreed behavior. The contracts and structures below describe the existing provisional frontend and must be reconciled with that specification before backend wiring.
This is a proposed API boundary for the independent renderer. No Go or Python implementation is supplied by this frontend rewrite. The renderer never imports the reference app's client, transport, or components. Types live in `packages/contracts/src/research.ts`; local and HTTP implementations share `ResearchService`.

## Connection and authority

Configure a base HTTP(S) URL in Connection settings, without credentials, query parameters, or fragments. Requests append `/v1`. Browser requests use cookie credentials; the Go service must implement authentication, CSRF protection, and an explicit allowed-origin policy before deployment. Python scientific services are reached through Go, never directly from this renderer. Secrets must not be stored in resource drafts or local storage.

A new connection is probed before replacing the current one. A failed probe or preference write leaves the previous service in place. Concurrent connection attempts use latest-request ownership, and workspace refreshes cancel superseded reads and ignore stale results. Invalid saved URLs fall back to the local draft service with a visible recovery message; the saved value remains available to correct.

HTTP responses are checked for required fields, collection structure, enum values, and inquiry revision shape before entering renderer state. Invalid JSON or incompatible payloads fail visibly. The frontend does not retry mutations automatically. Every mutation carries an `Idempotency-Key`; the backend must implement idempotency before any future retry facility is added. Inquiry mutations include the expected `revision` and must return HTTP 409 on conflicts.

## Endpoints

All response bodies below are JSON values directly, without an envelope.

| Method and route | Request | Response |
| --- | --- | --- |
| GET `/v1/workspace` | — | `WorkspaceSnapshot` |
| PATCH `/v1/workspace` | partial name, researchContext, taskPrefix | `Workspace` |
| POST `/v1/inquiries` | title, description, priority, projectId, assigneeId, labels, startDate, dueDate, parentId | `Inquiry` |
| GET `/v1/inquiries/:id` | — | `InquiryDetail` |
| PATCH `/v1/inquiries/:id` | revision plus editable inquiry fields | `Inquiry` |
| POST `/v1/inquiries/:id/comments` | text | `Activity` |
| POST `/v1/inquiries/:id/transitions` | revision, destination | `TransitionReceipt` |
| POST `/v1/projects` | name, description | `Project` |
| GET `/v1/resources/:kind` | — | `ResourceMap[kind][]` |
| GET `/v1/resources/:kind/:id` | — | `ResourceMap[kind]` |
| POST `/v1/resources/:kind` | `ResourceCreateMap[kind]` | `ResourceMap[kind]` |
| PATCH `/v1/resources/:kind/:id` | partial `ResourceCreateMap[kind]` | `ResourceMap[kind]` |

Non-success responses may provide `{ "message": "Readable error" }`; status codes are retained in `ServiceError`. Resource routes are typed proposals, not proof that these capabilities exist on a running backend. Authorization and scientific gates remain server responsibilities. A transition cannot be represented as a generic inquiry patch. Routing receipts name Jev and its policy/evidence references when present.

## Resource collections

Every workspace snapshot contains arrays for agents, runtimes, teams, skills, connectors, machines, colleagues, autopilots, conversations, and notifications, alongside inquiries and projects. A first installation returns empty arrays. `ResourceMap` and `ResourceCreateMap` define each record and permitted creation payload once. Runtime and machine collections are currently separate integration points; a future backend can explicitly map runtime sessions to host machines.

Local mode creates only user-requested drafts: agents are offline, connectors unconfigured, autopilots draft, and teams and skills inert. Creating a draft does not connect a provider, register an executable skill, start an agent, or activate a schedule. Local mode rejects machine/runtime registration, membership creation, conversations, and notifications because it cannot honestly supply the necessary server activity or authority. It never creates synthetic claims, runs, or seed records.

Local storage uses `lykeion.workspace.v1`. Existing snapshots without newly introduced resource arrays are read with empty collections and rewritten only upon an explicit mutation. Corrupt state is preserved and surfaced as an error rather than silently reset. This is single-browser draft persistence, not a transactional multi-user backend; cross-tab concurrency must not be relied upon.

## Remaining backend work

Implement authentication and CSRF/origin enforcement, workspace scoping and access control, persisted optimistic revisions and idempotency, resource lifecycle commands, pagination, live updates, and scientific transition admission. Generic resource creation must not be used to manufacture notifications or bypass machine pairing, invitations, or provider authorization. The API should expose dedicated lifecycle commands when those workflows are implemented. The current resource creation interface describes input data, not blanket client authority.

## Task auxiliary data

Inquiry patches can contain `wakeupDrafts` and `pullRequests`. Wakeup drafts remain inactive; saving them never schedules execution. Pull-request references are HTTPS links marked unverified, without fetching or enabling automation. Creation accepts an optional `parentId`; local mode checks that the parent exists.

`InquiryDetail.runs` is an optional validated collection of execution records and cost details. Only backend-provided records populate the timeline. An absent collection renders an empty state; it never creates simulated runs. Lifecycle authorization and activation require dedicated backend commands.
