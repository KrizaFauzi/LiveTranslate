#!/bin/sh
# Runs only against an isolated throwaway PostgreSQL container; never reads .env.
set -eu
migration_directory=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
docker run --rm -i --network none --tmpfs /var/lib/postgresql \
  -e POSTGRES_HOST_AUTH_METHOD=trust -v "$migration_directory:/migrations:ro" \
  postgres:18-alpine sh -ec '
    docker-entrypoint.sh postgres > /tmp/postgres-check.log 2>&1 &
    database_pid=$!
    trap "kill $database_pid 2>/dev/null || true" EXIT
    attempt=0
    # pg_isready can succeed against the bootstrap server; require a real connection instead.
    until psql -U postgres -tAc "select 1" >/dev/null 2>&1; do
      attempt=$((attempt+1))
      [ "$attempt" -gt 30 ] && { cat /tmp/postgres-check.log; exit 1; }
      sleep 1
    done
    psql -U postgres -v ON_ERROR_STOP=1 -f /migrations/tests/run_all.sql
  ' < /dev/null
