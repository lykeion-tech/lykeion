# Fresh backend and daemon implementation plan

Version 0.2 · 12 October 2026 · proposed delivery plan; no backend implementation is claimed.

Behavioral authority: [scientific workflow v0.2](../specs/scientific-workflow.md). Transport and command design: [backend API](../specs/backend-api.md). Inventory recheck: [capability gap audit](../../research/multica-reference/API-GAP-AUDIT.md). Machine-readable coverage: [API catalogue](../contracts/api-catalog.json). Desktop observations made during API design: [reference evidence](../../research/visual-baseline/api-design-reference/README.md).

## Delivery contract

Build a fresh Go control service, Go daemon/CLI and private Python scientific service. Preserve the separation of durable intent, execution attempts, role configurations and runtimes. Do not inherit Multica's source modules, database schema or agent-managed completion semantics. Keep the existing independent TypeScript renderer and native shell, migrating their provisional service adapters feature by feature.

The installed `/Applications/Lykeion.app` remains the interaction reference throughout implementation. Keep it running beside the rebuild when checking a slice. Existing screenshot evidence is a starting point; it does not replace live inspection of the particular interaction being changed. Reference data is private reference material, never seed data for the rebuilt application. Use a separate disposable rebuild workspace for acceptance fixtures. Do not submit tasks, change settings, acknowledge decisions or run automations in the reference merely to manufacture a comparison.

Every slice follows this sequence:

1. Observe the reference surface: entry point, empty/populated states where accessible, menus, focus, keyboard navigation, pending/error behavior and return path. Record what was actually observed; inaccessible states remain unverified.
2. Write an interaction contract mapping each action to a read, local draft change or named command. Mark intentional scientific changes explicitly. A control that looks enabled must have a real supported action.
3. Expand the slice's API records into OpenAPI/JSON Schema, with authorization, revisions, idempotency, events and examples. Validate and generate TypeScript/Python clients before wiring UI code.
4. Implement migrations and Go domain transactions, then daemon/Python work where needed. Exercise failure/recovery cases before claiming a scientific gate works.
5. Wire the renderer and CLI to the same commands. Observe operation receipts; never infer backend success from a button click or process launch.
6. Compare the rebuilt desktop with the live reference at matched viewport, scale, font and data shape. Capture both; inspect overlay/difference images and keyboard/focus behavior. Mask only documented nondeterministic content, never geometry or missing controls.
7. Save a completion receipt: feature IDs, schemas, migration IDs, test results, paired captures, intentional differences, known gaps and rollback instructions. A build passing is not visual acceptance.

A scientific extension with no reference equivalent (for example structured gate objections) needs a reviewed interaction contract and product-spec acceptance scenario. It must not be labelled an observed reference interaction. Pixel parity applies to preserved surfaces; new scientific controls must respect the reference's layout, typography and interaction conventions.

## Proposed repository boundaries

```text
backend/
  go.mod
  cmd/control/                 public API and background control workers
  cmd/daemon/                  independent runtime supervisor
  cmd/lykeion/                 CLI using public commands
  internal/domain/             typed scientific and operational records
  internal/control/            identity, commands, planning, decisions, read models
  internal/realtime/           workspace/worker WebSockets, replay, fan-out, presence
  internal/collaboration/      document operation log, transformation and access
  internal/policy/             deterministic jev boundary and replay
  internal/store/              PostgreSQL transactions, migrations, outbox
  internal/artifacts/          manifests, lineage, authorized blob storage
  internal/daemon/             grants, journal, fencing, sandbox and receipts
  internal/providers/          approved agent/provider adapters
science/
  pyproject.toml
  src/lykeion_science/
    api/                       private job API and capability validation
    jobs/                      durable technical job index/checkpoints
    handlers/                  scientific handlers by closed job kind
    instruments/               recorded observations and replay adapters
contracts/
  control.openapi.yaml
  worker.openapi.yaml
  science.openapi.yaml
  realtime.protocol.json       versioned workspace/worker frames and JSON Schemas
  schemas/                     manifests, policies, verdicts and handler payloads
packages/api-client/            generated TypeScript transports
packages/contracts/             compatibility boundary during frontend migration
apps/dashboard/                 existing independent Vue/TypeScript renderer
apps/desktop/                   existing Electron host and narrow native bridge
```

