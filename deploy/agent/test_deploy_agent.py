"""Tests for deploy_agent.py: python3 -m unittest deploy/agent/test_deploy_agent.py"""
import io
import os
import sys
import tarfile
import tempfile
import time
import unittest

sys.path.insert(0, os.path.dirname(__file__))
import deploy_agent as da  # noqa: E402


def dep(**over):
    d = {"id": 5, "ref": "v2.8.0-encatch.5", "task": "deploy:encatch", "environment": "production",
         "creator": {"login": "github-actions[bot]"}, "payload": {"version": "v2.8.0-encatch.5"},
         "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}
    d.update(over)
    return d


class Check(unittest.TestCase):
    def test_accepts_workflow_deployment(self):
        self.assertEqual(da.check(dep()), "v2.8.0-encatch.5")
        self.assertEqual(da.check(dep(payload='{"version": "v2.8.0-encatch.5"}')), "v2.8.0-encatch.5")

    def test_refuses(self):
        for name, d in {
            "other creator": dep(creator={"login": "someone"}),
            "branch ref": dep(ref="encatch/main", payload={"version": "encatch/main"}),
            "upstream tag": dep(ref="v2.8.0", payload={"version": "v2.8.0"}),
            "shell in ref": dep(ref="v2.8.0-encatch.5;rm -rf /", payload={"version": "v2.8.0-encatch.5;rm -rf /"}),
            "payload mismatch": dep(payload={"version": "v2.8.0-encatch.4"}),
            "other task": dep(task="deploy"),
            "staging": dep(environment="staging"),
            "too old": dep(created_at="2026-01-01T00:00:00Z"),
        }.items():
            with self.subTest(name), self.assertRaises(da.DeployError):
                da.check(d)


class Env(unittest.TestCase):
    def test_set_version(self):
        self.assertEqual(da.set_env_version("A=1\nLIBREDESK_VERSION=v1\nB=2\n", "v2"), "A=1\nLIBREDESK_VERSION=v2\nB=2\n")
        self.assertEqual(da.set_env_version("A=1\n", "v2"), "A=1\nLIBREDESK_VERSION=v2\n")
        self.assertEqual(da.env_value("DOMAIN=support.x\nDESK_DOMAIN=desk.x\n", "DOMAIN"), "support.x")


def tarball(entries):
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as t:
        for name, data, kind in entries:
            info = tarfile.TarInfo(name)
            if kind == "link":
                info.type, info.linkname = tarfile.SYMTYPE, data
                t.addfile(info)
            else:
                info.size = len(data)
                t.addfile(info, io.BytesIO(data))
    return buf.getvalue()


class Extract(unittest.TestCase):
    def test_only_deploy_dir(self):
        data = tarball([("libredesk-v1/deploy/Caddyfile", b"caddy", "file"),
                        ("libredesk-v1/deploy/agent/deploy_agent.py", b"x", "file"),
                        ("libredesk-v1/cmd/main.go", b"go", "file")])
        with tempfile.TemporaryDirectory() as d:
            da.extract_deploy_dir(data, d)
            self.assertTrue(os.path.isfile(os.path.join(d, "Caddyfile")))
            self.assertTrue(os.path.isfile(os.path.join(d, "agent", "deploy_agent.py")))
            self.assertFalse(os.path.exists(os.path.join(d, "main.go")))

    def test_refuses_escapes(self):
        for entries in ([("libredesk-v1/deploy/../../etc/x", b"x", "file")],
                        [("libredesk-v1/deploy/evil", "/etc/passwd", "link")]):
            with tempfile.TemporaryDirectory() as d, self.subTest(entries[0][0]):
                with self.assertRaises(Exception):
                    da.extract_deploy_dir(tarball(entries), d)

    def test_no_deploy_dir(self):
        with tempfile.TemporaryDirectory() as d, self.assertRaises(da.DeployError):
            da.extract_deploy_dir(tarball([("libredesk-v1/README.md", b"x", "file")]), d)


class InstallFiles(unittest.TestCase):
    def test_keeps_dst_mode_and_owner(self):
        with tempfile.TemporaryDirectory() as src, tempfile.TemporaryDirectory() as parent:
            dst = os.path.join(parent, "srv")
            os.makedirs(os.path.join(dst, "data"))
            with open(os.path.join(dst, ".env"), "w") as f:
                f.write("LIBREDESK_VERSION=v1\n")
            os.chmod(dst, 0o755)
            owner = (65534, 65534) if os.geteuid() == 0 else (os.getuid(), os.getgid())
            os.chown(dst, *owner)
            # The release: a 0700 temp dir (as tempfile makes) with a file, a script and a subdir.
            os.makedirs(os.path.join(src, "scripts"))
            with open(os.path.join(src, "Caddyfile"), "w") as f:
                f.write("new")
            with open(os.path.join(src, "scripts", "backup.sh"), "w") as f:
                f.write("#!/bin/sh")
            os.chmod(os.path.join(src, "scripts", "backup.sh"), 0o755)
            with open(os.path.join(src, ".env"), "w") as f:  # must never overwrite the server's .env
                f.write("LIBREDESK_VERSION=evil\n")
            os.chmod(src, 0o700)

            da.install_files(src, dst)

            self.assertEqual(os.stat(dst).st_mode & 0o777, 0o755)
            with open(os.path.join(dst, ".env")) as f:
                self.assertEqual(f.read(), "LIBREDESK_VERSION=v1\n")
            self.assertTrue(os.path.isdir(os.path.join(dst, "data")))
            self.assertEqual(os.stat(os.path.join(dst, "scripts", "backup.sh")).st_mode & 0o777, 0o755)
            for p in [dst, os.path.join(dst, "Caddyfile"), os.path.join(dst, "scripts"), os.path.join(dst, "scripts", "backup.sh")]:
                st = os.stat(p)
                self.assertEqual((st.st_uid, st.st_gid), owner, p)


if __name__ == "__main__":
    unittest.main()
