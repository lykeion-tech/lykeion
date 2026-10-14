# Task 7 report — realtime cursor replay

Base: `d426c2644e1f92ff8aff00d399d65922b24b76e2` on `codex/foundation-f1-control-core`.

Implemented the hub in `server/internal/realtime/hub.go`, with integration tests in `server/internal/handler/ws_test.go` and the deferred committed-publication and direct event-order regressions in `server/internal/store/store_test.go`. Frontend and unrelated implementation files remain untouched.

The authenticated `/ws` route accepts the prescribed subscribe message and sends event envelopes from the database in ascending ID order. Replay pages are 500 events; Redis publishes one wake-up per affected workspace, using `lyk:ws:<workspace>`, and never supplies delivered event content. The subscription is acknowledged before the first replay to avoid a replay/live wake-up gap. Polling remains active with healthy Redis and supplies delivery when Redis is absent or unavailable. A dedicated subscription client applies 250 ms context and socket bounds to the initial dial/handshake/acknowledgment, preventing a reachable but silent Redis from stalling replay. Default Gorilla origin protection is retained.

Each replay page opens the scoped store transaction and acquires `LockMember` (`FOR SHARE`) before reading events, holding that lock through every WebSocket write. Removal cannot commit between authorization and delivery. These transactions only read tenant data and do not take a workspace mutation lock after the membership lock, avoiding an inversion of the existing workspace-before-member order used by mutation requests. The entire flush, across pages and database reads, has a single two-second context and write deadline; it does not extend that deadline per event. Non-members receive the prescribed workspace-not-found error and the socket closes; removal is rechecked even when no events follow the cursor.

`Hub.Close() error` is idempotent, marks the hub closed, cancels and closes its registered connections, and waits for each handler to finish its reader and subscription cleanup. Registration is serialized with closure to prevent WaitGroup additions after shutdown begins. Task 9 must call this helper during server shutdown because HTTP shutdown does not own hijacked connections.

The store tests now read each published event from a separate PostgreSQL connection inside the callback, proving committed visibility. The direct concurrent Emit test holds the first transaction before commit and observes the second waiting on the workspace advisory lock. It verifies cursor ordering and a subsequent resume, independently of HTTP workspace locks that could conceal a missing event-insert lock. No store production change was required.

## Original helper review

Searched the original `/Users/diegomarono/lykeion` checkout for Go realtime/WebSocket/hub helpers and inspected `daemon/internal/daemon/wsrpc.go`. Its `wsRPCClient` and cancelable `wsOutbound` implement multiplexed daemon RPC request correlation, capability negotiation, and uncertain-outcome fallback, rather than a workspace event-table subscription hub. There is no compatible database replay or member-lock helper to port without importing a different protocol. The new implementation faithfully uses the assigned F1 protocol and existing scoped store/authentication helpers; it does not transplant the original daemon RPC envelope or queue semantics.

## Verification

All Go commands ran with `GOMAXPROCS=2` and `-p 1`, against `LYK_TEST_PG=postgres://owner:owner@127.0.0.1:15437/postgres?sslmode=disable` and `LYK_TEST_REDIS=redis://127.0.0.1:16387/0`. Every running command was polled to an exit result before the next test/build command began.

- RED: `go test -p 1 ./internal/handler/ -run 'Replay|Reconnect|NonMember|Polling|WS' -count=1` failed on the 501 stub/bad handshake, as expected. A separate `WSClose|WSBlocked` red run also failed on the stub before hub implementation.
- Focused GREEN: the same handler filter passed (5.679 s), covering replay/live Redis wake-up; concurrent card emits and reconnect; non-member refusal and removal; nil-Redis polling; 1,151 events spanning pages with concurrent live inserts; a reachable but silent Redis endpoint; authentication and origin rejection; removal committing while replay awaits its membership lock; hub shutdown; and backpressured writes holding membership until a bounded deadline.
- Focused store: `go test -p 1 ./internal/store/ -run 'Emit|Publish' -count=1` passed (1.202 s).
- Mutation RED proof: a temporary Go `-overlay` moved Publish before commit and removed the event-insert advisory lock. `TestEmitPublishesAfterCommitOnly` failed with `no rows in result set` from the separate observer, and `TestDirectConcurrentEmitCommitsInCursorOrder` failed because the second event committed before the first. The real production file was never modified by this experiment.
- Race: `go test -p 1 -race ./internal/handler/ ./internal/store/ -count=1` passed (handler 14.502 s; store 1.897 s; exit 0). An attempted formatting adjustment used a wrong relative path before this command, emitted a file-not-found error, and made no edits; this did not affect the successful race run.
- Full current server module: `go test -p 1 ./... -count=1 -timeout 90s` passed (handler 7.871 s; all other tested packages passed; exit 0).
- Sequential module build: `go build -p 1 ./...` passed (exit 0), launched after the full suite had exited.
- `git diff --check` passed. Final changes after verification only normalized test import grouping and added this report.

## Limits and integration note

Slow peers whose replay cannot complete within the flush deadline are disconnected and can resume from their last received event ID. Redis wake-up publication remains best effort and uses the provided client/request context; the event-table commit remains authoritative. The server lifecycle call to `Hub.Close` belongs to Task 9. No open blocker remains for Task 7.
