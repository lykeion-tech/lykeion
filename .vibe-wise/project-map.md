# Project Map

## Purpose
Lykeion: a research platform where AI agents carry out scientific work on a board and people steer it (CLAUDE.md).

## Requirements
- Board of five stage columns stays: Grounding, Hypotheses, Experimentation, Validation, Reporting (user, 2026-10-16).
- The scientific design of each stage is being redesigned from scratch by the learner (user, 2026-10-16).
- The built Grounding and Hypotheses code will be replaced once the new design is settled (user, 2026-10-17).
- Goal: Lykeion as a day-to-day L3 ("AI-led, human-assisted", Tie et al. arXiv 2605.23204) research tool scientists use without babysitting the AI; one pipeline per scientific field on the board, each designed and measured against that field's benchmarks (user, 2026-10-17).
- Teams of agents run each stage; a UI presents the outputs agents generate; the board as a whole reaches L3 (user, 2026-10-17).
- Goal framing: Lykeion as an evolvable multi-agent framework that reconciles generality with domain specificity (user, 2026-10-17; wording taken from PantheonOS's tagline, github.com/aristoteleo/PantheonOS, BSD-2).
- From 2026-10-17: ground stage design answers in Tie et al. arXiv 2605.23204 (~/Desktop/2605.23204v1.pdf), citing sections (user).
- Existing constraints from CLAUDE.md: a card moves only when its gate passes (server checks, plus Jev where used); evidence only via the sources server; every claim cites a recorded source; no routine step waits on a human.

## Components
Verified existing code (as built before the redesign):
- Go server `server/`, packages incl. `board`, `ground` (Grounding), `hyp` (Hypotheses), `round`, `records`, `predicate`, `jev`, `sources`, `autoresearch`.
- Daemon `server/internal/daemon`, CLI `server/internal/cli` (`lyk`).
- Vue dashboard `apps/dashboard/`, contracts `packages/contracts/`.
- Current stage design: `docs/superpowers/specs/2026-10-13-board-and-stages-design.md` §6–10 (Grounding and Hypotheses built; Experimentation, Validation, Reporting planned only).

## Main Flow
Card → Grounding → [gate] → Hypotheses → [gate] → Experimentation → [gate] → Validation → [gate] → Reporting
What each stage does, what its gate checks, and how route-backs work in the new design: ? (learner to design)

### Chosen: general core vs field pack, and the gate mechanism (confirmed 2026-10-17; not implemented)
Learner decisions:
- Shared core, same for every field: the five columns; how a gate decides.
- Field pack (Markdown templates, PantheonOS-style): agent roles, skills, checks; one benchmark per field.
- Every field check fails the gate in code. Meaning-level checks: Jev judges, code checks the verdict. Uncertain Jev answers go to a reviewer agent. If Jev is unavailable, another judge steps in.
AI additions, approved by the learner:
- Save every Jev choice whose answer is reused downstream (e.g. passage supports/contradicts/says nothing; passage origin; dataset unit of analysis; falsifier relevance).
- Choice record: question id, code-extracted candidates, answer, probabilities, pinned model version, input hash, judge identity (Jev / reviewer / fallback).
- Code checks: record exists for each required question; answer is the passing value; probability in the yes band or a reviewer verdict record covers the middle band; judge is not the author.
- Bands start at >= 0.70 yes, < 0.35 no, middle to reviewer; pinned per field pack.
- Reviewer and fallback judge: different model family from the author, system-assigned, verdict recorded.
### Chosen: field packs and their code (confirmed 2026-10-17; not implemented)
Learner decisions: a field pack ships its check code; it runs in a sandbox on the server; any workspace member approves a pack; packs do not evolve.
AI additions, approved: sandbox sees only the checked card's records, read-only; no network, no file system beyond input, time and memory limits, returns pass/fail + reasons only; approval is for a pack version + hash and updates re-ask; approvals recorded (who, version, when) and shown to members; a crashing check counts as fail.
### Chosen: the card and route-backs (confirmed 2026-10-17; not implemented)
Learner decisions: a card starts as a question, a dataset or a claim, set per field pack; an undermining result sends the same card back to an earlier column; gate code picks the target; hitting the cap escalates to a human; the board may revise the starting point.
AI additions, approved (learner delegated the drift-guard choices): each pack check declares failure type + target column; route-back keeps earlier work as a version, later records marked stale; cap per card across route-backs and revisions, number set by pack, default undecided; escalation moves the card to Blocked with history and options (raise cap, revise by hand, close); a revision cites the undermining result + typed reason, checked by code; original never overwritten, revisions versioned and linked; Jev drift check (narrows / reframes / changes; changes escalates; uncertain to reviewer); researcher notified, never waited on, may revert; claim cards: the claim is never revised, an undermining result is the answer, a revised claim is a new linked card; dataset cards: dataset fixed, question revisable.

- Reviewer's verdict is final (learner). Skills reach stage agents as in Multica: attached to agents, delivered to the CLI at task start (Settled on Multica's shape: multica-reference OFFICIAL_DOCS_ATLAS O012). Cap default: 3 route-backs per card, revisions count, 4th -> Blocked, pack may override (AI's call, delegated by learner).
- "Agents' performance evolves" (learner) conflicts with "packs don't evolve" (packs hold roles and skills): open.
### Grounding core contract (AI proposal; learner added the no-gap rule and moved on without corrections)
Pinned starting point; sources only via sources server; claim ledger (statement, quoted passage, Jev supports, Jev origin, stance); gaps citing claims; contrary search record; data inventory (hash, structure, Jev unit of analysis) for dataset cards. Gate: passages resolve + Jev records say supports, contrary search ran, gaps cite claims, inventory present. Pack adds sources list, field data checks, what counts as primary. No gap found -> card goes to Reporting as "already known" (learner).
Revision against paper §3.2 (AI proposal, approved by learner): ledger = persistent card evidence state cited by later stages, versioned; conflict records (Jev-chosen disagreeing claim pairs, acknowledged in gaps); light evidence cards (claim/method/dataset/result/limitation); explicit prior-art boundary record. Named deviation: dataset inventory stays in Grounding (AI's original proposal; learner approved without a separate answer) although the paper places data processing in Stage III.
### Hypotheses core contract, revised against paper §3.3/§6.1 (AI proposal, approved by learner)
>= 2 rivals incl. null/artefact from ledger only (missing premise -> Grounding); structured falsifiers; one discriminating test, non-overlapping predictions; frozen plan with steps (pre-registration, beyond the paper); feasibility + cost; novelty vs prior-art boundary; risk/safety record with pack content; generators with different inputs/configs + Jev restatement check; candidate pool with parked rivals kept on the card; selection by discriminability/feasibility, no LLM-judge scores or Elo; conflict records and undermining results are required inputs, >= 1 rival explains the anomaly; route-back resumes from pool + anomaly. Already stated -> Reporting as known. No feasible test -> Blocked (learner). Dataset cards: exploration half for Hypotheses, held-out half touched only by Experimentation (the fix AI described; adopted by learner's blanket approval, paper silent).
### Experimentation core contract, against paper §3.4 (AI proposal, approved by learner)
Substrate: code-native + tool-orchestrated via daemon CLIs; no lab-robotic; human gating only for exceptions. Outputs: run record per plan step (machine, CLI, tool, versions, lockfile/container digest, command + params, inputs/outputs by hash, seeds, exit status, logs); measurement keyed to plan measurement id, computed by code, no interpretation; deviation log (analysis-changing, chosen by Jev -> Hypotheses re-freeze); typed failures (infrastructure retries in place, scientific routes back); held-out data read only by plan steps. Gate: run record per step, hashes resolve, measurement present, deviations typed, environments recorded, held-out access checked. Reruns and baselines left to Validation (§3.5). Pack adds tools/environments, field execution checks, measurement definitions. Tests needing human participants or a wet lab -> Blocked (learner). AI's calls under blanket approval: infra retries don't count toward the card cap, 2 per step, then Blocked; a human-run result enters through the sources server with uploader identity as a "human-run" run record.
### Validation core contract, against paper §3.5 (AI proposal, approved by learner)
Rerun layer (clean rerun reproduces within pack tolerance; baseline/negative control run; metric frozen pre-result); recompute layer (stats recomputed, pack fatal checks); per-rival status by code (supported/refuted/inconclusive/invalid/caveated), no model raises a status; blind objector (different family, shuffled labels, typed objections tied to record ids -> code recheck or Jev, can only lower); keep -> Reporting, revise -> typed route-back, refuted -> Reporting as negative result, invalid -> route back; only Validation writes statuses. Pack adds tolerances, fatal checks, objection types, valid controls. Expert/time-grounded validation = reopen on new evidence (learner). AI's calls under blanket approval: Autoresearch periodically re-searches sources for Done cards (contradicting papers, retractions, failed replications); Jev chooses whether new evidence contradicts a claim the report rests on; if so the card reopens into Grounding with that evidence; each reopen gets a fresh cap; the prior report is kept as a version marked under review; researcher notified.
### Reporting core contract, against paper §3.6 (AI proposal, approved by learner)
Artifact-linked package, not text alone: claim sentences linked to ledger/status ids; numbers resolve to run outputs (code rejects unresolved); figures/tables record source object hashes and rebuild; citations resolve to ledger with Jev support; Jev per claim sentence: asserted status must match Validation's, and no generalisation beyond the unit of analysis; report kinds (finding, negative result, already known); reviewer files typed objections only, cannot change statuses; provenance bundle (code, env, data hashes, run records, ledger, plan, statuses, card history); limitations required from caveats, deviations, upheld objections; reports versioned for reopen. Pack adds reporting guideline (MINSEQE, CONSORT, APA JARS), metadata, figure conventions. Researcher receives a mix (learner). AI's call under blanket approval: a report page in the dashboard is primary (claims, statuses, links into the ledger and runs, card history); the provenance bundle is downloadable; a LaTeX manuscript draft is generated on request and opens in Coresearch Cowrite.
Still open: whether separation of duties is a core rule; what evolves (Reef lead); field-pack skill delivery to stage agents.

## Data and Trust Boundaries
PostgreSQL 17 with RLS per workspace; Redis 7; sources server is the only evidence path; TYPESAFE_API_KEY server-only (CLAUDE.md). Not re-verified for the redesign.

## Build and Deployment
Not inspected for this task.

## Unknowns
- Which field goes first; what is shared across field pipelines vs per-field; how unbenchmarked stages (Grounding, Hypotheses, Validation-as-critique, Reporting) get measured.
- New stage responsibilities, gate contents, record kinds, route-back rules: all open.
- Relevant prior research: `docs/superpowers/planning/2026-10-16-l3-field-pipelines-research.md`, `2026-10-16-benchmark-surveys/`.
