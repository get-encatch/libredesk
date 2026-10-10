#!/usr/bin/env bash
# Install or update the libredesk stack in /srv/libredesk. Safe to re-run.
# Run on the server as a sudo-capable user in the docker group:
#   cd /srv/libredesk && ./install.sh            # everything except Caddy
#   cd /srv/libredesk && ./install.sh --with-caddy   # once DNS and ports 80/443 are ready
set -euo pipefail

cd "$(dirname "$0")"
WITH_CADDY=0
[ "${1:-}" = "--with-caddy" ] && WITH_CADDY=1

# The postgres:alpine image runs Postgres (and pgBackRest) as uid/gid 70.
PG_UID=70

echo "== directories"
mkdir -p data/postgres data/uploads data/redis data/caddy/data data/caddy/config \
  backups/pgbackrest backups/dumps backups/restic secrets
chmod 700 secrets
sudo chown -R "$PG_UID:$PG_UID" backups/pgbackrest backups/dumps
chmod +x scripts/*.sh

echo "== secrets"
if [ ! -f .env ]; then
  cp .env.example .env
  sed -i \
    -e "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$(openssl rand -hex 24)/" \
    -e "s/^LIBREDESK_ENCRYPTION_KEY=.*/LIBREDESK_ENCRYPTION_KEY=$(openssl rand -hex 16)/" \
    -e "s/^LIBREDESK_SYSTEM_USER_PASSWORD=.*/LIBREDESK_SYSTEM_USER_PASSWORD=$(openssl rand -base64 18 | tr -d '/+=')Aa1!/" \
    .env
  chmod 600 .env
  echo "created .env with generated secrets"
fi
if [ ! -f secrets/restic-password ]; then
  openssl rand -base64 32 > secrets/restic-password
  chmod 600 secrets/restic-password
  echo "created secrets/restic-password"
fi

if [ ! -f secrets/my-tickets.env ]; then
  # One issuer to start with, for testing; add one line per Encatch backend app.
  echo "LIBREDESK_MY_TICKETS__ISSUERS__ENCATCH_TEST=$(openssl rand -hex 32)" > secrets/my-tickets.env
  chmod 600 secrets/my-tickets.env
  echo "created secrets/my-tickets.env (issuer: encatch-test)"
fi

echo "== containers"
docker compose build --pull db
docker compose pull app redis caddy
if [ "$WITH_CADDY" = 1 ]; then
  docker compose up -d
else
  docker compose up -d app db redis
fi

echo "== waiting for postgres"
for _ in $(seq 1 30); do
  [ "$(docker inspect -f '{{.State.Health.Status}}' libredesk_db)" = healthy ] && break
  sleep 2
done

echo "== pgBackRest"
docker exec -u postgres libredesk_db pgbackrest --stanza=libredesk stanza-create
docker exec -u postgres libredesk_db pgbackrest --stanza=libredesk check
if ! docker exec -u postgres libredesk_db pgbackrest --stanza=libredesk info --output=json | grep -q '"label"'; then
  echo "no backups yet, taking the first full backup"
  scripts/backup-db.sh full
fi

echo "== backup timers"
sudo cp systemd/libredesk-* /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now \
  libredesk-backup-db-full.timer libredesk-backup-db-diff.timer libredesk-backup-db-incr.timer \
  libredesk-dump-db.timer libredesk-backup-uploads.timer libredesk-restore-test.timer

echo "== done"
docker compose ps
