#!/usr/bin/env python3
"""Build a signed My Tickets sign-in link (reference implementation, stdlib only).

core-accounts' /support page does the same: an HS256 JWT listing every org the user
has ticket access in, valid for at most 60 seconds, used once. It POSTs the token
(field "token") to /my-tickets/login; this script prints a GET link for testing.

    MY_TICKETS_SECRET=... python3 make-link.py --instance prod --user 9134 \\
        --email anita@bigcorp.com --name "Anita Rao" \\
        --org 42:manage:BigCorp --project 42:17:manage:"Mobile app" \\
        --org 7::Acme --project 7:3:read:iOS

--org ID:ACCESS:NAME      ACCESS manage|read (all the org's tickets) or empty (projects only)
--project ORG:ID:ACCESS:NAME   ACCESS manage|read

The issuer is encatch-accounts-<instance> (local, dev, uat, prod, ...), signed with that
instance's secret. Send the instance's own numeric ids: the helpdesk prefixes them
with the instance ("prod-42"), so ids from different instances never collide.
"""
import argparse
import base64
import hashlib
import hmac
import json
import os
import sys
import time
import uuid


def b64url(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode()


def sign_hs256(claims: dict, secret: str) -> str:
    header = b64url(json.dumps({"alg": "HS256", "typ": "JWT"}, separators=(",", ":")).encode())
    payload = b64url(json.dumps(claims, separators=(",", ":")).encode())
    sig = hmac.new(secret.encode(), f"{header}.{payload}".encode(), hashlib.sha256).digest()
    return f"{header}.{payload}.{b64url(sig)}"


def main() -> None:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--instance", required=True, help="Encatch instance: local, dev, uat or prod")
    p.add_argument("--user", required=True, help="Encatch user ID")
    p.add_argument("--email", required=True)
    p.add_argument("--name", default="")
    p.add_argument("--org", action="append", required=True, help="ID:ACCESS:NAME, repeatable")
    p.add_argument("--project", action="append", default=[], help="ORG:ID:ACCESS:NAME, repeatable")
    p.add_argument("--base", default="https://support.encatch.com")
    a = p.parse_args()

    secret = os.environ.get("MY_TICKETS_SECRET")
    if not secret:
        sys.exit("set MY_TICKETS_SECRET to the issuer's secret")

    now = int(time.time())
    orgs = {}
    for o in a.org:
        oid, access, name = (o.split(":", 2) + ["", ""])[:3]
        orgs[oid] = {"id": oid, "name": name, "access": access, "projects": []}
    for x in a.project:
        oid, pid, access, name = (x.split(":", 3) + ["", "", ""])[:4]
        if oid not in orgs:
            sys.exit(f"--project {x}: add --org {oid}:...: first")
        orgs[oid]["projects"].append({"id": pid, "name": name, "access": access})
    claims = {
        "iss": f"encatch-accounts-{a.instance}", "instance": a.instance, "external_user_id": a.user, "email": a.email, "name": a.name,
        "orgs": list(orgs.values()), "iat": now, "exp": now + 60, "jti": str(uuid.uuid4()),
    }
    print(f"{a.base}/my-tickets/login?token={sign_hs256(claims, secret)}")


if __name__ == "__main__":
    main()
