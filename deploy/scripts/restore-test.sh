#!/usr/bin/env bash
# Prove the pgBackRest backups restore: restore the latest backup plus archived WAL
# into a throwaway container, wait for recovery to finish and query the data.
# Production is not touched. Run monthly by libredesk-restore-test.timer.
set -euo pipefail

cd "$(dirname "$0")/.."
source .env

target=$(mktemp -d /tmp/libredesk-restore-test.XXXX)
trap 'sudo rm -rf "$target"' EXIT
sudo chown 70:70 "$target"

docker run --rm --user 70:70 \
  -e PGBACKREST_PG1_USER="$POSTGRES_USER" \
  -e DB_USER="$POSTGRES_USER" -e DB_NAME="$POSTGRES_DB" \
  -v "$PWD/postgres/pgbackrest.conf:/etc/pgbackrest/pgbackrest.conf:ro" \
  -v "$PWD/backups/pgbackrest:/var/lib/pgbackrest" \
  -v "$target:/var/lib/postgresql/18/docker" \
  encatch/libredesk-postgres:18 sh -euc '
    data=/var/lib/postgresql/18/docker
    # archive-mode=off: the restored copy must never push WAL into the real archive.
    pgbackrest --stanza=libredesk --archive-mode=off --log-level-console=warn restore
    pg_ctl -D "$data" -l /tmp/pg.log -o "-c archive_mode=off" -w -t 300 start >/dev/null
    for _ in $(seq 120); do
      [ "$(psql -U "$DB_USER" -d "$DB_NAME" -Atc "select pg_is_in_recovery()")" = f ] && break
      sleep 1
    done
    result=$(psql -U "$DB_USER" -d "$DB_NAME" -Atc "select pg_is_in_recovery(), (select count(*) from pg_tables where schemaname = current_schema()), (select count(*) from users), (select count(*) from conversations)")
    pg_ctl -D "$data" -m fast stop >/dev/null
    echo "in_recovery|tables|users|conversations = $result"
    case "$result" in f\|0\|*) echo "restore test FAILED: no tables" >&2; exit 1 ;; f\|*) echo "restore test OK" ;; *) echo "restore test FAILED: still in recovery" >&2; tail -20 /tmp/pg.log >&2; exit 1 ;; esac
  '
