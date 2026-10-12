# Implementation receipts

## Initial publication

Initial independent frontend, reference evidence and design committed as `3ed3f0f6e56be19e56b36fdf66167c8666966b69` on `main` and pushed to the requested private repository. Remote divergence verified as 0/0. Typecheck, 13 frontend unit tests, production build and design-catalogue checks passed. The build reports a non-blocking large JavaScript chunk warning. Raw accessibility evidence is preserved byte-for-byte.

## S00 — initial contract foundation verified

Implemented common/S01 wire contracts and reproducible TypeScript/Python model generation, deterministic gate replay, a bounded collaboration protocol spike and fresh native reference receipts. Later feature commands remain design-only until their slice schemas are frozen; no blanket claim that the complete catalogue is implemented.

Verification: 19 route contracts, 25 schemas and 13 frame variants validate; generated artifacts reproduce without drift; two Python contract tests and nine Go policy/collaboration tests pass with the race detector; existing frontend typecheck passes. The schema validator was patched and npm reports zero vulnerabilities. Full production coediting remains S06 work.

## S01 — active

Control foundation now compiles and passes three PostgreSQL integration tests: concurrent idempotent commands, cross-workspace denial, CSRF/session boundaries, cross-instance WebSocket delivery, replay and membership removal. Database migrations use checksums and replay safely. Provisioning is explicit via the CLI; no default users or seeded research exist. Full S01 remains active: membership lifecycle mutations, resource grants, client integration and reference acceptance are not yet complete. S02–S15 remain pending. Product features must not be marked supported by capabilities until the real backend path and required acceptance tests pass. The installed desktop remains separate and is only inspected as an interaction reference.
