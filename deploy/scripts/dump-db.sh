#!/usr/bin/env bash
# Nightly logical dump (pg_dump custom format), kept for 7 days. Portable copy for
# quick inspection or moving to another server; pgBackRest is the primary backup.
set -euo pipefail

cd "$(dirname "$0")/.."
source .env

file="libredesk-$(date -u +%Y%m%dT%H%M%SZ).dump"
docker exec -u postgres libredesk_db pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc -f "/backups/dumps/$file"
echo "wrote backups/dumps/$file"

find backups/dumps -name 'libredesk-*.dump' -mtime +7 -print -delete
