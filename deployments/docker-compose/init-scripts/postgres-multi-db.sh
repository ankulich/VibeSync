#!/bin/bash
# Create one logical database per VibeSync microservice on first boot.
# Run by Postgres' docker-entrypoint-initdb.d hook. See docker-compose.yml.
set -euo pipefail

# The init role; matches POSTGRES_USER in the compose env.
POSTGRES_USER="${POSTGRES_USER:-vibesync}"
# Comma- or newline-separated list of database names.
DBS="${POSTGRES_MULTIPLE_DATABASES:-}"

if [[ -z "${DBS}" ]]; then
  echo "postgres-multi-db: POSTGRES_MULTIPLE_DATABASES empty; skipping."
  exit 0
fi

psql -v ON_ERROR_STOP=1 --username "${POSTGRES_USER}" <<-EOSQL
$(echo "${DBS}" | tr ',' '\n' | while read -r db; do
  if [[ -n "${db}" ]]; then
    # Quote the identifier: service names like "user" are reserved words in
    # SQL and CREATE DATABASE user is a syntax error without quotes.
    echo "SELECT 'CREATE DATABASE \"${db}\"' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${db}')\\gexec"
    echo "GRANT ALL PRIVILEGES ON DATABASE \"${db}\" TO ${POSTGRES_USER};"
  fi
done)
EOSQL

echo "postgres-multi-db: created databases: $(echo "${DBS}" | tr '\n' ' ')"
