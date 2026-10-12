# Installed desktop reference

Captured 2026-10-12 from `/Applications/Lykeion.app`, version 1.0.4.
ASAR SHA-256: `f31b2a57b1c9a173d25663d4a4c9a7f3c683edb549ad79edeecfe624389e9b07`.
Renderer: `file:///Applications/Lykeion.app/Contents/Resources/app.asar/out/renderer/index.html`.

This is the authoritative visual reference selected by the user. The earlier web reference is outdated. No application source was extracted for these observations.

## Evidence

- `01-tasks.png`: native desktop Tasks board; `01-tasks.ax.txt`: accessibility structure.
- `02-display.png`: Display menu.
- `03-create-agent.png`: New task composer, dismissed without submitting.

The captures contain existing user workspace content. That content is observation evidence, not seed data for the rewrite. Captures remain local.

## Observed differences from the web baseline

- Desktop navigation/history controls and a tab strip above the content pane.
- Sidebar groups Laboratory and Configure; Copilot, Colleagues, Connectors, Machines and Updates destinations.
- Narrower sidebar, larger content heading, revised toolbar spacing and tall tinted board columns.
- Aristotle artwork at bottom right.
- Display menu exposes grouping, ordering, sub-task visibility and card properties.
- Escape dismissed both Display and the task composer without changing records.

## Rea support and current boundary

Rea supports passive Electron `file://` renderer observation through a loopback CDP endpoint, including ASAR-contained HTML. Its separate active Electron scenario tool launches and owns an app process. Neither approach requires copying component implementations.

Documentation inspected from https://github.com/morluto/rea/blob/main/docs/electron-observation.md at commit `d80b55e11bc47c6f999dac7dcb8382219d65739a`.

With explicit user approval, Lykeion was relaunched with temporary debugging at `127.0.0.1:19223`. The loopback binding was verified. Rea 6.4.0 successfully listed and inspected the installed renderer; script source was excluded. `rea-targets.json` and `rea-tasks.json` retain the complete returned evidence. The capture contains one frame and 14 resources, with no reported truncated or unavailable sections; policy filtering excludes script source and out-of-scope or unattributed runtime records. It is not full application coverage.

Inspection evidence ID: `ev_0e2de57d5a496a2c17518c2f58075d516eeb32acbe93b22c3352135dd6f5c548`.

After capture, Lykeion was restarted normally. The debugging listener was verified closed and the normal process had no debugging arguments; see `rea-receipt.json`. The PNGs above are native screenshots, not Rea-generated screenshots.

## Expanded interaction inventory

The machine-readable `capture-index.json` lists every captured state and its screenshot hash. There are 118 numbered states, with 116 screenshots and 116 accessibility receipts. Numbered states 04–118 cover resource pages and creation forms, settings, all four themes, search, sidebar and tabs, board filters and saved views, Table and Swimlane, connector transports/JSON, Autopilot schedules/webhooks and task wakeups, skill import methods, agent capabilities/settings, custom runtimes, task inspector Files/Notebook/Compute, run timeline, and pull-request/subtask controls, column-specific creation, calendars, field preferences, reply modes, the timezone picker, pinned tasks, parent/subtask selection, agent avatars/emoji and thinking levels.

Captures contain private workspace content and stay local. Product data and user identities are not copied into the rewrite. Existing configuration was inspected without submitting forms, creating credentials, revealing environment values, running agents, stopping runtimes, or importing files. Reversible view changes were restored to Ash / Tasks / All / Board.

### Capture format and comparison limits

Native capture bytes are JPEG with a monitor ICC profile, despite the historical `.png` filenames. The index records the actual encoding. These files are retained unchanged for evidence integrity. Convert the ICC profile to sRGB before color measurements. Reference window captures are 1920 × 959; preview captures are 1920 × 960. Cursor effects, control indicators, relative timestamps and animation frames are transient; any comparison masks must list them explicitly. A screenshot alone does not prove a control's behavior.

Most states have both image and accessibility evidence. Native menu captures 55–56 have accessibility only. The local skill file chooser was opened then cancelled; no personal file was selected or uploaded, and the OS directory listing was not added to the visual baseline.

Pixel equivalence is not established. The independent browser capture manifest currently contains 112 images at 1920 × 959 CSS pixels and DPR 1: 110 paired images covering 109 distinct reference states, plus two unpaired states. These are browser screenshots, not native Electron captures. Remaining acceptance work includes resolving measured visual differences, wider keyboard/focus coverage, populated backend-dependent histories, actual execution/streaming/error states and platform dialogs. The rewrite deliberately exposes unavailable backend operations instead of inventing records or claiming execution.
