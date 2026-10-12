# API and implementation coverage recheck

12 October 2026 · API/plan v0.2 · design audit, not implementation verification.

Inputs: the complete [reference inventory](INVENTORY.md), its pinned source/document links and [Graphify receipt](GRAPHIFY.md), compared with [API v0.2](../../docs/specs/backend-api.md) and [delivery plan v0.2](../../docs/plans/backend-implementation.md). All 23 capability rows below were checked. Original inventory observations remain historical; their provisional scientific meanings are superseded by the agreed product specification. This audit does not rerun Graphify or assert that a temporary checkout still exists.

The pinned upstream raw-document fetch was unavailable during this recheck. The earlier live documentation check confirmed workspace WebSocket cache updates and worker WebSocket wakes with polling fallback; this audit uses the retained pinned inventory for the full capability comparison. No upstream source bodies were copied or used to generate code. WebSocket semantics are independently specified using [RFC 6455](https://www.rfc-editor.org/rfc/rfc6455). Simultaneous coediting, cursor presence, scientific leases and SQLite are Lykeion design choices, not inferred Multica features.

| Reference capability | Prior API/plan coverage | Disposition and concrete acceptance |
| --- | --- | --- |
| Workspaces, projects and access | Partial: invite/change-access; lifecycle/private/shared scope incomplete | Added recipient-bound accept/revoke, leave/remove/owner transfer, resource grants and authorization epochs; F01, S01, C01–C03 |
| Issue / board work object | Covered | Preserve inquiry/request/attempt separation; F02/F03, S03 |
| Status taxonomy | Covered | Keep columns, overlays and scientific disposition separate; F03, S03/S10 |
| Sub-issue stage barriers | Deliberately replaced | Only versioned evidence gates satisfy dependencies; cancelled child is not success; F07, S02/S10 |
| Agents and provider invocation | Covered | Pin role/configuration/runtime and provider capability; F05/F25, S02/S04/S14 |
| Squads / delegation | Covered, attribution underspecified | Roster not automatic execution; added explicit delegation/authorization chain; F06, S02/S04, C11 |
| Queue, claim and start | Covered | Atomic reserve/claim/start distinct; add low-latency wake with catch-up, no socket-created grants; F08, S04, C09 |
| Ownership and originator attribution | Partial: actor/config recorded, originator chain implicit | Added server-produced initiator/authorizer/executor/delegation provenance through command/grant/artifact; F05/F18, C11 |
| Completion and duplicate delivery | Covered | Same effect keys across receipt replay; process completion distinct from scientific acceptance; F09, S04/S09 |
| Daemon terminal outbox | Partial: replay covered, permanent rejection lifecycle vague | Added bounded delivery backoff, quarantine, incident and authorized recovery; F09, S04, C11 |
| Leases, time and stale recovery | Covered | Retain fences, monotonic expiry, clock/reboot reconciliation and no scientific result re-roll; F09, S04/S05 |
| Wakeups / recurring automation | Covered | Versioned triggers propose Backlog only; no accidental trial extensions; F15, S12 |
| Handoff evidence | Partial: refs available but handoff contract implicit | Added sealed canonical event/artifact manifest plus derived bounded summary and renderer provenance; F05/F11/F18, S02/S04, C11 |
| Isolation and durable work | Covered | Pinned implementation/environment and sandbox; worktree is not security boundary; F11/F20, S05/S09 |
| Review and PR target identity | Covered with scientific replacement | Review immutable input/version target; PR is supporting artifact, never gate substitute; F07/F20/F25 |
| Cancellation and stop | Covered | Request intent versus actual stopped execution, retained results and transfer-before-stop; F09/F12, S04/S13 |
| Run quotas and usage | Covered for spend/compute/time; storage operational limit implicit | Preserve budget rules; explicitly meter storage and stop new work under pressure, never silently evict evidence; F10/F11, S03/S15, C12 |
| Realtime and timeline | Gap: realtime capability kept but WebSocket/relay omitted | Replaced primary SSE with workspace WebSocket plus HTTP replay; multi-instance fan-out, slow consumers, presence and worker wake channel specified; F18/F22/F23, C02–C05/C09–C10 |
| Shared client logic and views | Covered | Same independent renderer/domain client and command authority for web/desktop; F22, S14 |
| Desktop shell | Covered | Native bridge and independent daemon remain distinct; F22/F26, S04/S14 |
| API compatibility | Covered; WebSocket protocol absent | Added versioned frame schemas, handshake/reconnect state machine and bounded queues; F23, S00/S01/S14 |
| Skills, tools and integrations | Covered | Approved capability expansion, pinned tools, recorded retrieval and secret isolation; F14/F25, S02/S05/S14 |
| Mobile, chat channels and commercial policy | Intentionally excluded/deferred | No mobile; optional external channels/billing do not block scientific core; F27, S15 |

Additional reliability item from the inventory's prose: **independent retention** was not concrete enough. API section 14 now specifies reference/hold-aware cleanup, negative/failed result retention, restricted-data erasure handling and storage backpressure. Health/metrics and restore tests were also made explicit as operational requirements; no particular monitoring library is inherited from the reference dependency list.

Workspace membership is the outer authorization boundary; programme/resource permissions and scientific blinding further restrict access. The old inventory's shorthand “programme/project is the permission boundary” must not be interpreted as replacing workspace membership or exposing every programme to every member.

The plan now assigns all retained capabilities, the original 40 scientific scenarios and C01–C12 collaboration/reliability cases. This is coverage evidence only. Remaining implementation choices include the independent document-operation library/protocol, auth provider flows, resource limits and deployment configuration, each with an explicit design-freeze gate. No claim of live UI parity or passing backend tests follows from this audit.
