# CLAUDE.md

This repo is **get-encatch/libredesk**, the Encatch (CMSS) fork of [abhinavxd/libredesk](https://github.com/abhinavxd/libredesk). The goal is to run a released libredesk version, add our own non-breaking features on top, and keep pulling upstream releases.

## Branches and remotes

- `origin` = get-encatch/libredesk (our fork). `upstream` = abhinavxd/libredesk.
- `release/v2.8.0` is an untouched copy of the upstream `v2.8.0` tag (`086b082`). Never commit our changes to `release/*` branches.
- Our feature work goes on our own integration branch `encatch/main` (the default branch on GitHub), created from `release/v2.8.0`.
- `main` mirrors upstream `main`, which is unreleased code. Do not build from it or deploy it.
- Upstream releases are git tags (`vX.Y.Z`) cut from upstream `main`. There are no upstream release branches.

## Pulling upstream releases

- Merge **stable release tags only**, never upstream `main` or `-rc` tags:
  `git fetch upstream --tags && git merge vX.Y.Z`
- Use merge, not rebase, on shared branches.
- Merge each release as it ships, so we never fall several versions behind.

## Rules for our changes (to keep upstream merges easy)

- Prefer adding new files over editing upstream files. Put Encatch code in its own folders (e.g. `internal/encatch/` for the backend, a separate folder under `frontend/`). Keep the hooks into upstream code small and mark them with an `// encatch:` comment.
- Database: don't edit upstream `schema.sql` or the per-version migration files (`internal/migrations/v*.go`). Prefix our tables with `encatch_` and keep our migrations separate.
- Don't change existing upstream APIs, tables or columns. Add new ones instead.
- Consider sending generic fixes and hooks upstream as PRs, to reduce what we maintain ourselves.

## Licence: AGPL-3.0 (we've decided to keep this fork open source)

- The fork stays public. Code deployed to users must match the public source, so push before or when deploying and never run private-only changes.
- Keep `LICENSE` and the upstream copyright notices. All code in this repo is AGPL-3.0.
- Keep a visible note that this is a modified version of libredesk (README, and ideally a source link in the app).
- Encatch services that only talk to libredesk over its API or webhooks are separate works. Code merged into this repo or binary is AGPL.
