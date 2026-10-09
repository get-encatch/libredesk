#!/usr/bin/env python3
"""Apply config.json (support tiers, SLA policies, SLA automation rules) to libredesk.

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


def rule_body(conditions, actions, any_of=None):
    """conditions: list ANDed together (group 1). any_of: optional list ORed together (group 2), ANDed with group 1."""
    groups = [{"logical_op": "AND", "rules": conditions},
              {"logical_op": "OR", "rules": any_of or []}]
    return [{"groups": groups, "actions": actions, "group_operator": "AND" if any_of else "OR"}]


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

    # 3. SLA policies.
    existing = {s["name"]: s for s in api.call("GET", "/api/v1/sla")}
    for p in cfg["sla_policies"]:
        body = {"name": p["name"], "description": p["description"], "first_response_time": p["first"],
                "next_response_time": p["next"], "resolution_time": p["resolution"],
                "notifications": existing.get(p["name"], {}).get("notifications") or []}
        cur = existing.get(p["name"])
        if not cur:
            print("sla: create %s" % p["name"])
            api.call("POST", "/api/v1/sla", body)
        elif (cur["first_response_time"], cur["next_response_time"], cur["resolution_time"], cur["description"]) != \
                (p["first"], p["next"], p["resolution"], p["description"]):
            print("sla: update %s" % p["name"])
            api.call("PUT", "/api/v1/sla/%d" % cur["id"], body)
    sla = {s["name"]: str(s["id"]) for s in api.call("GET", "/api/v1/sla")}
    if DRY_RUN:  # policies not created yet in a dry run
        sla.update({p["name"]: "<new %s>" % p["name"] for p in cfg["sla_policies"] if p["name"] not in sla})
    prio = {p["name"]: str(p["id"]) for p in api.call("GET", "/api/v1/priorities")}

    # 4. Build the desired automation rules.
    def tier_is(t):
        return cond(tier_key, "equals", t, "contact_custom_attribute")

    def actions(sla_name, priority=None, tags=()):
        acts = [{"type": "set_sla", "value": [sla[sla_name]]}]
        if priority:
            acts.append({"type": "set_priority", "value": [prio[priority]]})
        if tags:
            acts.append({"type": "add_tags", "value": list(tags)})
        return acts

    new_rules, update_rules = [], []
    for t in cfg["tiers"]:
        name, tags, prios = t["name"], t.get("tags", []), t.get("priorities")
        if prios:
            # New ticket: subject tag sets the starting priority, otherwise Medium.
            for level in ("Urgent", "High"):
                kw = [cond("subject", "contains", k) for k in cfg["subject_tags"][level]]
                new_rules.append(("%s new: %s, %s subject tag" % (RULE_PREFIX, name, level),
                                  "New %s ticket with a %s subject tag: %s priority, %s SLA." % (name, " / ".join(cfg["subject_tags"][level]), level, prios[level]),
                                  rule_body([tier_is(name)], actions(prios[level], level, tags), any_of=kw)))
            new_rules.append(("%s new: %s" % (RULE_PREFIX, name),
                              "New %s ticket: Medium priority, %s SLA." % (name, prios["Medium"]),
                              rule_body([tier_is(name)], actions(prios["Medium"], "Medium", tags))))
            # Priority change: apply the tier's SLA for the new priority.
            for level, sla_name in prios.items():
                update_rules.append(("%s priority: %s, %s" % (RULE_PREFIX, name, level),
                                     "%s ticket set to %s priority: %s SLA." % (name, level, sla_name),
                                     rule_body([tier_is(name), cond("priority", "equals", prio[level])], actions(sla_name))))
        else:
            conds = [tier_is(name)]
            any_of = None
            if t.get("catch_all"):
                conds, any_of = [], [cond(tier_key, "not set", "", "contact_custom_attribute"), tier_is(name)]
            new_rules.append(("%s new: %s" % (RULE_PREFIX, name),
                              "New %s ticket%s: Medium priority, %s SLA." % (name, " (or no tier recorded)" if t.get("catch_all") else "", t["sla"]),
                              rule_body(conds, actions(t["sla"], "Medium", tags), any_of=any_of) if conds else
                              [{"groups": [{"logical_op": "OR", "rules": any_of}, {"logical_op": "OR", "rules": []}],
                                "actions": actions(t["sla"], "Medium", tags), "group_operator": "OR"}]))
            if not t.get("catch_all"):
                # Re-apply on priority change, for tickets whose tier was recorded after they arrived.
                update_rules.append(("%s priority: %s" % (RULE_PREFIX, name),
                                     "%s ticket priority changed: re-apply %s SLA (catches tier recorded late)." % (name, t["sla"]),
                                     rule_body([tier_is(name)], actions(t["sla"]))))

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

    # 6. Remove obsolete SLA policies.
    for name in cfg.get("remove_sla_policies", []):
        if name in existing:
            print("sla: delete %s" % name)
            api.call("DELETE", "/api/v1/sla/%d" % existing[name]["id"])

    print("done: %d new-ticket rules, %d priority-change rules" % (len(new_rules), len(update_rules)))


if __name__ == "__main__":
    main()