These directories are a plan, not files claimed to exist. Use one Go module initially, with separate executables and enforced internal interfaces. Python owns scientific computation and adapters, not authoritative programme state. PostgreSQL transactions own acceptance, allocation and event publication. The daemon journals locally in SQLite and outlives Electron. Select and pin exact library/toolchain versions in S00. A change to this layout is an implementation decision; a change to authority requires a spec amendment.

## Ordered vertical slices

Dependencies below are completion dependencies, not suggestions to run large concurrent builds. Run heavy test suites sequentially on this machine. Each slice includes schema/client updates (including WebSocket frame schemas), migrations, real empty/error states and CLI coverage where a command is public.

| Slice | Feature coverage | Deliverable and storage | Reference/UI check | Exit evidence |
| --- | --- | --- | --- | --- |
| S00 Contract and reference freeze | All, particularly F07/F23 | Resolve jev binding; pin tools; expand common records and first-slice OpenAPI; compatibility rules; numerical policy fields without hidden defaults; baseline capture manifest; collaborative-edit protocol spike | Reconfirm installed desktop and rebuild identities; inventory every F-ID surface and intentional deviation | Validated schemas, generated-client drift check, feature traceability, agreed jev evaluation boundary; no claim that provider sampling is deterministic |
| S01 Identity, programmes and command spine | F01/F18/F22/F23/F26 | Session/device auth; invitation acceptance/revocation, member removal/leave/owner transfer and resource grants; workspace/programme/member tables; command receipts, revisions and transactional event outbox; workspace WebSocket gateway, authorized replay, cross-instance fan-out and expiring presence; capability negotiation and secure desktop bridge | Workspace switcher, Research navigation, connection/account settings, reconnect and empty states | Cross-workspace denial including counts; duplicate command has one effect; snapshot/WebSocket replay race, two-server fan-out, cursor expiry, member removal during replay and credential revocation tested |
| S02 Artifacts and independent authority | F05/F06/F07/F11/F14/F17 | CAS/upload manifests, authored/produced lifecycle, roles/configurations/rosters, independent review selection, structured decisions, policy versions/classification artifacts and deterministic evaluator | Agents/Teams/Skills/Connectors, detail drawers, Inbox split pane; new structured response contract | Reviewed bytes immutable; self-review and reviewer shopping denied; replay stable; comments/stale decisions cannot approve; classifier outage never advances |
| S03 Board, admission, requests and money | F02/F03/F04/F10/F12 | Inquiry/request typed relations, proposal/admission/funding, budget double-entry-style journal with dimensioned movements, obligations/floors, WIP, lead state and planning estimates | Task creation/details, seven-column board, filters, drag, priority reorder; reference controls mapped to roles and inquiry semantics | Competing allocations cannot overspend; forward drag pending until gate; no skip/force-Done; request dedup/consumer transfer; agent cap and exploration policy hold |
| S04 Daemon enrollment and durable execution | F08/F09/F22/F24 | Worker credential enrollment, capabilities, WebSocket wake/directive channel plus polling catch-up, queue/claim/grants/fences, daemon journal/outbox and permanent-rejection quarantine, attribution/handoff refs, cancellation/reconciliation, resource settlement and OS supervision | Machines/Runtimes: genuine capability/health reports, drain/restart, unavailable worker errors | Disconnect after terminal output then reconcile once; stale fence cannot settle; close desktop without killing authorized work; missing callback never causes blind rerun |
| S05 Python jobs, sandbox and instruments | F08/F09/F11/F20/F25 | Private science job API, job idempotency, environment resolution, isolated workers, permission mounts, controlled registry installs, recorded proxy and checkpoint handlers | Execution activity/logs, files and runtime capability surfaces; no unrestricted renderer execution | Undeclared egress denied; reserved files hidden; lease expiry saves only; reboot reconciles; request/response capture supports R1; remote data-class policy enforced |
| S06 Copilot as first complete execution path | F16/F17/F18/F22/F25 | Quick-session allowance, messages, exploratory jobs, private/shared session grants, simultaneous human/agent document operations, WebSocket synchronization/cursors, revisions/suggestions/per-author undo, cross-mode decisions and promotion | Live Copilot starter/composer/Add menu; Experiment/Cowrite switching and document edits; new budget/promotion responses | Starter sends nothing; real bounded exploratory run persists across restart; concurrent edits converge across two clients/servers; reconnect preserves unacknowledged edits; per-user undo is safe; Cowrite cannot execute without confirmation; promotion only makes Backlog proposal |
| S07 Grounding and accepted knowledge | F13/F19/F25 | Retrieval/source passages/search capture, dataset/method probes, scope/budget controls, dossier submission/gate, knowledge versions and Grounding imports | Inquiry artifacts, attachments, reviews, search and request links | Real passage resolution, contrary search and feasible route; diagnostic imports denied; pinned accepted imports keep status and restrictions; owner imports own request through Grounding too |
| S08 Hypotheses and test selection | F05/F07/F19 | Typed proposals, rivals/predictions/lineage, independent review, candidate portfolio and next-test decision, deduplicated feasibility requests | Hypothesis detail and gate feedback in established inquiry panes | Missing dossier citation rejected; at least two active discriminable rivals; blocked candidate cannot dispatch; only Validation authority commits portfolio, including initial reviewed selection |
| S09 Frozen experimentation | F08/F09/F10/F20/F25 | Contracts, frozen analysis/seeds/stopping/exclusions, pilot versus decisive trials, grants, attempts/accounting and sealed bundles | Experiment records, progress, logs, cancel/resume and failure details | Freeze precedes decisive output; builder cannot read reserved inputs; negative metric cannot be retried away; all trials counted; post-output correction is an amendment; disconnected outputs quarantined |
| S10 Validation and routing | F07/F12/F17/F20 | R0–R3 plans/receipts, independent reviewers, discrete objections/adjudication, ledger/portfolio transaction, jev routing and escalation | Gate receipt, objections, Needs me, blocked/stale overlays and history | R1 always and R2 for decisive tests; triggered R3 isolated; failed replication opens objection; no optional stopping; K reviewer crashes escalate; ledger and portfolio commit atomically |
| S11 Reporting, release and sharing | F11/F13/F21 | Rendering/audit jobs, sentence/number/figure provenance, reproducibility package, immutable release/closure, export, publication approval and errata | Report and attachment preview/download; Done outcomes, mixed counts, stopped closure | Unsupported inference blocked; claimed numbers trace; figures and R1 regenerate; wrong/stale release approval denied; retraction revokes consumers; restricted exports preserve policy |
| S12 Autopilot, wakeups and programme planning | F04/F15/F17/F18 | Approved templates, schedules/DST/misfires, signed webhooks/dedup, proposal caps, rolling plans/calibration, escalation deadlines | Autopilot templates/form/schedule preview; wakeups, activity and Inbox deadlines | Every trigger yields Backlog only; Run only excluded; duplicate event once; deadline parks without approval; selection cannot deplete reserves or overfill Validation |
| S13 Revision, incident and stop hardening | F02/F07/F09/F10/F12/F13/F17 | Full dependency propagation, selective reassessment, owner-stop transfer, policy amendment timing, incident suspension and recovery | History, dependent-card links, overlays, consequence dialogs, Inbox | Revoke stops/quarantines true descendants immediately; supersede stays stale; configuration suspended programme-wide; transfer precedes shared stop; no silent post-outcome relaxation |
| S14 Distribution, integrations and complete UI wiring | F14/F18/F22/F23/F24/F25/F26 | Remaining provider/OAuth adapters, code-reference verification, CLI parity, native file/notification/update paths, compatibility/deployment and opt-in draft migration | Every preserved desktop surface, menus, tabs, panes, shortcuts, settings, runtime/connection failure states | No generic provisional resource mutation remains; no exposed secrets; signed updater failure honest; web/desktop same authority; native egress/file actions scoped |
| S15 Cross-feature acceptance and release candidate | F01–F27 | Run product acceptance set, fault injection, schema drift/security review and complete feature receipt index; operational backup/restore/runbooks, retention/garbage-collection holds, storage-pressure controls and operator observability | Paired desktop evidence for every retained F-ID surface, justified scientific deviations; revisit old captures against live app | All 40 product scenarios plus collaboration cases C01–C12 traced and passing or explicitly unresolved; no unresolved mandatory feature called done; no mobile or deferred extras needed |

