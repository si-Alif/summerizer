#!/usr/bin/env sh
set -e

DSN="${SUMMERIZER_DB_DSN:-$DATABASE_URL}"

if [ -z "$DSN" ]; then
  echo "missing database DSN; set SUMMERIZER_DB_DSN or DATABASE_URL" >&2
  exit 1
fi

echo "running database migrations..."
/usr/local/bin/migrate -path /app/migrations -database "$DSN" up

echo "starting api"
exec /app/api "$@"
