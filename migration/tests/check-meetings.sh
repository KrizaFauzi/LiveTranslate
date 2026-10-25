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
    # The probe uses TCP because the bootstrap server of the entrypoint listens on the socket
    # only, so the probe can only reach the server that stays up for the check.
    until psql -h 127.0.0.1 -U postgres -tAc "select 1" >/dev/null 2>&1; do
      attempt=$((attempt+1))
      [ "$attempt" -gt 30 ] && { cat /tmp/postgres-check.log; exit 1; }
      sleep 1
    done
    psql -h 127.0.0.1 -U postgres -v ON_ERROR_STOP=1 -f /migrations/tests/run_all.sql
    sh /migrations/tests/transcript_concurrency.sh
  ' < /dev/null
