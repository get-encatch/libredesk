#!/usr/bin/env python3
"""encatch: splits upstream's Cypress specs into parallel shards for
.github/workflows/encatch-e2e.yml and prints the job matrix. Fails if a spec is in no
shard or in two, so new upstream specs can't be skipped silently."""
import glob
import json
import os
import sys

os.chdir(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "frontend"))

SHARDS = {
    "api": ["cypress/e2e/*.cy.js", "cypress/e2e/api/*.cy.js"],
    "pages-a-d": ["cypress/e2e/ui/[a-d]*.cy.js"],
    "pages-e-z": ["cypress/e2e/ui/[e-z]*.cy.js"],
    "livechat": ["cypress/e2e/integration/**/*.cy.js"],
}

# Specs that only pass on a fresh app: run alone, in their own shard (they still fail the
# check if they break). slaForm: 'rejects a submit with no name and no SLA time' fails when
# run after other specs in upstream's one-after-another run (even on untouched v2.8.0),
# but passes alone. Recheck after each upstream merge.
ISOLATED = ["cypress/e2e/ui/slaForm.cy.js"]

# Specs that fail on upstream's own release even alone. They run in a shard that warns
# instead of failing. Empty now; keep the reason next to each entry.
KNOWN_UPSTREAM_FAILURES = {}

all_specs = set(glob.glob("cypress/e2e/**/*.cy.js", recursive=True))
owner = {}
matrix = []
for name, patterns in SHARDS.items():
    files = sorted({f for p in patterns for f in glob.glob(p, recursive=True)} - set(ISOLATED) - set(KNOWN_UPSTREAM_FAILURES))
    for f in files:
        if f in owner:
            sys.exit(f"{f} is in shards {owner[f]} and {name}")
        owner[f] = name
    if files:
        matrix.append({"name": name, "spec": ",".join(files), "allow_failure": False})

for f in ISOLATED:
    if f in all_specs:
        owner[f] = "isolated"
        matrix.append({"name": os.path.basename(f).removesuffix(".cy.js"), "spec": f, "allow_failure": False})

known = sorted(f for f in KNOWN_UPSTREAM_FAILURES if f in all_specs)
if known:
    matrix.append({"name": "known-upstream-failures", "spec": ",".join(known), "allow_failure": True})
    owner.update({f: "known-upstream-failures" for f in known})

missing = sorted(all_specs - set(owner))
if missing:
    sys.exit("specs in no shard (add them to SHARDS): " + ", ".join(missing))

for m in matrix:
    print(f"{m['name']}: {m['spec'].count(',') + 1} specs", file=sys.stderr)
print("matrix=" + json.dumps(matrix))