S00–S05 establish reusable authority and execution before scientific workflows. S06 proves the full UI → Go → daemon → Python → artifact → event → UI path with explicitly exploratory work. S07–S11 establish the five scientific stages. S12 adds scheduled intake only after admission and execution boundaries exist. S13 is system-wide hardening: minimal revocation, ownership and incident controls are already mandatory in the earlier slices that introduce their affected resources.

## Feature acceptance and API ownership map

Paths below name planned owners, not copied upstream modules. Public commands and reads are defined in the API document; the catalogue records their feature groups. Supporting commands may span multiple features.

| ID | Go owner / Python participation | Principal API seam | Slices and indispensable acceptance |
| --- | --- | --- | --- |
| F01 | control/identity, control/programmes | programme.*, member.*, invitation.*, workspace.transfer-owner, access.change, invite acceptance | S01; invitation lifecycle, last-owner protection, revocation and minimal one-inquiry programme |
| F02 | control/inquiries, control/requests | inquiry.*, request.* | S03/S13; exclusive ownership, shared consumption, typed transitions |
| F03 | control/board | board read, request-transition/route, priority-signal | S03/S10/S14; overlays ordered, counts truthful, no optimistic move |
| F04 | control/planning, policy | inquiry.admit/select, planning read | S03/S12; eligibility separate from funding and calibrated exploration |
| F05 | control/roles, daemon/providers | role.*, configuration.*, review reads | S02/S08; approved configurations and durable lead state and canonical handoff attribution |
| F06 | control/teams, control/work | team.update-roster, work.authorize | S02/S04; delegation confers no additional authority |
| F07 | policy, control/reviews | gate.*, review.*, criteria.*, objection.* | S02/S10/S13; replay, independence and no bypass |
| F08 | control/scheduler, daemon/supervisor | worker claims, work.authorize | S04/S05/S09; grant commits budget and eligible placement |
| F09 | daemon/journal, control/attempts | worker receipts/finish/reconcile, attempt.* | S04/S05/S09; missed wakes recover; permanent delivery rejection quarantines; no output rerun |
| F10 | control/budgets | budget.*, ledger read | S03/S09/S13; per-dimension commitments, reserves/floors |
| F11 | artifacts, daemon/capture; Python manifests | artifact.*, uploads/content | S02/S05/S11/S15; immutable snapshots, pinned retention and reachable blobs |
| F12 | artifacts/lineage, control/incidents | supersede/revoke, incident.*, dependencies | S03/S10/S13; actual dependency edges and timely invalidation |
| F13 | control/knowledge; retrieval handlers | knowledge.*, grounding.import, two search APIs | S07/S11/S13; no diagnostic/blinded evidence laundering |
| F14 | control/configuration, providers | skill.*, connector.*, configuration.* | S02/S07/S14; human policy/access change gate |
| F15 | control/triggers | autopilot.*, wakeup.*, signed webhook | S12; never direct execution or frozen-trial extension |
| F16 | control/quick, control/documents; quick.experiment | quick.*, document.* | S06; shared/private work, concurrent edits, per-user undo, separate allowance and promotion |
| F17 | control/decisions, control/notifications | decision.respond, discussion.comment, notification.mark | S02/S10/S12/S13; structured exact-version authority only |
| F18 | control/events, control/views | workspace/worker WebSockets, replay, presence, activity, view.save, tab.pin | S01/S06/S12/S14; cross-instance authorized fan-out, canonical history and cursor recovery |
| F19 | control/science; retrieval/probe/hypothesis handlers | grounding.*, hypotheses.submit, portfolio.commit | S07/S08; dossier-only rivals and feasible next test |
| F20 | control/science; experiment/reproduction/validity handlers | experiment.*, validation.* | S05/S09/S10; evidence bundle, independent audit and ledger |
| F21 | control/releases; render/audit/export handlers | report.*, closure.release, release.*, artifact.export | S11; faithful immutable rendering, mixed/nonpositive completion |
| F22 | CLI, daemon/IPC, existing desktop bridge | same public commands, narrow native IPC | S01/S04/S06/S14; parity and independent daemon lifetime |
| F23 | all service contract boundaries | capabilities, schema versions, protocol negotiation | S00/S01/S14/S15; incompatible clients cannot bypass rules |
| F24 | control/runtimes, daemon/capabilities | runtime.*, machines/runtime reads | S04/S14; actual health and safe lifecycle controls |
| F25 | providers; scientific adapters and instruments | connector.*, declared jobs/proxy, code.* | S05/S07/S09/S14; recorded retrieval, pinned tools and restricted egress |
| F26 | control/identity, secret store, native updater | credentials, secure auth/OAuth flow, updates policy | S01/S14; secure failure/revocation and signed distribution |
| F27 | No scientific-core owner | No mobile API; optional extras disabled | S15; exclude mobile; channels/commercial extras remain deferred |

