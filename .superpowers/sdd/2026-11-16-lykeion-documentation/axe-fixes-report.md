# Documentation accessibility repairs

Repaired the three concrete findings from `/private/tmp/lykeion-axe.SFbr8d/results.json` and `widgets.json`:

- Search close button now visibly reads “Close search Esc”; its accessible name comes from that same content, and `aria-keyshortcuts="Escape"` identifies the working shortcut.
- Shared Markdown renderer makes both fenced and indented `<pre>` code examples named, focusable regions. A 3px blue `:focus-visible` outline marks keyboard focus. This is server-rendered and works with JavaScript disabled. Non-overflowing code examples retain a tab stop deliberately to preserve no-JavaScript access without client measurement machinery.
- Stage scene is a named `group`, keeping its evidence/gate/result text exposed individually. It is informative content, so hiding it or replacing its children with an image role would lose information.

Scope: shared article Markdown renderer and snapshot expectation, shared search button, stage markup, one CSS rule, two unit regressions and two browser regressions. No dependencies, client observers or data changes.

## Red and green commands

Before product changes:

`node_modules/.bin/vitest run apps/web/src/app/docs/navigation.test.tsx apps/web/src/app/docs/interactions.test.tsx > /private/tmp/axe-unit-red.log 2>&1`

Output: `Test Files 2 failed (2)`; `Tests 2 failed | 9 passed (11)`. The new tests failed for missing focusable code regions and missing scene group roles.

Initial browser red attempt could not start the sandboxed local server: `listen EPERM: operation not permitted 127.0.0.1:1453`. Browser checks were subsequently run with approved escalation. This is not represented as an assertion failure.

After changes:

`node_modules/.bin/vitest run apps/web/src/app/docs/*.test.ts* > /private/tmp/axe-unit-green.log 2>&1`

Output: `Test Files 6 passed (6)`; `Tests 33 passed (33)`.

`npm --prefix apps/web run typecheck > /private/tmp/axe-typecheck.log 2>&1`

Exit 0, `tsc --noEmit`.

`npm --prefix apps/web run lint > /private/tmp/axe-lint.log 2>&1`

Exit 0, `eslint`.

`node_modules/.bin/playwright test --config playwright.docs.config.ts > /private/tmp/axe-browser-green.log 2>&1`

Output: `14 passed (43.7s)`, `1 skipped`. Skip is the explicit isolated Caddy deployment case. New browser checks verify visible/accessible close text and Escape shortcut; with JavaScript disabled they verify Tab/Shift+Tab focus, solid outline, and actual ArrowRight horizontal scrolling at 1440px and 390px, plus named stage group semantics. Existing rendered-contrast regression: `1607 samples, minimum 4.53:1`.

After making focus screenshots explicitly scroll the code into view:

`LYK_DOCS_URL=http://127.0.0.1:1454 node_modules/.bin/playwright test --config playwright.docs.config.ts --grep 'article commands support' > /private/tmp/axe-focus-green.log 2>&1`

Output: `1 passed (2.6s)`.

`node /private/tmp/lykeion-axe.SFbr8d/fixes.cjs > /private/tmp/lykeion-axe.SFbr8d/fixes.log 2>&1`

Against an isolated development server started with `npm --prefix apps/web run dev -- --hostname 127.0.0.1 --port 1454`. axe-core 4.14.1: **32 checks, zero violations**. Three affected routes (`connect-machine`, `scientific-stages`, `core-concepts`) at 1440/390px; default/search open/search results/search empty; all four selected stage states. Full raw results: `/private/tmp/lykeion-axe.SFbr8d/fixes.json`; exact reproducible audit script: `/private/tmp/lykeion-axe.SFbr8d/fixes.cjs`. An initial attempt before the server persisted returned `ERR_CONNECTION_REFUSED`; the successful rerun produced all 32 checks.

`git diff --check`: exit 0.

## Visual evidence

Screenshots were opened and inspected directly. Code focus has a visible blue ring at both widths; overflowing commands stay within the article; search heading/close label fit on phone and desktop. Evidence in this directory:

- `axe-code-focus-390.png`
- `axe-code-focus-1440.png`
- `axe-search-focus-scientific-stages-390.png`
- `axe-search-focus-connect-machine-1440.png`
- Additional search screenshots: `axe-search-focus-{connect-machine,scientific-stages,core-concepts}-{1440,390}.png`.

## Remaining limits

Only axe `color-contrast` remains incomplete: the learning widgets use background gradients, and decorative aria-hidden scene arrows contain only non-text characters. No `aria-prohibited-attr` incomplete items remain. The existing browser contrast calculation passes at minimum 4.53:1; axe itself still requires human review for these gradient/non-text cases. This targeted rerun does not repeat the earlier 207-state whole-site audit. No production deployment or screen-reader session was performed.

The temporary root `node_modules` symlink was removed after verification; existing `apps/web/node_modules` was preserved.
