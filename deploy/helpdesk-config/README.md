# Helpdesk configuration (support tiers and SLAs)

`config.json` is the source of truth for Encatch's support tiers, SLA policies, the
automation rules that apply them, and the canned replies (macros). `apply.py` pushes it to libredesk through the API.
Change the JSON, run the script, commit both.

## Model

All times are **business hours** on the desk's default business hours
(**IST office hours**, Mon–Fri 10:00–19:00).

| Support tier | Low | Medium | High | Urgent |
|---|---|---|---|---|
| SaaS Standard | 48h | 48h | 48h | 48h |
| SaaS Growth | 24h | 24h | 24h | 24h |
| Growth Plus | 24h | 24h | 8h | 2h |
| Enterprise Standard | 24h | 24h | 8h | 2h |
| Enterprise Premium | 16h | 8h | 2h | 30m |

- The tier comes only from the contact's **Support tier** field (key `plan`), set by the team
  per contact. No tier recorded = SaaS Standard.
- **New tickets:** the first matching rule sets the SLA and starting priority. For Growth
  Plus and both Enterprise tiers, a subject tag sets the starting priority:
  `[URGENT]`/`[PRIORITY]` → Urgent, `[HIGH]`/`[EXPRESS]` → High, otherwise Medium.
- **Priority changes** (agents triage) re-apply the tier's SLA for the new priority.
  SaaS Growth also re-applies on priority change, which catches a tier recorded after
  the ticket arrived. SLA clocks always count from when the ticket was created.
- Lower tiers have no faster Urgent path: priority only orders the work.
- Resolution targets in the config are generous internal targets.

## Teams and alerts

- Every new ticket is assigned to the `default_team` (**Support**, round robin among its
  online members). Engineering and Billing get tickets by direct assignment.
  Agents and team membership are managed in the UI, not here (emails stay out of this public repo).
- Each SLA policy warns its `warn_before` time ahead of the deadline (to the assigned agent)
  and alerts on breach (assigned agent + all Admins). `role:<Role>` recipients are resolved
  to agent IDs at apply time. Alerts appear in-app, and by email once notification email is set up.

## Canned replies

| Macro | Also sets status |
|---|---|
| Acknowledge | — |
| Ask for bug details | Waiting on customer |
| Escalated to engineering | Waiting on engineering |
| Feature request logged | Resolved |
| Billing handover | — |
| Resolved - anything else? | Resolved |
| Closing (no response) | Resolved |

Greetings are a neutral "Hi,": contacts that email without a display name get their email
prefix (e.g. "rahul.gorad") as a first name. Placeholders available when the reply is sent:
`{{ .Author.FirstName }}`, `{{ .Conversation.ReferenceNumber }}`, `{{ .Contact.FirstName }}`. Macros are matched
by name; macros not in the config are left alone.

## Running it

Needs an API key for an admin agent (Admin → Agents → System → Generate API key). Keep
the key out of git, and revoke it in the UI when you're done.

```bash
export LIBREDESK_URL=https://support.encatch.com
export LIBREDESK_API_KEY=... LIBREDESK_API_SECRET=...
python3 apply.py --dry-run   # show what would change
python3 apply.py             # apply
```

On a fresh helpdesk, create these first (the script expects them; in production they
were made by hand):

- Business hours "IST office hours" (Mon–Fri 10:00–19:00), and the general timezone
  `Asia/Kolkata`.
- Teams "Support" (round robin), "Engineering" and "Billing", with no business hours or
  SLA of their own.
- Priority `Urgent` (no admin UI: insert a `conversation_priorities` row).
- Statuses "Waiting on customer" and "Waiting on engineering" (used by the macros).

The script is idempotent: objects are matched by name. It **owns every automation rule
whose name starts with `SLA`**: it creates, updates and deletes those to match the
config. Name hand-made rules differently. It also sets new-ticket rules to
"first match wins".

## Gotchas

- Libredesk computes SLA deadlines with the **assigned team's business hours** when the
  team has some set. Leave teams without business hours, and without a team SLA policy.
  Either would override these rules.
- Priority `Urgent` was added directly in the database (no admin UI for priorities).
- Never change the System agent's email from `System`. Libredesk looks that account up
  by email, and the app won't start without it.
