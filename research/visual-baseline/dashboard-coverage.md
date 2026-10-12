# Desktop dashboard capture coverage

The installed `/Applications/Lykeion.app` is authoritative. The earlier web dashboard is superseded. This inventory records observation, not a declaration of pixel equivalence.

| Surface | Observed evidence in `desktop/` | Rewrite / acceptance status |
| --- | --- | --- |
| Shell | 01,27–29,41,50–56,64 | Sidebar, tabs/history, search, four themes, Help/workspace menus implemented; image comparison ongoing |
| Tasks | 01–03,26,30–34,42,57–63,90,106–109,113–115 | Board/List/Table/Swimlane, filters, display, saved views, table edits/export implemented; card context menus, pins, local relations and calendar controls implemented; unavailable deletion/workdir/relationship operations report their adapter boundary |
| Task details | 26,91–101 | Properties, transcript/composer, resizable inspector implemented; run timeline, inactive wakeup drafts, unverified PR references and subtask prefill implemented; live execution requires adapters |
| Researches/Colleagues | 04–06 | Empty lists/creation observed; populated detail states require additional reference evidence |
| Autopilot | 07,24,68–72 | Schedule, interval, weekday/month, webhook draft controls and wakeup filters implemented; activation requires backend adapter |
| Agents | 08–09,43–44,46–47,81–89,116–118 | Configuration, Work, Capabilities, Settings, avatar/emoji and thinking panels implemented; inherited runtime inventories/history require adapters |
| Teams | 10,20 | Creation/list observed and implemented; populated detail not observed |
| Skills | 11,22–23,73–75 | Manual and import methods inspected; URL/runtime import execution requires adapters |
| Connectors | 12,21,65–67 | STDIO/HTTP/JSON form interactions implemented; secret storage/execution requires backend |
| Machines/runtimes | 13,25,45,76–80 | Machine and runtime details, technical disclosure, custom wizard implemented; daemon lifecycle actions require adapter |
| Settings | 15–16,35–40,48–52,102–105,110–112 | Profile/preferences/catalog forms observed; identity/token/metrics services require backend |
| Inbox/Copilot | 17–19 | Empty states/composer observed; live event/agent execution states require backend |
| Updates | 14 | Update surface observed; platform updater integration pending |

## Interaction checks

Native reference inspection exercised navigation, tabs and context menus, nested pickers, Escape dismissal, Table/Swimlane selection, connector transport/JSON switching, dynamic header rows, schedule modes/day patterns, task inspector tabs and technical disclosures. Existing user records were not edited or deleted; no agent runs, webhook creation, token creation, permission changes, imports or runtime restarts were submitted. The reference was returned to Tasks / All / Board / Ash after capture; the final read observed Agents after a subsequent window change.

`desktop/capture-index.json` identifies screenshots and accessibility receipts by state and hash. Historical `.png` names can contain JPEG bytes; the index records the actual format. These captures contain private workspace content and remain local.

Automated browser checks use isolated first-install profiles and explicitly created disposable test drafts. They cover persistence, rejected scientific stage transitions, independent tab histories, invalid API endpoint rejection, Table edits and column persistence, Swimlane collapse/grouping, CSV formula escaping, auxiliary draft forms, and child-task relationships. The latest checks passed 13 unit tests, 16 distinct functional cases across the frontend and final controls runs, and 10 visual-capture scenarios. See `verification-2026-10-12.md` and `tests/frontend.spec.ts`; passing functional checks do not prove pixel equality.

## Repeatable browser evidence

`tests/visual-capture.spec.ts` runs through `playwright.visual.config.ts` with one worker, isolated browser contexts, a 1920 × 959 viewport and DPR 1. The current manifest (`rewrite-browser/manifest.json`, run `2026-10-12T18:13:52.775Z`) records 112 screenshots with hashes and per-image context differences. There are 110 paired capture entries covering 109 distinct native-reference states; the extra paired entry is the UI-created Teams list. Two captures have no reference counterpart. No screenshot comparison assertion claims pixel equality.

All reference states have browser counterparts except 45, 55–56, 76–80 and 98. The machine/runtime and run-cost surfaces require authentic runtime/execution records; the suite does not invent them. States 55–56 are native application menus outside the browser renderer. This is evidence availability, not proof that those surfaces match.

## Remaining acceptance

- Compare all implemented dialogs and states at identical viewport, scale, theme and content.
- Capture populated Research/Team detail and backend-driven loading/error/streaming states.
- Board/menu, calendar, color and schedule corrections have been recaptured. Exact same-content pixel acceptance remains open; the final Aristotle fix has separate native evidence.
- States 105/111 now capture custom menus; keyboard selection, dismissal and persistence passed. Continue checking other native versus custom popup differences.
- Avatar categories, Unicode-name search and skin variants are implemented from an independently selected MIT dataset; OS emoji glyph rendering can still differ.
- Unsupported deletion, workdir copying and advanced relationship mutations remain explicit adapter boundaries, not simulated successes.
- Provide the backend adapters for runtime inventories, files/notebook/compute, wakeups, runs, identity, tokens, imports and updates.
- Keep factual status/data differences distinct from layout differences; never seed reference-user records to manufacture an apparent match.
