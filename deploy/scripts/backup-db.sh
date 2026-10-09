#!/usr/bin/env bash
# Physical Postgres backup with pgBackRest. Usage: backup-db.sh full|diff|incr
# Run by systemd timers (libredesk-backup-db-*.timer). Expiry of old backups
# (repo1-retention-full) runs automatically after each backup.
set -euo pipefail

type="${1:?usage: backup-db.sh full|diff|incr}"
case "$type" in full|diff|incr) ;; *) echo "invalid type: $type" >&2; exit 2 ;; esac

docker exec -u postgres libredesk_db pgbackrest --stanza=libredesk --type="$type" backup
