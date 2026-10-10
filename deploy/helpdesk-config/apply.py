#!/usr/bin/env python3
"""Apply config.json (support tiers, SLA policies, SLA automation rules, canned replies) to libredesk.

Idempotent: objects are matched by name and created or updated. Automation rules
whose name starts with RULE_PREFIX are owned by this script; any such rule not
generated from the config is deleted. Other rules are left alone.

Usage (stdlib only):
    export LIBREDESK_URL=https://support.encatch.com
    export LIBREDESK_API_KEY=... LIBREDESK_API_SECRET=...
    python3 apply.py [--dry-run]
"""
import json
import os
import sys
import urllib.error
import urllib.request

RULE_PREFIX = "SLA"
CONFIG = os.path.join(os.path.dirname(os.path.abspath(__file__)), "config.json")
DRY_RUN = "--dry-run" in sys.argv


class API:
    def __init__(self):
        self.base = os.environ["LIBREDESK_URL"].rstrip("/")
        self.auth = "token %s:%s" % (os.environ["LIBREDESK_API_KEY"], os.environ["LIBREDESK_API_SECRET"])

    def call(self, method, path, body=None):
        if DRY_RUN and method != "GET":
            print("  [dry-run] %s %s" % (method, path))
            return {}
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(self.base + path, data=data, method=method)
        req.add_header("Authorization", self.auth)
        req.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(req) as resp:
                return json.load(resp).get("data")
        except urllib.error.HTTPError as e:
            sys.exit("%s %s failed: %s %s" % (method, path, e.code, e.read().decode()[:500]))


def cond(field, operator, value="", field_type="conversation"):
    return {"field": field, "operator": operator, "value": value, "field_type": field_type,
            "case_sensitive_match": False}


def rule_body(match_any, actions, and_any=None, g1_op="OR"):
    """match_any: group 1 conditions, ORed (or ANDed with g1_op="AND"). and_any: optional
    conditions ORed together (group 2) that must also match (groups are ANDed)."""
    groups = [{"logical_op": g1_op, "rules": match_any},
              {"logical_op": "OR", "rules": and_any or []}]
    return [{"groups": groups, "actions": actions, "group_operator": "AND" if and_any else "OR"}]


