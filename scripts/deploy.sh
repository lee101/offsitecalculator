#!/usr/bin/env bash
# Publish to <slug>.app.nz: static front end on the CDN, Go backend as a server app.
# Needs the app CLI (curl https://app.nz/cli.sh | sh) and `app auth login`.
set -euo pipefail
cd "$(dirname "$0")/.."
set -a; [ -f .env ] && . ./.env; set +a

rm -rf dist-app && mkdir -p dist-app
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist-app/offsite-server ./cmd/offsite
cp -r web dist-app/web
cp appnz.yaml dist-app/

env_args=()
for k in APPNZ_CLIENT_ID SESSION_SECRET GOOGLE_MAPS_API_KEY GOOGLE_MAPS_BROWSER_KEY OFFSITE_MODEL; do
  [ -n "${!k:-}" ] && env_args+=(--env "$k=${!k}")
done
app apps deploy dist-app "${env_args[@]}"
# Static files are published separately so older app.nz servers (which only publish `output` for static apps) serve the page too.
app sites deploy offsitecalculator dist-app/web
