# Task 1 review

Spec compliance: changes requested. Quality: changes requested.

## Finding

- [P2] Serialize bootstrap with membership departure (`server/internal/handler/aristotle.go:41-69`, particularly project creation at line 62). The advisory lock serializes bootstrap requests only. `workspace.go:338` calls `CoresearchDepart` before deleting membership, and that function enumerates existing projects without taking the bootstrap advisory lock. A concurrent bootstrap can create/commit its private project after departure's project enumeration but before membership deletion. Deletion then cascades the conversation and project membership, leaving the backing project unarchived and permanently inaccessible. This contradicts the reported lifecycle guarantee that departure archives backing projects and lets ordinary startup accumulate abandoned projects across remove/rejoin cycles. Coordinate bootstrap and departure on the same workspace/member lock, acquired before departure enumerates projects and before bootstrap creates them, and recheck membership after acquiring it. Add a deterministically interleaved removal/bootstrap regression asserting no surviving unarchived backing project without membership/conversation.

## Positive assessment and boundaries

Bootstrap-to-bootstrap concurrency is properly serialized; the uniqueness indexes preserve ordinary Aristotle/name behavior, and the upsert does not reset settings. The project classification and RLS function preserve personal project privacy, with classified projects excluded from ordinary listing and protected from sharing/archive mutations. Existing admin authorization for agent editing is retained. Ordered starter validation and persistence meet the task requirements. Snapshot columns and the all-or-none constraint are feasible for Task 2's first-acceptance transaction; execution/bypass handling remains explicitly owned by Task 2.

Review was read-only apart from this report. Reviewed the supplied diff and verification report; inspected only membership departure, project RLS/create functions, transaction isolation, and supported harness definitions to resolve concrete integration risks. No git commands, suite reruns, or service/database mutations performed. The removal interleaving is a source-derived finding, not a reproduced runtime failure.

## Scoped re-review: original P2

ADDRESSED. Original P2 withdrawn: the source-derived race assessment overlooked the existing shared workspace mutation lock. `middleware.Scope` (`server/internal/middleware/middleware.go:117-130`) locks the workspace row before checking membership and retains that transaction through handler execution. `middleware.InWorkspace` (lines 169-179) reuses that transaction for `h.inWS`; `LockWorkspaceForMutation` (`server/pkg/db/queries/workspace.sql:65-69`) uses `FOR UPDATE`. Bootstrap POST and member-removal DELETE therefore cannot interleave creation with departure enumeration as alleged. No production locking change is required.

The new deterministic HTTP regression pauses departure after enumeration, observes bootstrap waiting for the workspace lock, then asserts bootstrap receives 404 after removal and zero unarchived orphan backing projects remain. Its reported passing result corroborates the source evidence. The report also records the earlier unsuccessful barrier attempts accurately.

Updated Task 1 verdicts: spec compliance APPROVED; quality APPROVED. No remaining actionable findings from this review. This re-review was limited to the original P2 and its regression/evidence; no suites rerun and no production changes made.
