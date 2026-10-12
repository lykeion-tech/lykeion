# Implementation receipts

## Initial publication

Initial independent frontend, reference evidence and design committed as `3ed3f0f6e56be19e56b36fdf66167c8666966b69` on `main` and pushed to the requested private repository. Remote divergence verified as 0/0. Typecheck, 13 frontend unit tests, production build and design-catalogue checks passed. The build reports a non-blocking large JavaScript chunk warning. Raw accessibility evidence is preserved byte-for-byte.

## S00 — initial contract foundation verified

Implemented common/S01 wire contracts and reproducible TypeScript/Python model generation, deterministic gate replay, a bounded collaboration protocol spike and fresh native reference receipts. Later feature commands remain design-only until their slice schemas are frozen; no blanket claim that the complete catalogue is implemented.

Verification: 19 route contracts, 25 schemas and 13 frame variants validate; generated artifacts reproduce without drift; two Python contract tests and nine Go policy/collaboration tests pass with the race detector; existing frontend typecheck passes. The schema validator was patched and npm reports zero vulnerabilities. Full production coediting remains S06 work.

## S01 — active

Control foundation now compiles and passes three PostgreSQL integration tests: concurrent idempotent commands, cross-workspace denial, CSRF/session boundaries, cross-instance WebSocket delivery, replay and membership removal. Database migrations use checksums and replay safely. Provisioning is explicit via the CLI; no default users or seeded research exist. Full S01 remains active: membership lifecycle mutations, resource grants, client integration and reference acceptance are not yet complete. S02–S15 remain pending. Product features must not be marked supported by capabilities until the real backend path and required acceptance tests pass. The installed desktop remains separate and is only inspected as an interaction reference.

### S01 membership and frontend checkpoint

Added migrations 002–003, recipient-bound invitation creation/acceptance/revocation, member removal and leave, pinned access-change decisions and recipient-approved ownership transfer. Exact-version responses apply their authorized effects atomically. The TypeScript client validates responses against generated schemas and preserves the original request on ambiguous transport retries. The desktop bridge holds its cookie in the main process, restricts destinations/routes and owns its authenticated WebSocket; web clients use cookie-authenticated WebSockets. Both retain durable HTTP replay fallback.

Colleagues keeps its name, route, introduction and compact row layout, and now contains membership management. Settings points users there. The installed reference was inspected without changing its accounts or data. A separate local acceptance schema contains only two synthetic accounts and no research programmes. Native invitation creation, web acceptance, cross-client membership refresh and native structured access approval were observed. With explicit user approval, the synthetic recipient left the shared workspace: the browser selected its remaining workspace and the owner list excluded the departed account. Lost-response leave retries recover the original receipt without restoring access; a new command after departure is refused. A reactive-clone failure found during the UI check was fixed and covered by a regression test. Reference and rebuild screenshots are in `research/visual-baseline/s01`.

This remains an **S01 checkpoint, not S01 completion**: programme creation/resource grants depend on sealed policy/profile artifacts; presence expiry, production rate/queue limits, persistent desktop credential storage, full CLI command parity and broader recovery acceptance remain outstanding. Membership decisions are a bounded S02 prerequisite, not a completed scientific Inbox or policy gate. Later slices remain pending.

Verification for this checkpoint: 15 Go tests passed with the race detector in isolated PostgreSQL schemas; 20 frontend/client/native-bridge tests passed; 30 schemas, 26 routes and 13 frame variants validated; TypeScript/Python generation reproduced without drift. The leave recovery test covers same-request replay, changed-request rejection and refusal of new commands after departure.

Final validation: production web/desktop build and TypeScript checks passed (the build retains a non-blocking large-chunk warning); both Python contract tests passed. Screenshot hashes are recorded in the S01 evidence manifest.


### S01 recovery, realtime and programme checkpoint (2026-10-13)

Added encrypted endpoint-bound desktop session storage, startup recovery and dedicated CLI authentication. The approved synthetic owner session survived preview restart without a new login code; no installed-reference credentials were used. Added transactional PostgreSQL event hints, expiring workspace presence, shared request/connection limits and reconnect backoff. Fixed stale browser socket closure affecting a newer heartbeat.

Implemented the bounded sealed-configuration prerequisite: hash-verified uploads, immutable manifests, owner-created programmes with pinned profiles/policies and explicit dimensioned ceilings, and versioned programme access plans. Reads filter programme grants; membership departure revokes them permanently rather than restoring them upon rejoin. The CLI can send the same versioned commands and uploads as the UI. Capability declarations omit these commands if the artifact store is unconfigured.

Native reference Researches empty state and creation form were inspected without submission. In the fresh preview, a synthetic zero-budget programme was created through the UI and its pinned constraints and owner grant were observed. Native file-picker focus required recovery; the final creation succeeded. Evidence is in `research/visual-baseline/s01/continuation`.

S01 remains active pending the remaining recovery/access acceptance cases. S02 has configuration-storage prerequisites only; general gate lifecycle, scientific Inbox, roles, reviewer independence and policy editing are not complete. S03–S15 remain pending. No scientific findings or experiments were generated by these checks.

Verification: 22 Go tests passed with the race detector against isolated PostgreSQL schemas; 21 frontend/native-client tests and two Python tests passed. Contracts validate with 42 schemas, 35 routes and 13 frame variants and regenerate without drift. Web/desktop production build passed with the existing large-chunk warning. Native screenshots have a separate continuation hash manifest.
