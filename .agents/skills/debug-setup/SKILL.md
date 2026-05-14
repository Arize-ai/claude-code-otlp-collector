---
name: debug-setup
description: Use when validating Claude Code telemetry setup for this collector, especially to confirm Claude emits OTLP data to a local file before testing the configured upstream destination.
---

# Debug Setup

Use this skill to validate the full setup without touching any live collector. Always prove the local Claude -> collector -> JSONL path first, then start a fresh debug collector on a separate port and prove authenticated OTLP forwarding to Arize.

## 0. Claude Settings Env Conflict Check

Before running any marker prompts, make sure Claude settings are not pinning telemetry env vars. Values set in Claude `settings.json` / `settings.local.json` can override or conflict with local `.env` and shell exports.

```bash
keys='
CLAUDE_CODE_ENABLE_TELEMETRY
CLAUDE_CODE_ENHANCED_TELEMETRY_BETA
ENABLE_ENHANCED_TELEMETRY_BETA
OTEL_TRACES_EXPORTER
OTEL_LOGS_EXPORTER
OTEL_EXPORTER_OTLP_PROTOCOL
OTEL_EXPORTER_OTLP_TRACES_PROTOCOL
OTEL_EXPORTER_OTLP_LOGS_PROTOCOL
OTEL_EXPORTER_OTLP_ENDPOINT
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT
OTEL_EXPORTER_OTLP_LOGS_ENDPOINT
OTEL_EXPORTER_OTLP_HEADERS
OTEL_RESOURCE_ATTRIBUTES
OTEL_LOG_USER_PROMPTS
OTEL_LOG_TOOL_DETAILS
OTEL_LOG_TOOL_CONTENT
OTEL_LOG_RAW_API_BODIES
'

settings_files="
$HOME/.claude/settings.json
$HOME/.claude/settings.local.json
$PWD/.claude/settings.json
$PWD/.claude/settings.local.json
"

found=0
for file in $settings_files; do
  [ -f "$file" ] || continue
  for key in $keys; do
    if jq -r --arg key "$key" 'paths | select(.[-1]? == $key) | join(".")' "$file" 2>/dev/null | grep -q .; then
      echo "Claude settings env conflict: ${file} contains ${key}"
      found=1
    fi
  done
done

if [ "$found" -ne 0 ]; then
  echo "Remove these telemetry env keys from Claude settings before continuing; debug-setup sets them explicitly for each test."
  exit 1
fi

echo "Claude settings env conflict check passed."
```

This check intentionally reports only file paths and key names. It must not print configured values.

## 1. Local File Smoke Test

Run the collector with an export file and no upstream exporter. Prefer a debug port so you do not disturb a running LaunchAgent on `14318`.

```bash
export CLAUDE_COLLECTOR_DEBUG_PORT="${CLAUDE_COLLECTOR_DEBUG_PORT:-14319}"
export CLAUDE_COLLECTOR_DEBUG_FILE="${CLAUDE_COLLECTOR_DEBUG_FILE:-/tmp/claude-collector-debug.jsonl}"
export CLAUDE_CODE_BODY_DIR="${CLAUDE_CODE_BODY_DIR:-/tmp/claude-code-otel-bodies}"

mkdir -p "$CLAUDE_CODE_BODY_DIR"
rm -f "$CLAUDE_COLLECTOR_DEBUG_FILE"

go build -o bin/claude-collector ./cmd/claude-collector
./bin/claude-collector \
  --listen ":${CLAUDE_COLLECTOR_DEBUG_PORT}" \
  --exporter-endpoint none \
  --export-file "$CLAUDE_COLLECTOR_DEBUG_FILE" \
  --allow-body-ref \
  --forward-logs=false
```

Keep that process running. In another terminal, verify the collector is reachable:

```bash
curl -fsS "http://localhost:${CLAUDE_COLLECTOR_DEBUG_PORT}/healthz"
```

Send a small Claude request with a unique marker. Use a Bash tool call so Claude Code emits the interaction/tool/LLM trace spans, not only API log events:

```bash
export DEBUG_WORD="debug-setup-$(date +%s)"

printf 'Use Bash to echo %s, then answer exactly with the echoed value.\n' "$DEBUG_WORD" | \
CLAUDE_CODE_ENABLE_TELEMETRY=1 \
CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1 \
ENABLE_ENHANCED_TELEMETRY_BETA=1 \
OTEL_TRACES_EXPORTER=otlp \
OTEL_LOGS_EXPORTER=otlp \
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf \
OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=http/protobuf \
OTEL_EXPORTER_OTLP_LOGS_PROTOCOL=http/protobuf \
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT="http://localhost:${CLAUDE_COLLECTOR_DEBUG_PORT}/v1/traces" \
OTEL_EXPORTER_OTLP_LOGS_ENDPOINT="http://localhost:${CLAUDE_COLLECTOR_DEBUG_PORT}/v1/logs" \
OTEL_LOG_USER_PROMPTS=1 \
OTEL_LOG_TOOL_DETAILS=1 \
OTEL_LOG_TOOL_CONTENT=1 \
OTEL_LOG_RAW_API_BODIES="file:${CLAUDE_CODE_BODY_DIR}" \
claude -p --model haiku --permission-mode bypassPermissions --allowedTools Bash
```

