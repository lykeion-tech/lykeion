# Task 4 review

Reviewed the task brief, report and supplied `3c168687..4ea6812` review diff once. Read-only review of the implementation; this review receipt is the only file written. No Git commands, tests, production edits or additional agents.

## Spec review

No blocking findings (P0–P2).

- The review range contains seven test/config files and the small parent-owned plan amendment; it contains no production code edits. The amendment explicitly records the latest user ruling: Coresearch is independent of product Projects, its conversation rail omits Projects, the main navigation retains `/projects`, and legacy direct Coresearch project routes remain compatible.
- Browser assertions cover the fixed three welcome research cards after saving a single custom starter, while separately requiring that custom starter in the unsaved draft. This catches the previously reported welcome regression rather than merely accepting fixture data.
- Narrow-layout assertions measure zero rectangle intersection between each of the two FABs and the textarea for both single-line and four-line drafts, in both themes. They also check focus, editable value, hit testing and document horizontal overflow.
- The rail test asserts absence of Projects/Manage projects links inside Conversations, the separate main-navigation Projects destination, exclusive rails, persisted selected rail and legacy direct project access. It does not claim to test the actual Projects backend.
- Existing global New, composer-hint absence, focus/rename, file/compute, notebook and Markdown assertions are retained. Bootstrap handling is inserted before domain-write capture in legacy fixtures, so the new initialization request does not contaminate the arrays whose original assertions remain intact. No legacy assertion weakening is visible in the supplied diff.

## Quality review

No blocking findings (P0–P2).

The tests exercise real Vue/router/API-client behavior with HTTP fixtures: actual settings save and confirmation, rendered custom/default starters, accepted identifiers and local storage, current transcript/run cancellation, restoration, failed transport retries with stable UUIDs and a changed UUID after payload editing. These are meaningful frontend acceptance checks. The lost-response case models server acceptance before a dropped response and verifies client replay of the identical request and receipt; its one-receipt assertion is a fixture-model assertion, not independent proof of production server idempotency. The report states this boundary accurately and assigns backend idempotency to separate backend verification.

Scope limits are appropriate and explicit: storage-pointer tests do not prove an authenticated second-member session or server isolation; screenshots depict fixture UI; root owns actual Local Lab/CUA acceptance and final sequential suites. No real online daemon or paid provider acceptance is claimed. The provided test report records passing targeted/full browser, lint and formatting checks, but this reviewer did not rerun or independently confirm those executions.

Nonblocking coverage limits: the `/coresearch/projects` compatibility assertion checks the URL, whereas the subsequent direct project-detail assertion checks a rendered sidebar; and this browser test asserts no Projects links rather than directly recording absence of a project-list HTTP request. These are bounded by the separate focused rail regression described in the plan/report and are not grounds to block this tests-only change.

Verdict: acceptable within the stated frontend fixture acceptance boundary, subject to root's separate backend/live/final verification. No changes requested.
