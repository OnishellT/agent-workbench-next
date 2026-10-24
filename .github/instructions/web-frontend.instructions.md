---
applyTo: "web/src/**,web/tests/**"
---
# Web frontend (React 19, Vite, Tailwind)

- Strings are authored in Spanish with an English entry in the module's `i18n.ts`; a missing entry must not blank the UI. Add the entry in the same change.
- Each section is a lazy module under `web/src/modules/<name>`; shell code lives in `web/src/app`. Do not import one module's internals from another.
- Effects must clean up (abort controllers, listeners, intervals). No fetch inside render.
- The browser never holds a credential other than the session cookie. Foundry's token stays server-side.
- Browser specs run against a real trial instance: no mocks, no skips for pending acceptance.
