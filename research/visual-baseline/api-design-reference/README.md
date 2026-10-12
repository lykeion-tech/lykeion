# Desktop reference observations for API design

Captured on 12 October 2026 from the running installed `/Applications/Lykeion.app` through native accessibility and screenshots. This is the desktop interaction reference, not the older localhost web dashboard. The manifest records byte hashes and detected image encodings. Native capture filenames use `.png`; trust the detected encoding when processing them.

These captures support the API/implementation plan. They do not establish parity for every surface, backend correctness, or successful scientific work. Prior broader reference evidence remains in the [coverage inventory](../dashboard-coverage.md). During each implementation slice, inspect the live app again alongside the rebuild.

| Evidence | Observed behavior | API consequence |
| --- | --- | --- |
| [copilot](copilot.png) / [accessibility](copilot.ax.txt) | Selected Literature Review & Gap Analyst; starter actions and empty composer | Selected role and local draft precede durable QuickSession creation |
| [copilot-starter](copilot-starter.png) / [accessibility](copilot-starter.ax.txt) | Design a reproducible experiment fills composer and enables Send; does not submit | Starter has no mutation API call; `quick.start` only on submission |
| [copilot-add](copilot-add.png) / [accessibility](copilot-add.ax.txt) | Add offers Image or files, Research context, Skills, Connectors | Scoped uploads and approved version references; selecting a tool does not grant access |
| [autopilot](autopilot.png) / [accessibility](autopilot.ax.txt) | Empty template surface includes literature watch, hypothesis review, experiment monitor, weekly report, reproducibility audit, manuscript evidence check, scratch | Template choice creates a draft, not scheduled execution |
| [autopilot-create](autopilot-create.png) / [accessibility](autopilot-create.ax.txt) | Form exposes runbook/title, agent/team, Create task/Run only, project/directory, subscribers, schedule/webhook, time/interval/weekdays/timezone/next runs | Preserve form interaction conventions; intentionally replace output with Backlog proposal, role assignment and human-approved activation |
| [inbox](inbox.png) / [accessibility](inbox.ax.txt) | Notification list and separate detail pane with selection placeholder | Notification reads separate from structured DecisionItem authority |
| [inbox-filter](inbox-filter.png) / [accessibility](inbox-filter.ax.txt) | Filter has Unread only, From, Status, Priority | Typed authorized filters; no decision-state change from filtering |

The filled starter was cleared without sending. The Autopilot form was cancelled without creating an automation. Inbox notifications were not opened or acknowledged. Menus were dismissed and the reference was returned to Copilot without submitting a task. No reference record was copied into product fixtures.

Structured scientific decisions, separate quick-work allowance, promotion confirmation and claim-ledger routing are agreed Lykeion extensions. These particular captures do not demonstrate those behaviors; validate them against the product specification and new interaction contracts. Capture files may contain private reference-app context; do not publish them as public sample data.