## Schema and migration checkpoints

Each slice checks in OpenAPI, WebSocket protocol/frame JSON Schemas and generated clients together. Document handshake/close/reconnect state machines and per-frame limits outside OpenAPI; HTTP schemas alone do not define a WebSocket protocol. Required detail before its code begins: all referenced payload types, required/optional semantics, enum closure, limits, per-field data classifications, authentication, permitted roles, expected revisions, errors, idempotency scope and retention, event types and result references. Publish positive and forbidden examples. A command named in the design catalogue is not yet a validated wire schema.

Create fresh migrations by slice: identity/programmes/memberships/invitations/grants/authorization epochs/receipts/outbox; artifacts/roles/policies/reviews/decisions; inquiries/requests/budget journal/planning; workers/grants/attempts/settlements; quick sessions/document operations/snapshots/suggestions; knowledge/scientific contracts/ledgers/releases; triggers and remaining projections. Database constraints enforce unique effects, immutable version links, ownership, valid references and budget settlement. JSON artifact schemas do not replace relational transaction invariants.

No migration imports the original app's database or credentials. Existing rebuild drafts stay local until the user previews and confirms an import. Import only unaccepted proposals/artifacts and preserve origin labels; do not invent old gate decisions or execution history. Schema changes during rollout use expand/contract migration and compatible readers. Downgrades that cannot understand newer sealed artifacts are refused, not repaired by deleting records.