Confirm the JSONL file contains the marker and OpenInference fields:

```bash
test -s "$CLAUDE_COLLECTOR_DEBUG_FILE"

jq -r --arg word "$DEBUG_WORD" '
  select(.signal == "traces") |
  .spans[] |
  select(
    ((.attributes["input.value"] // "") | contains($word)) or
    ((.attributes["output.value"] // "") | contains($word))
  ) |
  [
    .name,
    (.status | if type == "object" then .code else . end),
    .attributes["openinference.span.kind"],
    .attributes["llm.model_name"],
    .attributes["input.value"],
    .attributes["output.value"]
  ] | @tsv
' "$CLAUDE_COLLECTOR_DEBUG_FILE"
```

Success means:

- The file exists and is non-empty.
- At least one `claude_code.llm_request` span appears.
- At least one `claude_code.tool` span appears when using the Bash tool prompt.
- `openinference.span.kind` is `LLM`.
- `input.value` or `output.value` contains the unique marker.
- `llm.model_name` is present and does not include Claude Code terminal suffixes like `[1m]`.
- Successful spans have status `STATUS_CODE_OK`.

If the file is empty, fix the local path before testing Arize or any upstream destination. Check the health endpoint, OTLP endpoint env vars, and whether another collector is already bound to the same port.

Stop this local-only collector before continuing to the authenticated full-path test. The next step reuses the same debug port with Arize forwarding enabled.

## 2. Destination Connectivity Check

After local file output works, check the configured destination from `.env`. It is safe to print the Space ID for confirmation, but never print API keys or authorization headers.

```bash
set -a
source .env
set +a

missing=0
for var in ARIZE_SPACE_ID ARIZE_API_KEY; do
  eval "value=\${${var}:-}"
  if [ -z "$value" ]; then
    echo "${var}=missing"
    missing=1
  elif [ "$var" = "ARIZE_SPACE_ID" ]; then
    echo "${var}=${value}"
  else
    echo "${var}=***"
  fi
done

if [ "$missing" -ne 0 ]; then
  echo "Missing required Arize destination environment variables."
  exit 1
fi

endpoint="${ARIZE_OTLP_ENDPOINT:-${CLAUDE_COLLECTOR_EXPORTER_ENDPOINT:-https://otlp.arize.com/v1/traces}}"

curl -sS -o /dev/null \
  -w 'http_code=%{http_code} remote_ip=%{remote_ip} time_connect=%{time_connect} time_tls=%{time_appconnect}\n' \
  --connect-timeout 5 \
  "$endpoint"
```

This is a connectivity probe, not a full authenticated ingest test. Any HTTP response with a real `remote_ip` proves DNS, TCP, and TLS are working. `http_code=000`, DNS errors, connection refused, or TLS failures mean the collector cannot reach the destination.

The env-var preflight prints `ARIZE_SPACE_ID` so users can confirm the target space. It must only print `ARIZE_API_KEY=***`, never the key value or a partial key.

## 3. Required Full-Path OTLP Send Check

After the connectivity probe passes, run an authenticated OTLP send using a fresh debug collector on the separate debug port. This is required for `debug-setup`: it proves Claude emits telemetry, the collector maps it, the local JSONL export records it, and Arize accepts the forwarded trace.

In one terminal, start the collector with Arize enabled and logs captured:

```bash
export CLAUDE_COLLECTOR_DEBUG_PORT="${CLAUDE_COLLECTOR_DEBUG_PORT:-14319}"
export CLAUDE_COLLECTOR_DEST_FILE="${CLAUDE_COLLECTOR_DEST_FILE:-/tmp/claude-collector-destination-debug.jsonl}"
export CLAUDE_COLLECTOR_DEST_LOG="${CLAUDE_COLLECTOR_DEST_LOG:-/tmp/claude-collector-destination-debug.log}"

rm -f "$CLAUDE_COLLECTOR_DEST_FILE" "$CLAUDE_COLLECTOR_DEST_LOG"

set -a
source .env
set +a

go build -o bin/claude-collector ./cmd/claude-collector

export CLAUDE_COLLECTOR_EXPORTER_HEADERS="space_id=${ARIZE_SPACE_ID},api_key=${ARIZE_API_KEY}"

./bin/claude-collector \
  --listen ":${CLAUDE_COLLECTOR_DEBUG_PORT}" \
  --exporter-endpoint "${ARIZE_OTLP_ENDPOINT:-https://otlp.arize.com/v1/traces}" \
  --project-name "${ARIZE_PROJECT_NAME:-claude-oltp-direct}" \
  --allow-body-ref="${CLAUDE_COLLECTOR_ALLOW_BODY_REF:-false}" \
  --body-ref-cleanup-roots "${CLAUDE_COLLECTOR_BODY_REF_CLEANUP_ROOTS:-}" \
  --body-ref-cleanup-ttl "${CLAUDE_COLLECTOR_BODY_REF_CLEANUP_TTL:-1h}" \
  --body-ref-cleanup-suffixes "${CLAUDE_COLLECTOR_BODY_REF_CLEANUP_SUFFIXES:-.request.json,.response.json}" \
  --export-file "$CLAUDE_COLLECTOR_DEST_FILE" \
  --forward-logs=false 2>&1 | tee "$CLAUDE_COLLECTOR_DEST_LOG"
```

