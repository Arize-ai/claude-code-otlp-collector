#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [[ ! -f .env ]]; then
  echo "Missing .env. Create one with ARIZE_API_KEY, ARIZE_SPACE_ID, and ARIZE_PROJECT_NAME." >&2
  exit 1
fi

set -a
# shellcheck disable=SC1091
source .env
set +a

: "${ARIZE_API_KEY:?ARIZE_API_KEY is required in .env}"
: "${ARIZE_SPACE_ID:?ARIZE_SPACE_ID is required in .env}"

export CLAUDE_COLLECTOR_EXPORTER_HEADERS="space_id=${ARIZE_SPACE_ID},api_key=${ARIZE_API_KEY}"

exec go run ./cmd/claude-collector \
  --listen "${CLAUDE_COLLECTOR_LISTEN:-:14318}" \
  --exporter-endpoint "${ARIZE_OTLP_ENDPOINT:-https://otlp.arize.com/v1/traces}" \
  --project-name "${ARIZE_PROJECT_NAME:-claude-oltp-direct}" \
  --project-from-resource-attribute "${CLAUDE_COLLECTOR_PROJECT_FROM_RESOURCE_ATTRIBUTE:-}" \
  --allow-body-ref="${CLAUDE_COLLECTOR_ALLOW_BODY_REF:-false}" \
  --body-ref-cleanup-roots "${CLAUDE_COLLECTOR_BODY_REF_CLEANUP_ROOTS:-}" \
  --body-ref-cleanup-ttl "${CLAUDE_COLLECTOR_BODY_REF_CLEANUP_TTL:-1h}" \
  --body-ref-cleanup-suffixes "${CLAUDE_COLLECTOR_BODY_REF_CLEANUP_SUFFIXES:-.request.json,.response.json}" \
  --forward-logs=false
