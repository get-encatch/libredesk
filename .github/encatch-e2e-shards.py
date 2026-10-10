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

# Specs that also fail on upstream's own release. They run in their own shard, which is
# allowed to fail, so the other shards stay meaningful. Recheck after each upstream merge.
KNOWN_UPSTREAM_FAILURES = {
    "cypress/e2e/ui/slaForm.cy.js": "fails on untouched upstream v2.8.0 "
    "('rejects a submit with no name and no SLA time'); recheck after merging v2.9.0",
}

all_specs = set(glob.glob("cypress/e2e/**/*.cy.js", recursive=True))
owner = {}
matrix = []
for name, patterns in SHARDS.items():
    files = sorted({f for p in patterns for f in glob.glob(p, recursive=True)} - set(KNOWN_UPSTREAM_FAILURES))
    for f in files:
        if f in owner:
            sys.exit(f"{f} is in shards {owner[f]} and {name}")
        owner[f] = name
    if files:
        matrix.append({"name": name, "spec": ",".join(files), "allow_failure": False})

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
