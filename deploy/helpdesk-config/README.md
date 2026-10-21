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
- **New-ticket alert** (`new_ticket_alert`): every new-ticket rule also runs a Notify action, so
  each new ticket alerts these recipients once (in-app and by email). Recipients are
  `agent:<Full name>`, `team:<Team>` or `role:<Role>`, resolved to IDs at apply time. Notify
  only reaches agents, so a group mailbox gets an agent of its own (**Notification Desk**: no
  team, so it's never assigned tickets; no password). Never use the Support inbox's own address:
  the alert would come back as a new ticket, again and again. Subject and message are plain
  text; the email adds a link to the ticket.

## Auto-close (`auto_close`)

Tickets left **Waiting on customer** get an automated reminder after `reminder_after_hours`
(72) and status **Auto-closing**, then an automated close message after `close_after_hours`
(120) and status **Resolved**. Two time-trigger rules, `Auto-close: reminder` and
`Auto-close: close`.

- Hours count from the agent's last reply, in clock hours (weekends included). Automated
  replies come from the System user and don't restart the clock.
- Any customer reply (email or My Tickets) sets the ticket to Open, which stops both rules.
  The next agent reply that sets Waiting on customer starts the countdown again.
- Time triggers run hourly (so up to an hour late) and only see tickets created in the last
  30 days (hard-coded in libredesk): older tickets aren't auto-closed.
- The messages say they're automated and how to keep or reopen the ticket. They're plain
  HTML with no `{{ }}` placeholders: libredesk fills placeholders only in the emailed copy,
  so My Tickets and the agent app would show them raw. The email's subject line already
  carries the ticket's subject and number.
- The emails follow the same rules as agent replies: customers of non-prod instances aren't
  emailed (`my_tickets.email_instances`); My Tickets shows the messages either way.
- My Tickets shows Auto-closing as "Waiting on you" (`internal/encatch/mytickets/model.go`).

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
export LIBREDESK_URL=https://desk.encatch.com   # not the support domain: it blocks the admin API
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

Statuses (`statuses`: Waiting on customer, Waiting on engineering, Auto-closing) are created
by the script when missing; existing ones are left alone.

The script is idempotent: objects are matched by name. It **owns every automation rule
whose name starts with `SLA` or `Auto-close`**: it creates, updates and deletes those to
match the config. Name hand-made rules differently. It also sets new-ticket rules to
"first match wins".

## Gotchas

- Libredesk computes SLA deadlines with the **assigned team's business hours** when the
  team has some set. Leave teams without business hours, and without a team SLA policy.
  Either would override these rules.
- Priority `Urgent` was added directly in the database (no admin UI for priorities).
- Never change the System agent's email from `System`. Libredesk looks that account up
  by email, and the app won't start without it.
