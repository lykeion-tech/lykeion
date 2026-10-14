# Task 1 report

Implemented the execution schema, tenant RLS policies, narrowly scoped security-definer functions, sqlc queries, and generated Go database types. The migration test checks that the app role can call both sweepers and that token lookup succeeds with no match; every setup and query error is checked.

## Evidence

- RED: `go test -p 1 ./migrations/` with the new test and migration temporarily withheld failed as expected: `relation "runtime" does not exist (SQLSTATE 42P01)`.
- GREEN: `go test -count=1 -p 1 ./migrations/` passed.
- Full server suite: `go test -count=1 -p 1 ./...` passed all packages sequentially.
- `go tool sqlc generate` completed; generated types include nullable `CustomName`, `QueueReason`, `FailureReason`, `LateUsageAt`, `int64` token counts, and the expected parameter/row types.
- `git diff --check` passed.

## Files

- `server/migrations/0002_execution.up.sql`
- `server/migrations/execution_test.go`
- `server/pkg/db/queries/execution.sql`
- `server/pkg/db/generated/execution.sql.go`
- `server/pkg/db/generated/models.go`

## Commit

`453f602` (`Add execution schema for runtimes and agent runs`).

## Concerns

None identified within Task 1 scope.
