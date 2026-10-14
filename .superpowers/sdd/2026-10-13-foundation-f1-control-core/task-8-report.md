# Task 8 report — workspace route isolation sweep

Base: `e7e93f56746b8ba8fb8234e3a23b7c5688f81d9d` on `codex/foundation-f1-control-core`.

Added `server/internal/handler/isolation_test.go`. The route walker exercises all 18 registered tenant-data API routes in the current router. For workspace URL routes it requests A's workspace as user B and requires 404, including admin-only routes whose scope middleware must hide workspace existence before role checks. For card and inbox resource routes it supplies A's IDs under B's workspace scope. Collection routes that have no foreign resource ID use A's workspace scope as user B. Write operations use valid route-specific bodies so validation errors cannot mask authorization behavior.

The test separately checks that B cannot read A's card, comments, or events, add a comment to A's card, or mark A's inbox item read while scoped to B. It verifies A's card count is unchanged and that the attempted comment did not create data. The test is named `TestWorkspaceRouteIsolation`, matching the required `-run Isolation` filter.

No route defect was observed, so no production handler or router changes were needed. The optional Task 6 deferred cross-workspace boundaries for comments, card events, and Inbox ownership are covered by the foreign-resource probes. `follows_from` remains guarded by the existing card creation validation and is outside this route sweep's necessary scope.

## Verification

All Go commands ran from `server/`, with `GOMAXPROCS=2`, `-p 1`, and the supplied local PostgreSQL and Redis services. Each process exited before the next began.

- Focused audit: `go test -p 1 ./internal/handler/ -run Isolation -v` passed; output logged `checked 18 registered workspace routes`.
- Handler suite: `go test -p 1 ./internal/handler/ -v` passed.
- Full current module: `go test -p 1 ./... -count=1 -timeout 90s` passed across all server packages.
- One initial full-module attempt used an incorrect Redis URL containing credentials and failed with `WRONGPASS`; rerunning with the exact supplied Redis URL passed.

No known blocker remains for Task 8.
