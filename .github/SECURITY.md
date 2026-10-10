# Security policy (Encatch fork of Libredesk)

This repository is Encatch's fork of [Libredesk](https://github.com/abhinavxd/libredesk), which
runs Encatch's customer support desk. GitHub shows this file instead of the upstream
`SECURITY.md` in the repository root, which is kept unchanged so upstream releases merge cleanly.

## Reporting a vulnerability

Report privately through GitHub: **[Report a vulnerability](https://github.com/get-encatch/libredesk/security/advisories/new)**.
Please don't open a public issue, pull request or discussion for security problems.

Include what you found, how to reproduce it, and the impact you expect. We aim to acknowledge
reports within 3 working days and will keep you updated until the issue is resolved.

## What to report where

- **Code only in this fork** goes here. That's Encatch's additions:
  - `internal/encatch/`: the My Tickets customer pages and the "Remove for security" redaction
  - `cmd/encatch_*.go`
  - `frontend/apps/main/src/features/encatch/`
  - the deployment setup in `deploy/` and `.github/workflows/encatch-deploy.yml`
- **Issues that also affect upstream Libredesk** should be reported to the Libredesk maintainers
  as their [`SECURITY.md`](../SECURITY.md) describes, at
  https://github.com/abhinavxd/libredesk/security/advisories. Tell us here too, so we can patch
  our deployment quickly.

Upstream's threat model and out-of-scope list also apply to this fork. The My Tickets pages
(`/my-tickets`) are an additional untrusted surface: anyone with a valid signed link from an
Encatch application can use them.

## Please don't

- Test against Encatch's production systems (`desk.encatch.com`, `support.encatch.com`). Run the
  stack locally instead; `deploy/README.md` and `internal/encatch/mytickets/devpreview` help.
- Access, change or delete data that isn't yours, or degrade the service.
