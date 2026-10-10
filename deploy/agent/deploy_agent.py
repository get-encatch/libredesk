#!/usr/bin/env python3
"""Encatch deploy agent: deploys releases approved in GitHub, pulling them itself.

Runs on the server from a systemd timer (deploy/systemd/encatch-deploy.timer). It
needs no GitHub credentials and accepts no inbound connections:

1. GitHub: pushing a v*-encatch.N tag builds the image (release.yml). The
   encatch-deploy.yml workflow then waits for approval on the "production"
   environment and, once approved, records a GitHub deployment
   (task "deploy:encatch") for that tag.
2. This agent reads the latest such deployment from GitHub's public API (the repo
   is public; only users with write access can create deployments), checks it,
   and deploys: backup, deploy/ files from the tag, new image, health check, and
   rollback if unhealthy.
3. It writes its progress to deploy-state/public/status.json, which Caddy serves
   at https://<desk>/.well-known/encatch-deploy.json. The approved workflow job
   watches that file and marks the GitHub deployment success or failure.

Standard library only. Usage: deploy_agent.py [--dry-run]
"""

import fcntl
import hashlib
import io
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request
from datetime import datetime, timezone

REPO = "get-encatch/libredesk"
TASK = "deploy:encatch"
ENVIRONMENT = "production"
CREATOR = "github-actions[bot]"
TAG_RE = re.compile(r"^v\d+\.\d+\.\d+-encatch\.\d+$")
MAX_AGE_SECONDS = 6 * 3600  # an approval older than this must be given again
HEALTH_TIMEOUT = 180

BASE = os.environ.get("ENCATCH_DEPLOY_BASE", "/srv/libredesk")
STATE = os.path.join(BASE, "deploy-state")
STATUS = os.path.join(STATE, "public", "status.json")
LAST = os.path.join(STATE, "last_deployment_id")
PREVIOUS = os.path.join(STATE, "previous")  # deploy files before the last deploy, for rollback
# Never overwritten from the release: server-only files and data.
KEEP = [".env", ".env.bak", "data/", "backups/", "secrets/", "deploy-state/"]


class DeployError(Exception):
    pass


def log(msg):
    print(f"{datetime.now(timezone.utc).isoformat(timespec='seconds')} {msg}", flush=True)


# ---- GitHub (public, read-only) ----

def latest_deployment():
    url = (f"https://api.github.com/repos/{REPO}/deployments"
           f"?environment={ENVIRONMENT}&task={TASK.replace(':', '%3A')}&per_page=1")
    req = urllib.request.Request(url, headers={
        "Accept": "application/vnd.github+json", "User-Agent": "encatch-deploy-agent"})
    with urllib.request.urlopen(req, timeout=20) as r:
        items = json.load(r)
    return items[0] if items else None


def check(dep, now=None):
    """Returns the tag to deploy, or raises DeployError if the deployment isn't acceptable."""
    now = now or time.time()
    creator = (dep.get("creator") or {}).get("login")
    if creator != CREATOR:
        raise DeployError(f"not created by the deploy workflow (creator {creator!r})")
    if dep.get("task") != TASK or dep.get("environment") != ENVIRONMENT:
        raise DeployError("wrong task or environment")
    tag = dep.get("ref", "")
    if not TAG_RE.match(tag):
        raise DeployError(f"ref {tag!r} is not a release tag")
    payload = dep.get("payload") or {}
    if isinstance(payload, str):
        payload = json.loads(payload or "{}")
    if payload.get("version") != tag:
        raise DeployError("payload version doesn't match the tag")
    created = datetime.fromisoformat(dep["created_at"].replace("Z", "+00:00")).timestamp()
    if now - created > MAX_AGE_SECONDS:
        raise DeployError("approval is older than 6 hours; approve a new deploy")
    return tag


# ---- local state ----

def write_status(dep_id, version, state, message=""):
    os.makedirs(os.path.dirname(STATUS), exist_ok=True)
    tmp = STATUS + ".tmp"
    with open(tmp, "w") as f:
        json.dump({"deployment_id": dep_id, "version": version, "state": state, "message": message,
                   "updated_at": datetime.now(timezone.utc).isoformat(timespec="seconds")}, f)
    os.chmod(tmp, 0o644)
    os.replace(tmp, STATUS)
    log(f"status: deployment {dep_id} {version} {state} {message}".rstrip())


def read_last():
    try:
        with open(LAST) as f:
            return int(f.read().strip() or 0)
    except FileNotFoundError:
        return None


def save_last(dep_id):
    with open(LAST, "w") as f:
        f.write(str(dep_id))


def set_env_version(text, version):
    """Returns .env text with LIBREDESK_VERSION set to version."""
    if re.search(r"(?m)^LIBREDESK_VERSION=", text):
        return re.sub(r"(?m)^LIBREDESK_VERSION=.*$", f"LIBREDESK_VERSION={version}", text)
    return text.rstrip("\n") + f"\nLIBREDESK_VERSION={version}\n"


def env_value(text, key):
    m = re.search(rf"(?m)^{re.escape(key)}=(.*)$", text)
    return m.group(1).strip() if m else ""


def sha(path):
    try:
        with open(path, "rb") as f:
            return hashlib.sha256(f.read()).hexdigest()
    except FileNotFoundError:
        return ""


# ---- release files ----

