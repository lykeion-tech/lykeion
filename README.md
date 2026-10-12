# Lykeion independent frontend

A TypeScript dashboard shared by the web and Electron desktop shells. The installed Lykeion desktop app is a visual reference; the renderer is independently implemented with Vue, Vue Router and Lucide. The scientific workflow specification remains authoritative for stage transitions.

## Run

```sh
npm ci
npm run dev
```

Open http://127.0.0.1:1430. For the desktop preview:

```sh
npm run build
npm run desktop:dev
```

For a desktop renderer using the production assets, use `npm run desktop` after building. The preview uses the separate `Lykeion Preview` application data directory and does not overwrite the installed reference application's profile.

## Verify

Run checks sequentially:

```sh
npm run typecheck
npm test
npm run test:e2e
npm run build
```

Browser tests need a Playwright Chromium installation (`npx playwright install chromium`) or `PLAYWRIGHT_CHROMIUM_EXECUTABLE` pointing at a compatible installed test browser. Tests create disposable records in isolated browser profiles. The normal first-install workspace is empty.

## Backend boundary

The local adapter persists explicitly created drafts and comments. It does not start agents or claim scientific evidence. Scientific stage advancement is rejected locally. Settings → API connection selects the typed HTTP adapter when a compatible Go service is available; Python scientific services belong behind that service.

See [frontend API](docs/specs/frontend-api.md), [scientific workflow](docs/specs/scientific-workflow.md), and [capture coverage](research/visual-baseline/dashboard-coverage.md). Runtime execution, credentials, authenticated workspace management, scheduled activation, imports, notebook kernels and live usage history still require backend integration. Controls expose these boundaries rather than fabricating outcomes.

## Visual acceptance

`research/visual-baseline/desktop/` contains local reference captures and accessibility receipts; its index records screenshot hashes and actual encodings. The earlier web reference is superseded. `rewrite-desktop/` holds preview captures. Pixel equality has not been established across every state.

Reference captures include private workspace content and must not be published as sample product data. See [asset provenance](docs/specs/asset-provenance.md) for reused brand material.
