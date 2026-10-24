# Magpie integration reference

Workbench scope and acceptance live only in [plan.md](../../plan.md).
This file records integration facts; it does not define another execution plan.

- Upstream: https://github.com/yetone/magpie
- Incorporated release: v0.1.1265
- Incorporated commit: 674b6ca2881f1289c4ccdec158762978a463320c
- License: MIT, retained in LICENSE.
- Separate Go module, executable and original frontend assets. Root
  `make binaries` builds it with `nogui`, preserving the browser UI.
- Native UI uses absolute API paths. The Workbench integration uses dedicated
  host origins and authenticated native panels; preserve Host/Origin, session
  isolation, gateway authentication and plugin-page permissions.
- Automatic gateway mode and built-in plugin switches affect visible pages.
  Showing a page does not install an agent or configure a different user.
- The existing integration has local patches, including browser delivery of
  encrypted backup exports and native-panel framing. Review their diff against
  the pin when updating; keep the original backup format and licenses.

Actor selection and tests against agents opened by the actual Linux host user
are part of the root plan. Existing private test homes do not prove that result.
Native Magpie owns its provider/model/routing configuration and usage records;
Workbench keeps its frontend and native mechanisms.
