#!/usr/bin/env python3
"""Build a signed My Tickets sign-in link (reference implementation, stdlib only).

Encatch backend apps do the same in their own language with any JWT library: an
HS256 JWT with the claims below, valid for at most 60 seconds, used once.

    MY_TICKETS_SECRET=... python3 make-link.py --iss encatch-test --user 9134 \\
        --email anita@bigcorp.com --name "Anita Rao" --org 42 --org-name BigCorp \\
        --tier "Growth Plus" --project 17:"Mobile app" --scope self

Prints https://support.encatch.com/my-tickets/login?token=<jwt>; open it within 60 s.
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
    p.add_argument("--iss", required=True, help="issuing app, e.g. encatch-dashboard")
    p.add_argument("--user", required=True, help="Encatch user ID")
    p.add_argument("--email", required=True)
    p.add_argument("--name", default="")
    p.add_argument("--org", required=True, help="Encatch org ID")
    p.add_argument("--org-name", required=True)
    p.add_argument("--tier", default="", help="the org's support tier")
    p.add_argument("--project", action="append", default=[], help='ID:"Name", repeatable')
    p.add_argument("--current-project", default="")
    p.add_argument("--scope", default="self", choices=["self", "project", "org"])
    p.add_argument("--base", default="https://support.encatch.com")
    a = p.parse_args()

    secret = os.environ.get("MY_TICKETS_SECRET")
    if not secret:
        sys.exit("set MY_TICKETS_SECRET to the issuer's secret")

    now = int(time.time())
    claims = {
        "iss": a.iss, "external_user_id": a.user, "email": a.email, "name": a.name,
        "org_id": a.org, "org_name": a.org_name, "scope": a.scope,
        "iat": now, "exp": now + 60, "jti": str(uuid.uuid4()),
    }
    if a.tier:
        claims["support_tier"] = a.tier
    if a.project:
        claims["projects"] = [{"id": pid, "name": name} for pid, _, name in (x.partition(":") for x in a.project)]
    if a.current_project:
        claims["current_project_id"] = a.current_project
    print(f"{a.base}/my-tickets/login?token={sign_hs256(claims, secret)}")


if __name__ == "__main__":
    main()
