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

- A contact is in a tier if its **Support tier** field (key `plan`) is set to it, **or** its
  email is at one of the tier's `domains` in `config.json` (e.g. `"domains": ["bigcorp.com"]`).
  Domains cover everyone at a company, including first-time contacts. Never list shared
  domains like `gmail.com`. No tier and no domain match = SaaS Standard.
- **New tickets:** the first matching rule sets the SLA and starting priority. For Growth
  Plus and both Enterprise tiers, a subject tag sets the starting priority:
  `[URGENT]`/`[PRIORITY]` → Urgent, `[HIGH]`/`[EXPRESS]` → High, otherwise Medium.
- **Priority changes** (agents triage) re-apply the tier's SLA for the new priority.
  SaaS Growth also re-applies on priority change, which catches a tier recorded after
  the ticket arrived. SLA clocks always count from when the ticket was created.
- Lower tiers have no faster Urgent path: priority only orders the work.
- Resolution targets in the config are generous internal targets.

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