Use the direct binary command here so the debug `--listen` port wins even when `.env` contains `CLAUDE_COLLECTOR_LISTEN`.

In another terminal, send the same tool-call marker request to that port:

```bash
export CLAUDE_COLLECTOR_DEBUG_PORT="${CLAUDE_COLLECTOR_DEBUG_PORT:-14319}"
export CLAUDE_CODE_BODY_DIR="${CLAUDE_CODE_BODY_DIR:-/tmp/claude-code-otel-bodies}"
export DEBUG_WORD="debug-arize-$(date +%s)"

printf 'Use Bash to echo %s, then answer exactly with the echoed value.\n' "$DEBUG_WORD" | \
CLAUDE_CODE_ENABLE_TELEMETRY=1 \
CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1 \
ENABLE_ENHANCED_TELEMETRY_BETA=1 \
OTEL_TRACES_EXPORTER=otlp \
OTEL_LOGS_EXPORTER=otlp \
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf \
OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=http/protobuf \
OTEL_EXPORTER_OTLP_LOGS_PROTOCOL=http/protobuf \
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT="http://localhost:${CLAUDE_COLLECTOR_DEBUG_PORT}/v1/traces" \
OTEL_EXPORTER_OTLP_LOGS_ENDPOINT="http://localhost:${CLAUDE_COLLECTOR_DEBUG_PORT}/v1/logs" \
OTEL_LOG_USER_PROMPTS=1 \
OTEL_LOG_TOOL_DETAILS=1 \
OTEL_LOG_TOOL_CONTENT=1 \
OTEL_LOG_RAW_API_BODIES="file:${CLAUDE_CODE_BODY_DIR}" \
claude -p --model haiku --permission-mode bypassPermissions --allowedTools Bash
```

Check the upstream result:

```bash
export CLAUDE_COLLECTOR_DEST_LOG="${CLAUDE_COLLECTOR_DEST_LOG:-/tmp/claude-collector-destination-debug.log}"

grep -E 'forwarded telemetry|downstream rejected telemetry|collector stopped|failed to write|error=' "$CLAUDE_COLLECTOR_DEST_LOG"
```

Success means the logs include `forwarded telemetry path=/v1/traces status=<2xx>`, preferably `status=200`. A line like `downstream rejected telemetry path=/v1/traces status=401` or `403` usually means credentials or Space ID are wrong. `404` usually means the endpoint path is wrong. `429` means rate limiting. `5xx` means the destination accepted the connection but failed server-side.

Also confirm the local file contains the same marker:

```bash
export CLAUDE_COLLECTOR_DEST_FILE="${CLAUDE_COLLECTOR_DEST_FILE:-/tmp/claude-collector-destination-debug.jsonl}"

jq -r --arg word "$DEBUG_WORD" '
  select(.signal == "traces") |
  .spans[] |
  select(
    ((.attributes["input.value"] // "") | contains($word)) or
    ((.attributes["output.value"] // "") | contains($word))
  ) |
  [.name, .status, .attributes["openinference.span.kind"], .attributes["input.value"], .attributes["output.value"]] | @tsv
' "$CLAUDE_COLLECTOR_DEST_FILE"
```

Full-path success requires both checks to pass:

- Local JSONL contains the marker on `TOOL` and `LLM` spans.
- Collector logs show `forwarded telemetry path=/v1/traces status=<2xx>`.

If the local JSONL file has the marker but the logs show a downstream rejection, the collector mapping is working and the problem is upstream auth, endpoint, or service availability. If the local JSONL file does not have the marker, go back to the local file smoke test.

## Safety Notes

- It is okay to print `ARIZE_SPACE_ID` during setup debugging.
- Never paste or print `ARIZE_API_KEY`, `OTEL_EXPORTER_OTLP_HEADERS`, or `CLAUDE_COLLECTOR_EXPORTER_HEADERS`.
- Keep debug files under `/tmp` unless the user asks for another path.
- Do not delete `.env`.
- `debug-setup` should start its own debug collector; use `debug-live-collector` only when the user explicitly wants to test an already-running collector.
