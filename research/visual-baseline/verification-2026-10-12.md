# Desktop inspection and rewrite verification — 2026-10-12

Reference: installed `/Applications/Lykeion.app`. The outdated web application is not the acceptance reference. Rea's passive capture receipt and native interaction evidence are stored separately; native screenshots must not be described as Rea-generated.

## Evidence

`desktop/capture-index.json` now indexes 118 numbered states: 116 screenshots and 116 accessibility receipts. Image hashes are calculated from the retained original bytes, and encoding is identified by file signature rather than the historical `.png` extension. States 55–56 have accessibility evidence but no screenshot; 02–03 have screenshots without an accessibility receipt.

Latest states include working-agent filtering, column creation, calendars, field preferences, reply/timezone choices, sidebar pins, parent/subtask selection, agent avatar/emoji and thinking menus. Reference inspection did not submit task creation, schedule activation, imports, permission changes, tokens or agent execution. Reversible reference views and pins were restored; the reference returned to Tasks / All / Board / Ash after capture; the final read observed Agents after a subsequent window change.

`rewrite-browser/manifest.json` records run `2026-10-12T18:13:52.775Z`: 112 browser screenshots at 1920 × 959 CSS pixels, DPR 1. Of these, 110 entries pair to 109 distinct reference states; Teams also has a separate UI-created draft-list image. Two images have no reference counterpart. All nine missing pairs are explicit: machine/runtime states 45 and 76–80, native menus 55–56, and run-cost detail 98. The suite creates local drafts through ordinary UI interactions in isolated contexts; it does not seed storage or copy reference records. Per-image hashes and context differences are in the manifest.

These screenshots are browser-renderer evidence. Earlier native Electron preview screenshots remain separately in `rewrite-desktop/`; the browser run does not establish native-window parity.

## Verification receipts from this work session

- `npm test`: 13 unit tests passed with one worker.
- Functional browser checks: all 16 distinct cases have passed in this session: 10 frontend cases and a final 6-case controls run (11.0 seconds). The latter includes new emoji keyword/skin-variant and custom settings menu tests. They were run sequentially, not as one all-16 invocation.
- `npx playwright test --config playwright.visual.config.ts`: 10 capture scenarios passed in 26.1 seconds with one worker, producing the expanded manifest above.
- `npm run build`: final renderer typecheck, Vite production build and desktop TypeScript compilation passed. Main JS is approximately 593 kB; Vite retains its advisory 500 kB chunk warning. Emoji data is a separate lazy-loaded chunk.

Behavior checks cover honest first-install state, draft/comment persistence, rejected local scientific stage transitions, independent tab history, invalid connection rejection, Table edits/columns and safe CSV export, Swimlane grouping/collapse, task auxiliary drafts and relationships, context-menu/pin behavior, configuration forms and calendar interaction. Capture checks verify reachable states and screenshots, not pixel equality.

## Acceptance remains open

Visual comparison prompted corrections to board menu dimensions/icons, Table spacing, Swimlane headings, focus behavior, context-menu glyphs and tooltip size. The final capture run includes menu, schedule and emoji corrections; the later Aristotle clipping fix has separate native evidence at `rewrite-desktop/aristotle-unclipped.png`. No blanket pixel-parity claim is supported.

- Autopilot schedule spacing and compact searchable timezone trigger were corrected and recaptured; live scheduling still requires an adapter.
- Property-type and reply-mode controls (105/111) now use custom, keyboard-accessible menus with visible captured options. Native Electron property-menu inspection also passed.
- The independent emoji browser now uses MIT-licensed emojibase Unicode data for complete categories, names/keywords and skin variants. Emoji rendering remains OS/font dependent.
- Populated Research and Team detail references remain unobserved. Backend-driven histories, loading/error/streaming states, multiple-record hierarchy and runtime-dependent detail need authentic data and further evidence.
- Browser versus native chrome, font rendering, scrollbars, cursor indicators, animation frames, timestamps, IDs and deliberately different local draft contents must be separated from layout defects.
- Scientific execution and evidence gates, agent streaming, scheduling/webhook provisioning, runtime lifecycle, files/notebook/compute, identity/tokens, secret storage, imports, notifications and updater integration require backend/platform adapters. Draft configuration does not claim activation. Unsupported deletion/workdir/advanced relationship commands report their boundary.

See `dashboard-coverage.md`, `desktop/README.md`, `rewrite-browser/manifest.json` and `docs/specs/frontend-api.md` for the observation and API boundaries.