## Failure-focused verification

Tests below are required implementation work, not tests reported as run for this design document.

- Contract: generated-client/schema drift, closed command enums, required version references, malformed manifests, incompatible daemon/science versions, unauthorized nested refs and secret-free events.
- Transactions: simultaneous allocation/claim/decision/transition; competing WIP slots; duplicate webhook and completion; outbox commit then process crash; two realtime instances, lost wake notifications and reconnect replay; stale approval versus amendment. Assert one durable effect and preserved receipts.
- Execution: missed/duplicate worker WebSocket wakes, permanent terminal-delivery rejection, daemon crash before start, after metric, during upload and after server commit before acknowledgement; offline expiry with fixed grace; clock rollback/reboot; lost cancellation; repeated failed reviewer attempts; produced verdict prevents re-roll.
- Scientific: pinned dossier citations; hidden reserved data; original/amended plan timing; deterministic gate replay from sealed classifications; R1/R2/R3 distinctions; underpowered/negative/deviated/contested outcomes; inadmissible evidence never supports a finding.
- Information flow: diagnostic search counts/snippets cannot leak blinded outcomes; mixed data-class lineage; revoked root checked while propagation is pending; remote placement and export blocked when policy forbids them.
- Product: multiple workspace members receive authorized updates, concurrent authors converge and retain per-user undo; starter fills but does not send; keyboard/menus/tabs/panes preserved; structured decision distinct from comments; undo respects intervening edits; Autopilot creates Backlog; all seven columns and mixed Done represented.
- Operations: database/artifact backup restoration preserves hash roots, event cursors and budget journal; local outbox recovery; credential rotation; environment/image availability; zero seeded product records in a clean install.

