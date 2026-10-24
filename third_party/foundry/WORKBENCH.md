# Foundry integration reference

Workbench scope and acceptance live only in [plan.md](../../plan.md). This file records
integration facts; it does not define another execution plan.

- Upstream: Conversation Foundry loop, `https://github.com/OnishellT/foundry`
- Incorporated commit: `0dc27d9dbd3975f4d23b0bb4b5e3e40b08439964` (2026-10-23)
- License: AGPL-3.0-only (`LICENSE`, `NOTICE`, `VENDOR_PROVENANCE.json` kept as shipped).
  Vendored inner sources keep their own licenses next to them (`foundry_loop/vendor/`).
- Python 3.11+, standard library only; its own SQLite store, token and loopback server.
  Dropped from the copy: `.git`, `.github`, `flake.nix`, `flake.lock` (Workbench has its own flake).

## How Workbench runs it

- `third_party/foundry/embed.go` embeds `foundry_loop/` (code + prebuilt offline UI) and `examples/`
  (the package reads it as its sibling for the guided example) in the Workbench binary. `internal/foundryloop` unpacks it under
  `<state-dir>/foundry-loop/runtime/<content hash>/`, starts
  `python3 -m foundry_loop --data <state-dir>/foundry-loop/data start --port <free port>`
  on loopback at the first `/foundry/` request, and stops it when Workbench closes.
- `/foundry/*` is reverse-proxied behind the Workbench session: Workbench supplies Foundry's
  token (read from Foundry's own store) and drops the browser's cookies, Origin and any
  `X-Foundry-Token`. The token never reaches the browser. Foundry's CSP `frame-ancestors 'none'`
  is relaxed to `'self'` so the Foundry section can frame it.
- Overrides: `WORKBENCH_FOUNDRY_PYTHON` (interpreter), `WORKBENCH_FOUNDRY_DATA` (absolute data
  directory, e.g. to reuse an existing store while its own service is stopped). Without a configured
  interpreter Workbench tries `python3`, `python3.14`…`python3.11`, `python` and takes the first 3.11+.
  `WORKBENCH_*` variables are not passed to the child.
- Capture is opt-in exactly as upstream: a fresh store has no sources. Nothing here registers
  or scans a source on its own.

## Local patches (review their diff against the pin when updating)

- `ui/src/api.ts`: requests resolve relative to the document (`api/state`, not `/api/state`),
  so the same build works at `/` and under `/foundry/`. The old `window.__WORKBENCH__`
  rewrite to `/api/foundry/` is gone.
- `ui/vite.config.ts`: `base: "./"` (relative asset URLs).
- `ui/src/main.tsx`: `?embedded=1&view=<page>` hides Foundry's sidebar; the host drives the page
  with `postMessage({type:"foundry:view"})` and Foundry reports page changes with
  `{type:"foundry:navigated"}`, same-origin only.
- Theme: `ui/src/index.css` uses the Workbench tokens (`web/src/styles/theme.css`, light and dark) instead
  of its own dark-only palette; every hard-coded colour became a token (status colours via `--ok`/`--warn`).
  `ui/src/main.tsx` takes `?theme=` and `foundry:theme` messages (system preference when standalone);
  `index.html` no longer forces `class="dark"`. When changing Foundry's UI, keep colours as tokens.
- Embedded layout (`ui/src/index.css`, `ui/src/App.tsx`): no sidebar, no breadcrumb, compact bar.
- Language: Foundry is written in Spanish. `ui/src/i18n.ts` translates what reaches the DOM (text nodes and
  `placeholder`/`title`/`aria-label`/`alt`, never `pre`/`code`/transcripts) from `ui/src/i18n/en.ui.ts` (UI)
  and `en.server.ts` (backend messages): exact strings plus `{name}` patterns for text built at runtime.
  `ui/src/main.tsx` takes `?lang=` and `foundry:lang` messages (the browser's language when standalone) and
  remounts the app on a change; `api.ts` formats dates in that locale. No component was edited for this.
  **When upstream adds or changes a string**, `bun test tests` (UI) or `python -m unittest tests.test_i18n_coverage`
  (backend messages) fails until it has an entry. Strings assembled by concatenation need a pattern
  (`"Cambiar ruta de {x}"`): the browser test `web/tests/e2e/foundry-language-release.spec.ts` walks every page
  and dialog in English and reports Spanish text that is left.
- Dev dependency `@babel/parser` (pinned) for the UI coverage test.
- `foundry_loop/web/`: rebuilt from the above (`make foundry-ui`).
- `tests/test_web_delivery.py`: resolves asset URLs with `urljoin` (the build now emits `./assets/…`).

## Tests

`make foundry-test` (Foundry's own suite), `internal/foundryloop` (real embedded Foundry behind the
proxy), `web/tests/e2e/foundry-release.spec.ts` (real browser against a trial instance).
