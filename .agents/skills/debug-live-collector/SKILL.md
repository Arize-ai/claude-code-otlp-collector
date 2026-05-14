---
name: debug-live-collector
description: Use when testing an already-running Claude collector by sending a live marker trace to its configured OTLP endpoint without starting a second collector.
---

# Debug Live Collector

Use this skill when the collector is already running and you want to test the live process, port, environment, forwarding, and logs. This is different from `debug-setup`, which starts an isolated collector for deterministic local-file debugging.

## 1. Resolve And Check The Live Collector

Read `.env` for the configured listener, but do not print secrets.

```bash
set -a
source .env
set +a

listen="${CLAUDE_COLLECTOR_LISTEN:-:14318}"
export CLAUDE_COLLECTOR_LIVE_PORT="${listen##*:}"

echo "CLAUDE_COLLECTOR_LISTEN=${listen}"
curl -fsS "http://localhost:${CLAUDE_COLLECTOR_LIVE_PORT}/healthz"
lsof -nP -iTCP:"${CLAUDE_COLLECTOR_LIVE_PORT}" -sTCP:LISTEN
```

Success means the live collector is reachable and returned `ok`.

If this fails, do not continue with a live marker test. Use `debug-setup` to start an isolated collector, or start the live collector first.

## 2. Confirm Destination Env Shape

Show the target Space ID and fully redact the API key.

```bash
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
```

This checks the current shell’s `.env`, which should match the live collector setup. It cannot prove the already-running process has the same env unless you also inspect its launch configuration or logs.

## 3. Send A Live Marker Trace

Send a small Claude Code prompt to the live collector port. This will send real telemetry wherever the live collector forwards it.

```bash
export DEBUG_WORD="debug-live-$(date +%s)"
export CLAUDE_COLLECTOR_LIVE_PORT="${CLAUDE_COLLECTOR_LIVE_PORT:-14318}"
export CLAUDE_CODE_BODY_DIR="${CLAUDE_CODE_BODY_DIR:-/tmp/claude-code-otel-bodies}"

printf '%s\n' "$DEBUG_WORD" > /tmp/claude-collector-live-marker.txt

printf 'Use Bash to echo %s, then answer exactly with the echoed value.\n' "$DEBUG_WORD" | \
CLAUDE_CODE_ENABLE_TELEMETRY=1 \
CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1 \
ENABLE_ENHANCED_TELEMETRY_BETA=1 \
OTEL_TRACES_EXPORTER=otlp \
OTEL_LOGS_EXPORTER=otlp \
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf \
OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=http/protobuf \
OTEL_EXPORTER_OTLP_LOGS_PROTOCOL=http/protobuf \
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT="http://localhost:${CLAUDE_COLLECTOR_LIVE_PORT}/v1/traces" \
OTEL_EXPORTER_OTLP_LOGS_ENDPOINT="http://localhost:${CLAUDE_COLLECTOR_LIVE_PORT}/v1/logs" \
OTEL_LOG_USER_PROMPTS=1 \
OTEL_LOG_TOOL_DETAILS=1 \
OTEL_LOG_TOOL_CONTENT=1 \
OTEL_LOG_RAW_API_BODIES="file:${CLAUDE_CODE_BODY_DIR}" \
claude -p --model haiku --permission-mode bypassPermissions --allowedTools Bash
```

The command should print the marker, for example `debug-live-1778739341`.

## 4. Inspect Local Export File If Enabled

If the live collector has `CLAUDE_COLLECTOR_EXPORT_FILE` configured, inspect that file for the marker.

```bash
export DEBUG_WORD="$(cat /tmp/claude-collector-live-marker.txt)"
export LIVE_EXPORT_FILE="${CLAUDE_COLLECTOR_EXPORT_FILE:-/tmp/claude-collector-latest.jsonl}"

test -s "$LIVE_EXPORT_FILE"

jq -r --arg word "$DEBUG_WORD" '
  select(.signal == "traces") |
  .spans[] |
  select(
    ((.attributes["input.value"] // "") | contains($word)) or
    ((.attributes["output.value"] // "") | contains($word))
  ) |
  [.name, .status, .attributes["openinference.span.kind"], .attributes["llm.model_name"], (.attributes["tool.name"] // ""), .attributes["input.value"], .attributes["output.value"]] | @tsv
' "$LIVE_EXPORT_FILE"
```

Success means the live collector received and transformed the marker trace locally.

If this file is missing or does not contain the marker, that does not automatically mean live forwarding failed. The live collector may have been started without local export enabled, or with a different export file path.

## 5. Inspect Live Collector Logs

Look for upstream feedback in the live collector logs:

```bash
grep -E 'forwarded telemetry|downstream rejected telemetry|collector stopped|failed to write|error=' /tmp/claude-collector-destination-debug.log
```

Adjust the log path to match the way the live collector is launched. For a LaunchAgent, check its configured stderr/stdout paths.

Success means logs include:

```text
forwarded telemetry path=/v1/traces status=<2xx>
```

Common failures:

- `401` or `403`: credentials or Space ID are wrong.
- `404`: endpoint path is wrong.
- `429`: rate limited.
- `5xx`: destination accepted the connection but failed server-side.

## Result Interpretation

- Health passes + local export contains marker + logs show `status=200`: live collector is working end to end.
- Health passes + no local export marker + logs show `status=200`: live forwarding works, but local export is unavailable or pointed somewhere else.
- Health passes + local export contains marker + logs show downstream rejection: collector mapping works; fix destination auth, endpoint, or service issue.
- Health fails: this is not a live-collector problem yet; start the collector or use `debug-setup`.

## Safety Notes

- This skill sends telemetry through the live collector and may send data to Arize.
- It is okay to print `ARIZE_SPACE_ID` during setup debugging.
- Never print `ARIZE_API_KEY`, `OTEL_EXPORTER_OTLP_HEADERS`, or `CLAUDE_COLLECTOR_EXPORTER_HEADERS`.
- Do not stop or restart the live collector unless the user explicitly asks.