Use unit tests for pure rules, real PostgreSQL integration tests for concurrency, daemon subprocess/fault-injection tests for recovery, Python handler tests with pinned fixtures for analysis, and UI/native checks for interaction parity. Use test-only synthetic scientific fixtures, visibly isolated from user work. Run the relevant package tests and then one full slice acceptance path sequentially; broaden only for an actual changed boundary or unresolved failure. Record exact commands/tool versions when the test harness exists rather than inventing runnable commands now.

## Design-freeze inputs and intentional deviations

S00 must settle the provider-auth/local-install bootstrap flow, deployment topology, jev integration contract, artifact retention guarantees and signed distribution strategy. Programme policy exposes review K, lease/grace durations, pause/deadline actions, budget/floor/reserve rules, exploration share, WIP/escalation limits and diversity thresholds. Freeze WebSocket frame/queue/rate limits, replay retention, presence expiry and the document transformation protocol alongside these values. No arbitrary defaults silently become scientific policy. Restricted data requires connectivity as agreed; public-data leases remain bounded. Public release requires an explicit irreversible-action approval.

The live reference contains controls whose behavior must change: Autopilot Run only is excluded; agent assignees become lead roles; direct stage edits become gate requests; generic Done becomes audited release/closure; quick-work promotion requires confirmation; Inbox comments never authorize. Keep their surrounding visual grammar while making these semantic differences visible. The product specification prevails over existing app behavior on these points.

The running reference cannot prove future backend semantics. Completion requires both interaction evidence and transaction/scientific tests. No slice may be called complete because screenshots match while actions are local mocks, or because APIs pass while the desktop layout and keyboard behavior regress.

## Acceptance scenario ownership

Scenario numbers refer to the authoritative product specification, not new tests invented by the API catalogue. The owning slice must supply executable evidence; S15 reruns their cross-feature paths. These assignments establish coverage, not a passing status.

| Product scenario IDs | Owning slices | Required path |
| --- | --- | --- |
| 1, 2, 3 | S07 | Grounding scope, contrary search, interrupted retrieval/gate |
| 4, 5 | S08 | Admitted evidence and protected portfolio |
| 6, 7, 8 | S05/S09 | Reserved-data isolation, freeze, trial/retry accounting |
| 9, 10, 11, 12 | S10 | Negative evidence, R2/R3, objections and legitimate routing |
| 13, 14, 15, 16, 17, 18, 19, 20 | S11 | Faithful report, audited release, nonpositive outcomes and publication |
| 21, 22, 24, 25 | S03 | Board gates, request result, admission and WIP |
| 23 | S11 | Mixed disposition counts and question-level explanation |
| 26, 27, 28 | S02/S10/S13 | Authority, failed reviewer replacement, outcome-aware amendment |
| 29, 30, 31 | S04/S05/S09 | Lease expiry, connectivity restrictions and safe resume |
| 32, 33, 34 | S07/S11/S13 | Inherited restrictions, diagnostic blinding and retraction |
| 35 | S12 | Backlog-only Autopilot intake |
| 36, 37 | S06 | Cowrite confirmation/undo and exploratory promotion |
| 38, 39 | S02/S13 | Superseded decision, deadline action and configuration suspension |
| 40 | S02/S05 | Sealed snapshot enforcement across every access path |


