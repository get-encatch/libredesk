#!/usr/bin/env bash
# Show backup state: pgBackRest backups and WAL archive, dumps, restic snapshots,
# disk usage and the backup timers' last/next runs.
set -uo pipefail

cd "$(dirname "$0")/.."

echo "== pgBackRest"
docker exec -u postgres libredesk_db pgbackrest --stanza=libredesk info

echo; echo "== pg_dump files"
ls -lh backups/dumps 2>/dev/null | tail -n +2

echo; echo "== restic snapshots (uploads)"
docker run --rm \
  -v "$PWD/backups/restic:/repo" \
  -v "$PWD/secrets/restic-password:/run/restic-password:ro" \
  -e RESTIC_REPOSITORY=/repo -e RESTIC_PASSWORD_FILE=/run/restic-password \
  restic/restic:0.18.1 snapshots --compact

echo; echo "== disk usage"
sudo du -sh data/postgres data/uploads backups/* 2>/dev/null
df -h . | tail -1

echo; echo "== timers"
systemctl list-timers 'libredesk-*' --all --no-pager
