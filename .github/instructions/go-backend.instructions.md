---
applyTo: "cmd/**,internal/**"
---
# Go backend

- Stdlib `net/http` patterns; module handlers are injected through `app.Modules`. No DI or ORM frameworks.
- SQLite through `modernc.org/sqlite` (pure Go, `CGO_ENABLED=0`). One connection per store unless the code already documents otherwise; never run a second query while rows or a transaction are open on the same single connection.
- Every build and test uses `-tags nogui` and an explicit package list (Magpie is a separate module).
- Errors: wrap with `%w`, keep the existing user-facing wording; capitalised error strings are deliberate here.
- Revocation in flight (a source, an SSH identity, app close) must cancel the work and never publish its results.
- Anything that starts a process (agents, the Foundry child, SSH) needs a timeout, a process-group kill and a test of the cancel path.
