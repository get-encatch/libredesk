#!/usr/bin/env bash
# Rebuild ../static/app.css from the templates. Run after changing templates or app.css.
set -euo pipefail
cd "$(dirname "$0")"
npx --yes tailwindcss@3.4.17 -c tailwind.config.cjs -i app.css -o ../static/app.css --minify