## Collaboration acceptance supplement

These are new Lykeion implementation requirements, not claims that Multica supports collaborative document editing. They extend F01/F05/F08/F09/F11/F16/F18/F22/F23/F26 without renumbering the 40 scientific scenarios.

| Case | Owner | Required test and observed UI result |
| --- | --- | --- |
| C01 | S01 | Invite acceptance is recipient-bound, single-use and idempotent; expired/revoked invitations fail without leaking workspace data; leaving/removing the last owner requires transfer |
| C02 | S01/S14 | Two members in one workspace on different Go instances see the same permitted committed event; another workspace/member lacking programme access sees neither content nor counts |
| C03 | S01 | Remove a member during subscription/replay: further delivery and edits stop, queued old frames are reauthorized, client clears projections, historical attribution survives |
| C04 | S01/S14 | Disconnect after commit before acknowledgement, reconnect on another instance, replay once in the UI; expired cursor yields fresh snapshot, not a silent gap |
| C05 | S01/S06 | Multiple browser/desktop sessions expose only authorized presence; per-session expiry removes stale cursors; private Copilot and blinded scientific work do not leak activity |
| C06 | S06 | Two humans and a Cowrite agent concurrently insert/delete/format; server and all clients converge under the pinned edit protocol without lost edits |
| C07 | S06 | Reconnect rebases pending edits using their original IDs; duplicate sends commit once; compacted base preserves local draft and requests merge; revoked access rejects offline writes |
| C08 | S06 | Per-author undo preserves another person's intervening edits; accepting a stale suggestion detects conflict; submission seals an exact revision while successor draft remains editable |
| C09 | S04 | Duplicate/missed WebSocket wakes and socket loss do not duplicate claims or authorize work; polling catches missed work; lease heartbeat/cancellation remains authoritative |
| C10 | S01/S15 | Slow client queues remain bounded; durable events force resync rather than silent loss; presence may coalesce; outbox wake loss or server restart recovers through durable catch-up |
| C11 | S02/S04/S15 | Human→lead→specialist→runtime chain retains initiating/authorizing/executing identities and canonical handoff refs; rejected terminal delivery remains recoverable/quarantined |
| C12 | S11/S15 | Cleanup cannot delete artifacts pinned by release, negative result, active review or undelivered receipt; full disk stops new work; approved restricted-data erasure records unavailability and consequences |

S00's collaboration spike must demonstrate deterministic operation transformation, Unicode semantics, supported block edits, per-author undo and cross-version handling using independently selected code. Its exit includes runnable convergence tests; a conflict dialog for every simultaneous keystroke does not satisfy C06. Reject automatic merging of scientific contracts or produced evidence: only ordinary drafts participate.

Add paired-member acceptance sessions to the desktop-reference loop: inspect existing member settings, collaboration affordances and notifications in the running reference without inviting people or modifying their permissions. Build and test new live-cursor/coediting affordances in disposable rebuild accounts, labelled as Lykeion extensions where the reference has no equivalent. Previous captures do not validate newly added interactions.

The inventory audit preserves all essential capabilities and records remaining design-freeze choices explicitly. It does not select Multica's router, WebSocket library, cache framework, ORM, Redis deployment or source layouts. SQLite stays a proposed per-daemon journal, not a workspace collaboration database. The server remains authoritative even when a client has an optimistic text edit or an active socket.
