---
applyTo: "internal/hosts/**,internal/models/**,internal/app/**,internal/config/**"
---
# Hosts, models and the single-origin server

- SSH: the host key is pinned and verified on every connection; a changed identity stops the operation and surfaces it. Never fall back to trust-on-every-use.
- Commands sent over SSH are static strings built from allowlisted names; user input is never interpolated into a shell.
- Cookies are per instance and per native site; session values are random and held in memory; the token is never placed in a cookie or URL.
- `X-Forwarded-Proto` is trusted only from configured loopback proxies.
- Native Magpie runs under the real user HOME without copying or chmodding it.
