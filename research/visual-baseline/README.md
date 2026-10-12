# Lykeion visual acceptance baseline

Status: capture in progress. No frontend rewrite or pixel-equivalence claim has been completed.

## Authoritative reference correction — 2026-10-12

The user clarified that the updated reference is **`/Applications/Lykeion.app`**, not the older web application. Native desktop capture is now authoritative; see [desktop evidence](desktop/README.md). All earlier web screenshots and measurements below are historical, superseded evidence and must not drive visual acceptance. The current independent renderer is provisional until compared with the installed desktop interface.

Lykeion brand artwork may be reused with provenance, as explicitly authorized by the user. Application components and styles remain independently implemented.

Scope: the authenticated Lykeion dashboard and its desktop shell. The public marketing website is outside the rewrite scope. Login/onboarding are access paths; landing-page captures do not count toward dashboard acceptance.

The reference is the user's redesigned Lykeion, checked out separately from the new implementation. Its running interface is the visual and behavioral reference. Existing component source is not a translation template.

## Historical web reference identity (superseded)

- Repository: https://github.com/lykeion-ia/lykeion.git
- Commit: `eb8cd0a4c48e9329b001e4c0627625e30eac27a5`
- Temporary checkout: `/tmp/lykeion-visual-reference-20261012`
- Reference web: `http://localhost:13610`
- Reference API: `http://localhost:18910`
- Disposable database: Docker project `lykeion-visual-reference`, port `55436`.
- Python environment: `/tmp/lykeion-visual-api-env`. The checkout's pre-existing `.venv` symlink is not used for installation.
- Baseline viewport: 1440 × 1000 CSS pixels, device scale factor 1, light theme, `en-US`, `Europe/Copenhagen`.

## Capture tooling

Selected tool: [Rea](https://github.com/morluto/rea). Installed CLI version `6.4.0` in `/tmp/lykeion-rea-npm-cache`; documentation checkout at `/tmp/lykeion-rea-tool-20261012`, commit `428eee00fdd0406cdf2b734c9ea14a2bb02da279`.

The CLI entry point is `scripts/rea.mjs`, not the import-only `dist/cli.js`. An initial invocation of the latter produced an empty output and is not capture evidence.

Use `capture-browser-scenario` for declared interactions, DOM/accessibility captures, and screenshots. Use `compare-web-screenshots` for matching reference/rewrite viewport captures. Preserve complete evidence JSON on disk; extract image artifacts for visual inspection. Missing or failed captures cannot satisfy coverage.

The capture browser is the installed Chrome for Testing under `/Users/diegomarono/Library/Caches/ms-playwright/chromium-1217/`. Scenarios use isolated profiles. Reference dependencies were installed from its lockfile; no upstream application source has been copied into the new implementation.

## Required coverage

- Typography, colors, borders, radii, icon geometry, spacing, density and alignment.
- Web and desktop shells, navigation, breadcrumbs, sidebars and pane resize/persistence.
- Research board, task/inquiry detail, chat/activity surfaces, projects, agents/squads, runtimes, settings and updates where present in the reference.
- Menus, dialogs, context menus, command palette, keyboard shortcuts, focus movement and Escape behavior.
- First-install/onboarding, real empty states, loading, errors, disconnected backend, validation and disabled controls.
- Theme and relevant viewport variants. No mobile application is part of this project.

## Implementation boundary

The scientific behavior is defined by [the scientific workflow specification](../../docs/specs/scientific-workflow.md). Visual fidelity does not grant permission to bypass those gates. The frontend must expose typed backend adapters, loading/empty/error states, operation progress and gate rejection details. It must not seed fabricated product records.

Implementation libraries are selected independently after observing the reference surface. Visual acceptance is measured per captured state; unobserved states remain explicitly unresolved. Original application component bodies, stylesheets and assets are not copied by default. Any separately licensed font/icon dependency must be acquired independently with its provenance recorded.
