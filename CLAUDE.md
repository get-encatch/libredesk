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
- Security fixes taken ahead of a release (2026-10-10, Dependabot triage): cherry-picked upstream `908f3413` (qs 6.16.0) and `92954f1d` (axios 1.20.0) with `-x`; they merge cleanly when the release containing them arrives. Our own lockfile-only update moved prosemirror-view to 1.42.6 (with prosemirror-model 1.25.12) and nanoid to 5.1.16. If `frontend/pnpm-lock.yaml` conflicts on the next merge, take upstream's lockfile, run `pnpm install`, and check prosemirror-view is still >= 1.42.3 with a single prosemirror-model.
- **Merge upstream v2.9.0 as soon as it's released:** upstream `ac5921e1` ("fix security issues found in repository review": media ownership, session revocation, OIDC, widget websocket) is on upstream main only. It changes `media.Insert`, which `internal/encatch/mytickets/adapter.go` calls, so expect a one-line fix there. It also sets `frame-ancestors 'self'` on the agent app page itself, so the `@agentApp` header rule in `deploy/Caddyfile` (added 2026-10-10 as a stopgap) can be removed then.

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

## Releases and container images

- Images are published only to GitHub Container Registry: `ghcr.io/get-encatch/libredesk`, linux/amd64 only (tags `latest` and `vX.Y.Z-encatch.N`). There is no Docker Hub publishing.
- Release by tagging `encatch/main` as `vX.Y.Z-encatch.N`, where `X.Y.Z` is the upstream version we're based on, e.g. `v2.8.0-encatch.1`. Never reuse a plain upstream tag name.
- Pushing such a tag runs `.github/workflows/release.yml` (GoReleaser). It pushes the images and creates a **draft** GitHub Release that someone publishes by hand. Plain upstream tags (`vX.Y.Z`) don't trigger it.
- Deploys need no SSH: after the build, `.github/workflows/encatch-deploy.yml` waits for approval on the GitHub `production` environment (reviewer: godwinpinto; branch policy `encatch/main`), then records a GitHub deployment. The server's deploy agent (`deploy/agent/deploy_agent.py`, systemd `encatch-deploy.timer`, every 2 min) pulls it from the public API, deploys with backup + health check + automatic rollback, and reports via `/.well-known/encatch-deploy.json` on the desk domain. On success the workflow publishes the tag's draft GitHub Release (Latest only if it's the newest version). Runbook: `deploy/README.md` > Deploys. Agent tests: `python3 -m unittest deploy/agent/test_deploy_agent.py`.
- Our edits to `.goreleaser.yaml` and `release.yml` are marked `# encatch:`. Expect small conflicts there when merging upstream releases, and keep our version (GHCR only, `get-encatch` owner).

## GitHub repository security (set 2026-10-10)

- Rulesets: "Protect branches" (no force-push or deletion of `encatch/main`, `main`, `release/*`, no bypass); "Release tags: immutable" (`v*` tags can't be moved or deleted by anyone); "Release tags: who can create" (only org admins create `v*` tags). Syncing upstream tags into the fork therefore needs an org admin.
- Production environment: reviewer godwinpinto only, branch `encatch/main` only, admins can't bypass.
- Actions: approval required for all outside contributors' workflows; only GitHub-owned, verified, and the listed actions (goreleaser, docker, crowdin) may run. A new third-party action must be added under Settings > Actions first.
- Secret scanning with push protection, Dependabot alerts and security updates, and private vulnerability reporting are on. Our policy is `.github/SECURITY.md` (GitHub shows it before upstream's root `SECURITY.md`, which stays untouched). Upstream's PR-welcome workflow is disabled in GitHub (not edited).

## Production deployment

- `deploy/` holds our production stack (server `ubuntu@103.205.140.125`, installed in `/srv/libredesk`). Two domains: **desk.encatch.com** for agents (full app, libredesk Root URL) and **support.encatch.com** for customers (Caddy allowlist of customer paths; everything else shows a static page). New customer-facing features must be added to the support allowlist in `deploy/Caddyfile`. `deploy/README.md` is the runbook: install/update, backups, restore.
- Stack: our GHCR app image (version pinned via `LIBREDESK_VERSION` in the server's `.env`), `postgres:18-alpine` with pgBackRest, Redis, Caddy for HTTPS. Upstream's root `docker-compose.yml` is untouched and not used in production.
- Backups (decided 2026-10-09): pgBackRest weekly full, daily differential, 6-hourly incremental, continuous WAL, keep 5 fulls; nightly `pg_dump` for 7 days; uploads via restic every 6h for 31 days; monthly automated restore test. All on the same disk for now; object storage (pgBackRest `repo2`) is planned.
- Secrets (`.env`, `secrets/restic-password`) exist only on the server and are git-ignored. Never commit them.
- The `postgres` container needs `init: true`: without it, pgBackRest's async archive workers make Postgres (as PID 1) restart in a loop.
- Helpdesk data change made directly in the production DB (no admin UI exists for it): an `Urgent` row in `conversation_priorities`. If an upstream migration ever inserts its own priorities, check it doesn't clash. The reports chart's hard-coded priority list (`OverviewBarChart.vue`) was extended to include it.
- Support tiers, SLA policies and SLA automation rules are config-as-code in `deploy/helpdesk-config/` (`config.json` + `apply.py`, run via the libredesk API). All SLAs use business hours (desk default: IST office hours). The script owns every automation rule named `SLA…`. Teams must not get their own business hours or SLA policy (either overrides the tier SLAs).
- Never change the System agent's email from `System`: libredesk looks it up by that email and refuses to start without it. The agent edit form forces a valid email, so don't save System through it; use `./libredesk --set-system-user-password` for its password.

## Fork changes to upstream code (check these when merging upstream)

All marked `encatch:`. Keep this list current.

- `internal/automation/evaluator.go`: rules read ticket custom attributes (`conversation_custom_attribute`, constant in `internal/automation/models/encatch.go`), and a missing custom attribute counts as empty so "not set" matches. Tests: `internal/automation/encatch_evaluator_test.go`. Candidate upstream PR.
- `internal/user/agent.go`: the System agent's email can't change (libredesk won't start without it).
- `frontend/apps/main/src/features/admin/agents/formSchema.js`: the agent form accepts `System` as an email.
- `frontend/apps/main/src/features/reports/OverviewBarChart.vue`: Urgent priority in the reports chart.
- `cmd/handlers.go`: one call to `initEncatch(g)` (cmd/encatch.go) at the end of `initHandlers`; it sets up all our features.
- `frontend/apps/main/src/features/conversation/message/MessageBubble.vue`: imports and renders `EncatchRedactMenu` next to each bubble, and the incoming-message wrapper is a flex row too (bubbles are `w-fit`, so no visual change). If upstream reworks this file, re-add the tag where the delete-note menu sits.
- `frontend/apps/main/src/router/index.js` and `constants/navigation.js`: two routes and two sidebar entries: Admin > Teammates > Removal log (`/admin/teams/removal-log`, permission `activity_logs:manage`) and Admin > Workspace > Customer organisations (`/admin/customer-organisations`, permission `sla:manage`). Their titles are plain strings used as i18n keys, so no upstream translation files change.
- Database index `encatch_conversations_org_activity` on upstream's `conversations` table (org id from `custom_attributes`, `last_interaction_at`, `id`), created at startup by `mytickets.EnsureIndexes` (`CONCURRENTLY`, in the background; an invalid one is rebuilt). It serves the paged My Tickets list. No upstream file changes; when merging, check upstream still sets `last_interaction_at` for customer-visible messages only (`update-conversation-last-message`) and hasn't added a clashing index.
- Per-instance message gates (`internal/encatch/instancegate`, set in `cmd/encatch_mytickets.go` from `my_tickets.email_instances`, `agent_notification_instances`, `ai_email_instances`): `internal/conversation/message.go` (`sendOutgoingMessage` skips the email send) and `conversation.go` (`SendTransientEmail`, AI agent) call `internal/conversation/encatch.go`; `internal/notification/dispatcher.go` (`Send`, `SendWithEmails`) calls `internal/notification/encatch.go`. If upstream adds another path that emails customers or notifies agents, gate it too.
- `.goreleaser.yaml`: builds only linux/amd64 and publishes only to `ghcr.io/get-encatch/libredesk` (see Releases above). `.github/workflows/release.yml`: runs only on `v*-encatch.*` tags, no Docker Hub.

Our own code (new files only): `internal/encatch/mytickets/` (My Tickets: signed-token customer pages; `adapter.go` is the only file there that calls libredesk code; shadcn component styles with Encatch's design tokens via Tailwind (tokens in `ui/app.css`; logos in `static/`, light SVG and dark PNG from encatch.com): after editing `templates/` run `ui/build.sh` and commit `static/app.css`; preview locally with `go run ./internal/encatch/mytickets/devpreview` on http://localhost:8790) and `cmd/encatch_mytickets.go` (wiring, routes, config). Tickets from instances not listed in `my_tickets.email_instances` (prod only) don't email the customer, those not in `agent_notification_instances` (dev, uat, prod) don't notify agents, and `ai_email_instances` (none) gates AI agent emails; gated tickets get a private note for agents. Each ticket from My Tickets gets the attribute `encatch_instance` and the tag `instance:<code>` (e.g. `instance:prod`, created when missing), added by the System agent just before the customer's first message so the inbox list still previews the customer's text. Customer attachments are stored like agent uploads (private media, signed links) and must pass both libredesk's upload settings and `my_tickets.allowed_extensions`, with `my_tickets.max_total_upload_mb` (50) for all files of one message. `app.server.max_body_size` is set above 5 × libredesk's per-file limit so oversized uploads get a friendly message instead of a bare 413; raise it if that limit goes up.

Customer organisations (support tiers): `internal/encatch/orgtiers/` (tables `encatch_orgs` and `encatch_org_tier_changes`, created at startup), `cmd/encatch_orgs.go` (routes `/api/v1/encatch/orgs...`, all `sla:manage`) and `frontend/apps/main/src/features/encatch/orgs/`. An org is listed when its users first open Support (My Tickets login), or an admin adds it by hand. Its tier (default `SaaS Standard`, choices from `my_tickets.tiers`) goes on each new ticket as `ticket_tier`, which the SLA rules use; changing it affects new tickets only, and every change is logged with who made it.

Redaction ("Remove for security"): `internal/encatch/redact/` (all logic), `cmd/encatch_redact.go` (routes `/api/v1/encatch/...`, libredesk auth and conversation-access checks) and `frontend/apps/main/src/features/encatch/redact/` (menu + dialog). Agents can remove text or files from their own messages, admins from any message including customers'. A reason of at least 5 words is required and every removal is logged in our table `encatch_redactions` (created at startup by `redact.EnsureSchema`, `CREATE TABLE IF NOT EXISTS`; not an upstream migration) and in a private note. Admins (anyone with `activity_logs:manage`) browse and search the log at Admin > Teammates > Removal log (`GET /api/v1/encatch/redactions`). Files are deleted at once; a background job also purges files of deleted private notes immediately, relying on upstream's `delete-private-message` query setting `media.model_id = 0` (check this still holds when merging upstream). Text redaction stores the placeholder as `content_type = 'text'` because the agent app's HTML renderer doesn't re-render in place. Design doc: https://claude.ai/code/artifact/2a8250ea-adf1-44c9-9602-8c6f9c440670
