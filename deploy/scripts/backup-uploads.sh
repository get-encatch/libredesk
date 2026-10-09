#!/usr/bin/env bash
# Back up uploaded attachments (data/uploads) with restic, keeping 31 days of
# snapshots. The repository is encrypted with secrets/restic-password: losing that
# file makes these backups unreadable, so keep a copy in a password manager.
set -euo pipefail

cd "$(dirname "$0")/.."
RESTIC_IMAGE=restic/restic:0.18.1

restic() {
  docker run --rm \
    -v "$PWD/data/uploads:/data/uploads:ro" \
    -v "$PWD/backups/restic:/repo" \
    -v "$PWD/secrets/restic-password:/run/restic-password:ro" \
    -e RESTIC_REPOSITORY=/repo \
    -e RESTIC_PASSWORD_FILE=/run/restic-password \
    --hostname libredesk \
    "$RESTIC_IMAGE" "$@"
}

if [ ! -f backups/restic/config ]; then
  restic init
fi

restic backup --tag uploads /data/uploads
restic forget --tag uploads --keep-within 31d --prune
