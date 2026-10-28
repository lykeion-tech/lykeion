# Aristotle Coresearch Redesign Implementation Plan

**Goal:** Deliver the user-approved personal Aristotle chat experience alongside existing project chats.
**Authority:** User-approved plan pasted in the conversation on 2026-10-28. This document records its requirements and executable task boundaries.
**Architecture:** Personal agents and conversations use existing Go/PostgreSQL Coresearch execution; Vue provides welcome/starter UI and L2 rail swapping.

## Global Constraints
- Preserve six pre-existing UI corrections: global New opens study; remove composer hint; connectors plug; Coresearch chat icon; both Studies icons flask.
- Each member in each lab has one personal built-in Aristotle, harness claude/model empty by default. Do not overwrite customization or adopt existing ordinary agents named Aristotle.
- Built-in remains personal and cannot be archived. Existing authorization and machine/tool isolation stay intact.
- Bootstrap is authenticated, workspace-scoped and idempotent; persist welcome without invoking CLI.
- Each conversation has a private backing project excluded from ordinary project listing. Existing project URLs remain valid.
- New chat saves only with accepted first turn, atomically and idempotently. Welcome execution snapshot freezes on first turn too.
- Snapshot harness/model/instructions for each conversation at first turn; settings updates affect only future chats. Explicit model honored only for Aristotle; ordinary project provider defaults preserved.
- Starters are ordered label/prompt pairs; empty uses Ground a research question / Design a reproducible experiment / Review evidence and findings defaults.
- No automatic studies, scheduled digests or background work.
- Heavy checks sequential, Go GOMAXPROCS=2 and -p 1. No unrelated files staged; no merge/push authorized for this feature.

### Task 1: Personal Aristotle persistence and bootstrap
Own server migration, SQL/generated queries, handler bootstrap/agents and backend tests only. No frontend changes.
- Add built-in agent identity, ordered starters, conversation classification/title metadata and configuration snapshot storage needed by Task 2.
- Make uniqueness permit one Aristotle per workspace/member while retaining ordinary live-agent name uniqueness. Keep existing ordinary Aristotle intact.
- Add POST /api/coresearch/bootstrap returning agent, welcome conversation and enough project/chat IDs to navigate. Invoke later from frontend at lab startup. Transactionally ensure agent and private welcome backing project/chat once; concurrency must be safe; no CLI/machine requirement.
- Add GET /api/coresearch/conversations to list current member's conversations with id/project_id/chat_id/title/preview/is_welcome and timestamps, omitting private backing projects from ordinary project list.
- Extend agent GET/PATCH fields for built_in_key and conversation_starters; prohibit public builtin creation/identity mutation, builtin archive or visibility change. Seed editable profile and instructions; enforce existing edit authorization.
- Extend active agent API validation for ordered label/prompt pairs (max 12, label 120 characters, prompt 16 KiB, no empty fields/NUL/invalid UTF-8; empty array means defaults).
- Use tests first: repeated/concurrent bootstrap, two members, cross-workspace, existing-name collision, no runtime, no duplicates/settings overwrite, ordinary name uniqueness, privacy and archive restrictions, starter roundtrip/validation.
- Keep schema/query changes generated using current repo tooling and document interfaces for Task 2/3 in report.

### Task 2: Atomic conversations and snapshotted execution
Own server Coresearch queue/handlers/runtime integration/SQL and tests; no frontend changes.
- Add POST /api/coresearch/conversations accepting body and client-generated request_id UUID. Atomically create private backing project/chat, capture caller's Aristotle config, accept/enqueue first turn and return conversation. Replays return same accepted result; reject request-id reuse with changed payload.
- Add POST /api/coresearch/conversations/{id}/turns for welcome/normal turns with body and request_id. Welcome snapshots on first accepted turn; all sends idempotent. Existing project chat turn APIs remain compatible.
- Validate caller ownership/workspace, available authorized own machine matching snapshotted harness before acceptance; no partial creation on failure. Preserve current sandbox/tool authorization, cancellation and stream paths by returning existing chat/project/run IDs.
- Apply frozen instructions to actual prompt, propagate configured model through enqueue/claim/daemon/provider invocation for Aristotle only; keep ordinary Coresearch provider-default behavior. Fail closed on untrusted client config.
- Concurrency tests for first turn/config snapshot and idempotent retry, unavailable machine retry, current setting changes versus existing snapshots, both Claude/Codex model argument propagation, cross-member/workspace and normal project regressions.
- Ensure welcome static content is not represented as a fake generated response; UI handles built-in intro.

### Task 3: Vue chat experience, agent settings and rail switching
Own apps/dashboard source and focused frontend tests only. Read earlier task reports for exact contracts.
- At authenticated lab startup call idempotent bootstrap with workspace-scoped query state; show recoverable failures; never reuse stale member/lab responses.
- Coresearch first visit selects welcome; subsequent entry restores last valid conversation keyed member/lab; deleted/stale entry falls back welcome. Add draft new route and conversation route without breaking existing /coresearch/:projectId/:chatId URLs.
- Use screenshot geometry: welcome chat header/portrait, centered max-width body, intro, three action cards and bottom composer; draft Chat with Aristotle portrait/description, three starter buttons, Customize starters and composer. Use existing theme and Aristotle artwork; no external mock images.
- Research prompts: Ground a research question -> Help me turn my research question into a focused, evidence-grounded question. Ask about the topic and constraints first.; Design a reproducible experiment -> Help me design a reproducible experiment. Ask about the hypothesis, available data, and constraints first.; Review evidence and findings -> Help me review evidence and findings. Ask me to share the sources or results and assess their strength and uncertainty.
- Starter click sends saved prompt through real flow. New draft is not saved until acceptance. Request UUID retained on retry; content edits get new UUID. Errors retain draft; no machine shows clear blocker and Settings link; welcome/history/settings stay usable.
- Context rail Conversations (title/latest preview) and Projects; existing projects keep functionality. + opens unsaved draft. Context rail replaces app navigation in SAME slot, not nested double rails. Port /tmp/lyk-coresearch-l2/packages/ui/src/shell/RailFabs.tsx and shell.css to Vue using existing tokens/icons, attribution conventions. Default context, persist view per member/lab; buttons labels Show navigation / Show conversations and aria-pressed. Narrow view remains usable.
- Customize starters -> /agents/:id?tab=settings, actual active AgentDetail supports query tab and starter edit label/prompt pairs, save validation, defaults preview. Portrait on builtin agent list/detail and chats. Builtin archive/visibility controls unavailable; ordinary agent behavior retained.
- Reuse existing transcript/stream/cancel plumbing with conversation send endpoint; no invented unavailable features or automatic background work. Settings affect new conversations only; starter changes reflect in unsaved new chat.
- Test lab switch state, starter save/deep link, drafts/idempotent retry, last-chat restore, rail state, no machine and existing project/global New behavior.

### Task 4: End-to-end acceptance and final integration
Own focused e2e tests and task-owned evidence; fixes assigned back to relevant implementers.
- Add browser tests for bootstrap first welcome, + unsaved draft, starter send/retry, starter settings save/reflection, reload/last chat, FAB switching, existing project access, global New, no-machine blocker, light/dark/narrow layout.
- Run typecheck, lint/format exact changed files, frontend suites, focused Playwright, affected backend packages sequentially; widen only for changes/failures. Real browser verify actual Local Lab at 1431/1430, update backend preview only after protecting lab state and applying migration.
- Whole-branch spec/security/quality review, fix real findings, retain evidence and report honest live/runtime limitations. Leave preview accessible. No merge or push.