def main():
    cfg = json.load(open(CONFIG))
    api = API()
    tier_key = cfg["support_tier_attribute"]["key"]

    # 1. Default business hours.
    hours = {h["name"]: h["id"] for h in api.call("GET", "/api/v1/business-hours")}
    bh_id = str(hours[cfg["default_business_hours"]])
    general = api.call("GET", "/api/v1/settings/general")
    if general.get("app.business_hours_id") != bh_id:
        print("business hours: default -> %s" % cfg["default_business_hours"])
        general["app.business_hours_id"] = bh_id
        api.call("PUT", "/api/v1/settings/general", general)

    # 2. Support tier contact attribute.
    a = cfg["support_tier_attribute"]
    attrs = [x for x in api.call("GET", "/api/v1/custom-attributes?applies_to=contact") if x["key"] == a["key"]]
    body = {"applies_to": "contact", "key": a["key"], "name": a["name"], "description": a["description"],
            "data_type": "list", "values": a["values"], "regex": "", "regex_hint": ""}
    if not attrs:
        print("attribute: create %s" % a["name"])
        api.call("POST", "/api/v1/custom-attributes", body)
    elif attrs[0]["name"] != a["name"] or attrs[0]["values"] != a["values"] or attrs[0]["description"] != a["description"]:
        print("attribute: update %s" % a["name"])
        api.call("PUT", "/api/v1/custom-attributes/%d" % attrs[0]["id"], body)

    # 2b. Ticket (conversation) custom fields, matched by key; others are left alone.
    tattrs = {x["key"]: x for x in api.call("GET", "/api/v1/custom-attributes?applies_to=conversation") or []}
    for ta in cfg.get("ticket_attributes", []):
        body = {"applies_to": "conversation", "key": ta["key"], "name": ta["name"], "description": ta["description"],
                "data_type": ta["type"], "values": ta.get("values", []), "regex": "", "regex_hint": ""}
        cur = tattrs.get(ta["key"])
        if not cur:
            print("ticket field: create %s" % ta["name"])
            api.call("POST", "/api/v1/custom-attributes", body)
        elif (cur["name"], cur["description"], cur["data_type"], cur["values"] or []) != \
                (ta["name"], ta["description"], ta["type"], ta.get("values", [])):
            print("ticket field: update %s" % ta["name"])
            api.call("PUT", "/api/v1/custom-attributes/%d" % cur["id"], body)

    # 3. SLA policies, with warning/breach alerts.
    def recipients(spec):
        out = []
        for r in spec:
            if r.startswith("role:"):
                role = r[5:]
                for ag in api.call("GET", "/api/v1/agents") or []:
                    if role in (api.call("GET", "/api/v1/agents/%d" % ag["id"]).get("roles") or []):
                        out.append(str(ag["id"]))
            else:
                out.append(r)
        named = [r for r in dict.fromkeys(out) if not r.isdigit()]
        return named + sorted({r for r in out if r.isdigit()}, key=int)
    alerts = cfg.get("sla_alerts", {})
    warn_to, breach_to = recipients(alerts.get("warning_recipients", [])), recipients(alerts.get("breach_recipients", []))

    def notifications(p):
        n = []
        if warn_to and p.get("warn_before"):
            n.append({"type": "warning", "recipients": warn_to, "time_delay": p["warn_before"],
                      "time_delay_type": "before", "metric": "all"})
        if breach_to:
            n.append({"type": "breach", "recipients": breach_to, "time_delay": "",
                      "time_delay_type": "immediately", "metric": "all"})
        return n

    existing = {s["name"]: s for s in api.call("GET", "/api/v1/sla")}
    for p in cfg["sla_policies"]:
        body = {"name": p["name"], "description": p["description"], "first_response_time": p["first"],
                "next_response_time": p["next"], "resolution_time": p["resolution"],
                "notifications": notifications(p)}
        cur = existing.get(p["name"])
        if not cur:
            print("sla: create %s" % p["name"])
            api.call("POST", "/api/v1/sla", body)
        elif (cur["first_response_time"], cur["next_response_time"], cur["resolution_time"], cur["description"],
              cur.get("notifications") or []) != \
                (p["first"], p["next"], p["resolution"], p["description"], body["notifications"]):
            print("sla: update %s" % p["name"])
            api.call("PUT", "/api/v1/sla/%d" % cur["id"], body)
    sla = {s["name"]: str(s["id"]) for s in api.call("GET", "/api/v1/sla")}
    if DRY_RUN:  # policies not created yet in a dry run
        sla.update({p["name"]: "<new %s>" % p["name"] for p in cfg["sla_policies"] if p["name"] not in sla})
    prio = {p["name"]: str(p["id"]) for p in api.call("GET", "/api/v1/priorities")}
    teams = {t["name"]: str(t["id"]) for t in api.call("GET", "/api/v1/teams") or []}
    default_team = cfg.get("default_team")
    if default_team and default_team not in teams and not DRY_RUN:
        sys.exit("default_team %r does not exist in libredesk; create it first" % default_team)

    # 4. Build the desired automation rules.
    def tier_is(t):
        return cond(tier_key, "equals", t, "contact_custom_attribute")

    def in_tier(t):
        """Contact is in tier t when its Support tier field is set to t."""
        return [tier_is(t["name"])]

    def actions(sla_name, priority=None, tags=(), new_ticket=False):
        acts = []
        if new_ticket and default_team:
            acts.append({"type": "assign_team", "value": [teams.get(default_team, "<team %s>" % default_team)]})
        acts.append({"type": "set_sla", "value": [sla[sla_name]]})
        if priority:
            acts.append({"type": "set_priority", "value": [prio[priority]]})
        if tags:
            acts.append({"type": "add_tags", "value": list(tags)})
        return acts

    TICKET = "conversation_custom_attribute"
    labels = cfg.get("requested_priority_labels", {})

    def ticket_tier_is(t):
        return cond("ticket_tier", "equals", t, TICKET)

    no_ticket_tier = cond("ticket_tier", "not set", "", TICKET)
    new_rules, update_rules = [], []

    # Tickets that carry their own tier (raised via My Tickets) match these first. New-ticket
    # rules are first-match, so they never fall through to the contact-tier rules below.
    for t in cfg["tiers"]:
        name, tags, prios = t["name"], t.get("tags", []), t.get("priorities")
        if prios:
            for level in ("Urgent", "High"):
                new_rules.append(("%s new: ticket tier %s, %s requested" % (RULE_PREFIX, name, labels[level]),
                                  "New ticket with ticket tier %s and requested priority %s: %s priority, %s SLA." % (name, labels[level], level, prios[level]),
                                  rule_body([ticket_tier_is(name)], actions(prios[level], level, tags, new_ticket=True),
                                            and_any=[cond("requested_priority", "equals", labels[level], TICKET)])))
            new_rules.append(("%s new: ticket tier %s" % (RULE_PREFIX, name),
                              "New ticket with ticket tier %s: Medium priority, %s SLA." % (name, prios["Medium"]),
                              rule_body([ticket_tier_is(name)], actions(prios["Medium"], "Medium", tags, new_ticket=True))))
            for level, sla_name in prios.items():
                update_rules.append(("%s priority: ticket tier %s, %s" % (RULE_PREFIX, name, level),
                                     "Ticket tier %s ticket set to %s priority: %s SLA." % (name, level, sla_name),
                                     rule_body([ticket_tier_is(name)], actions(sla_name), and_any=[cond("priority", "equals", prio[level])])))
        else:
            new_rules.append(("%s new: ticket tier %s" % (RULE_PREFIX, name),
                              "New ticket with ticket tier %s: Medium priority, %s SLA." % (name, t["sla"]),
                              rule_body([ticket_tier_is(name)], actions(t["sla"], "Medium", tags, new_ticket=True))))
            update_rules.append(("%s priority: ticket tier %s" % (RULE_PREFIX, name),
                                 "Ticket tier %s ticket priority changed: re-apply %s SLA." % (name, t["sla"]),
                                 rule_body([ticket_tier_is(name)], actions(t["sla"]))))

    # Tickets without a ticket tier (e.g. email) use the contact's Support tier.
    for t in cfg["tiers"]:
        name, tags, prios = t["name"], t.get("tags", []), t.get("priorities")
        if prios:
            # New ticket: subject tag sets the starting priority, otherwise Medium.
            for level in ("Urgent", "High"):
                kw = [cond("subject", "contains", k) for k in cfg["subject_tags"][level]]
                new_rules.append(("%s new: %s, %s subject tag" % (RULE_PREFIX, name, level),
                                  "New %s ticket with a %s subject tag: %s priority, %s SLA." % (name, " / ".join(cfg["subject_tags"][level]), level, prios[level]),
                                  rule_body(in_tier(t), actions(prios[level], level, tags, new_ticket=True), and_any=kw)))
            new_rules.append(("%s new: %s" % (RULE_PREFIX, name),
                              "New %s ticket: Medium priority, %s SLA." % (name, prios["Medium"]),
                              rule_body(in_tier(t), actions(prios["Medium"], "Medium", tags, new_ticket=True))))
            # Priority change: apply the tier's SLA for the new priority (only without a ticket tier).
            for level, sla_name in prios.items():
                update_rules.append(("%s priority: %s, %s" % (RULE_PREFIX, name, level),
                                     "%s contact, ticket without its own tier, set to %s priority: %s SLA." % (name, level, sla_name),
                                     rule_body(in_tier(t) + [no_ticket_tier], actions(sla_name),
                                               and_any=[cond("priority", "equals", prio[level])], g1_op="AND")))
        else:
            match = in_tier(t)
            if t.get("catch_all"):
                # Last new-ticket rule (first-match): catches tickets no tier rule matched.
                # contact_email is a belt-and-braces match for any email sender.
                match = [cond(tier_key, "not set", "", "contact_custom_attribute"),
                         cond("contact_email", "contains", "@")] + match
            new_rules.append(("%s new: %s" % (RULE_PREFIX, name),
                              "New %s ticket%s: Medium priority, %s SLA." % (name, " (or no tier recorded)" if t.get("catch_all") else "", t["sla"]),
                              rule_body(match, actions(t["sla"], "Medium", tags, new_ticket=True))))
            if not t.get("catch_all"):
                # Re-apply on priority change, for tickets whose tier was recorded after they arrived.
                update_rules.append(("%s priority: %s" % (RULE_PREFIX, name),
                                     "%s contact, ticket without its own tier, priority changed: re-apply %s SLA." % (name, t["sla"]),
                                     rule_body(in_tier(t) + [no_ticket_tier], actions(t["sla"]), g1_op="AND")))

    # 5. Sync rules (owned = name starts with RULE_PREFIX).
    current = {}
    for rtype in ("new_conversation", "conversation_update"):
        for r in api.call("GET", "/api/v1/automations/rules?type=%s" % rtype) or []:
            if r["name"].startswith(RULE_PREFIX):
                current[r["name"]] = r
    wanted = {}
    for rtype, events, rules in (("new_conversation", [], new_rules),
                                 ("conversation_update", ["conversation.priority.change"], update_rules)):
        for weight, (name, desc, body) in enumerate(rules):
            wanted[name] = {"name": name, "description": desc, "type": rtype, "events": events,
                            "enabled": True, "weight": weight, "rules": body}
    for name, r in current.items():
        if name not in wanted:
            print("rule: delete %s" % name)
            api.call("DELETE", "/api/v1/automations/rules/%d" % r["id"])
    weights = {}
    for name, w in wanted.items():
        cur = current.get(name)
        if not cur:
            print("rule: create %s" % name)
            created = api.call("POST", "/api/v1/automations/rules", w)
            rid = created.get("id") if created else None
        else:
            rid = cur["id"]
            if (cur["description"], cur["events"], cur["rules"], cur["enabled"]) != (w["description"], w["events"], w["rules"], True):
                print("rule: update %s" % name)
                api.call("PUT", "/api/v1/automations/rules/%d" % rid, w)
        if rid:
            weights[str(rid)] = w["weight"]
    if weights:
        api.call("PUT", "/api/v1/automations/rules/weights", weights)
    api.call("PUT", "/api/v1/automations/rules/execution-mode", {"mode": "first_match"})

    # 6. Canned replies (macros), matched by name; macros not in the config are left alone.
    statuses = {st["name"]: str(st["id"]) for st in api.call("GET", "/api/v1/statuses")}
    macros = {mc["name"]: mc for mc in api.call("GET", "/api/v1/macros") or []}
    for mc in cfg.get("macros", []):
        acts = [{"type": "set_status", "value": [statuses[mc["set_status"]]]}] if mc.get("set_status") else []
        body = {"name": mc["name"], "message_content": mc["message"], "actions": acts,
                "visibility": "all", "visible_when": ["replying"]}
        cur = macros.get(mc["name"])
        if not cur:
            print("macro: create %s" % mc["name"])
            api.call("POST", "/api/v1/macros", body)
            continue
        cur_acts = [{"type": a["type"], "value": a["value"]} for a in (cur or {}).get("actions") or []]
        if cur and (cur["message_content"], cur_acts, cur["visibility"], cur["visible_when"]) != \
                (body["message_content"], acts, "all", ["replying"]):
            print("macro: update %s" % mc["name"])
            api.call("PUT", "/api/v1/macros/%d" % cur["id"], body)

    # 7. Remove obsolete SLA policies.
    for name in cfg.get("remove_sla_policies", []):
        if name in existing:
            print("sla: delete %s" % name)
            api.call("DELETE", "/api/v1/sla/%d" % existing[name]["id"])

    print("done: %d new-ticket rules, %d priority-change rules" % (len(new_rules), len(update_rules)))


if __name__ == "__main__":
    main()