def fetch_deploy_dir(tag, dest):
    """Downloads the tag's source tarball and extracts only its deploy/ directory into dest."""
    url = f"https://codeload.github.com/{REPO}/tar.gz/refs/tags/{tag}"
    req = urllib.request.Request(url, headers={"User-Agent": "encatch-deploy-agent"})
    with urllib.request.urlopen(req, timeout=120) as r:
        data = r.read()
    extract_deploy_dir(data, dest)


def extract_deploy_dir(data, dest):
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as tar:
        members = []
        for m in tar.getmembers():
            parts = m.name.split("/", 2)  # <repo>-<tag>/deploy/...
            if len(parts) < 3 or parts[1] != "deploy" or not parts[2]:
                continue
            m.name = parts[2]
            members.append(m)
        if not members:
            raise DeployError("release has no deploy/ directory")
        tar.extractall(dest, members=members, filter="data")  # "data" refuses links and paths outside dest


def rsync(src, dst):
    cmd = ["rsync", "-a", "--checksum"] + [f"--exclude={k}" for k in KEEP] + [src.rstrip("/") + "/", dst.rstrip("/") + "/"]
    subprocess.run(cmd, check=True)


# ---- deploy ----

def sh(*args, check=True):
    log("$ " + " ".join(args))
    return subprocess.run(args, cwd=BASE, check=check, text=True, capture_output=True)


def compose(*args, check=True):
    return sh("docker", "compose", *args, check=check)


def healthy(tag, desk, support):
    deadline = time.time() + HEALTH_TIMEOUT
    while time.time() < deadline:
        image = compose("ps", "app", "--format", "{{.Image}} {{.State}}", check=False).stdout.strip()
        if image == f"ghcr.io/{REPO}:{tag} running":
            ok = all(sh("curl", "-fsS", "--max-time", "5", f"https://{d}/health", check=False).returncode == 0
                     for d in (desk, support) if d)
            if ok:
                return True
        time.sleep(5)
    return False


def deploy(dep_id, tag, dry_run=False):
    env_path = os.path.join(BASE, ".env")
    with open(env_path) as f:
        env = f.read()
    prev = env_value(env, "LIBREDESK_VERSION")
    desk, support = env_value(env, "DESK_DOMAIN"), env_value(env, "DOMAIN")
    log(f"deploying {tag} (running {prev})")

    with tempfile.TemporaryDirectory() as tmp:
        fetch_deploy_dir(tag, tmp)
        if dry_run:
            log("dry run: release files fetched; stopping before any change")
            return
        write_status(dep_id, tag, "in_progress", "backing up")
        sh("./scripts/backup-db.sh", "incr")

        # Keep the current files for rollback, then install the release's.
        shutil.rmtree(PREVIOUS, ignore_errors=True)
        os.makedirs(PREVIOUS)
        rsync(BASE, PREVIOUS)
        before = (sha(os.path.join(BASE, "Caddyfile")), sha(os.path.join(BASE, "docker-compose.yml")))
        rsync(tmp, BASE)
    caddy_changed = before != (sha(os.path.join(BASE, "Caddyfile")), sha(os.path.join(BASE, "docker-compose.yml")))

    def apply(version, restart_caddy):
        with open(env_path) as f:
            text = f.read()
        with open(env_path, "w") as f:
            f.write(set_env_version(text, version))
        compose("pull", "app")
        compose("up", "-d", "--no-deps", "app")  # never recreates Postgres or Redis
        if restart_caddy:
            compose("up", "-d", "--no-deps", "caddy")  # picks up compose changes (e.g. volumes)
            compose("restart", "caddy")       # re-reads the bind-mounted Caddyfile

    write_status(dep_id, tag, "in_progress", "starting new version")
    try:
        apply(tag, caddy_changed)
        if healthy(tag, desk, support):
            write_status(dep_id, tag, "success", f"running {tag} (was {prev})")
            return
        raise DeployError("new version did not become healthy")
    except (DeployError, subprocess.CalledProcessError) as e:
        log(f"deploy failed: {e}; rolling back to {prev}")
        write_status(dep_id, tag, "in_progress", f"failed ({e}); rolling back to {prev}")
        rsync(PREVIOUS, BASE)
        try:
            apply(prev, caddy_changed)
            back = healthy(prev, desk, support)
        except subprocess.CalledProcessError:
            back = False
        note = f"rolled back to {prev}" if back else f"ROLLBACK TO {prev} ALSO UNHEALTHY - needs attention"
        write_status(dep_id, tag, "failure", f"{e}; {note}")


def main():
    dry_run = "--dry-run" in sys.argv
    os.makedirs(os.path.join(STATE, "public"), exist_ok=True)
    with open(os.path.join(STATE, "lock"), "w") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            log("another run is in progress")
            return
        dep = latest_deployment()
        last = read_last()
        if dep is None:
            if last is None:
                save_last(0)
            return
        if last is None:
            # First run: don't deploy anything approved before the agent was installed.
            log(f"first run: marking deployment {dep['id']} as already handled")
            save_last(dep["id"])
            return
        if dep["id"] <= last:
            return
        try:
            tag = check(dep)
        except DeployError as e:
            write_status(dep["id"], dep.get("ref", ""), "failure", f"refused: {e}")
            save_last(dep["id"])
            return
        if not dry_run:
            save_last(dep["id"])  # before deploying: a crash must not redeploy in a loop
        try:
            deploy(dep["id"], tag, dry_run)
        except Exception as e:  # report anything unexpected instead of leaving "in progress"
            write_status(dep["id"], tag, "failure", f"agent error: {e}")
            raise


if __name__ == "__main__":
    main()
