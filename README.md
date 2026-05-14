# Claude Collector

`claude-collector` is an OTLP/HTTP bridge for Claude Code telemetry. It receives Claude Code native OpenTelemetry traces/logs, adds OpenInference/Arize-compatible attributes for LLM and TOOL spans, and forwards the result to Arize or another OTLP/HTTP destination.

It preserves Claude Code's native trace tree:

```text
claude_code.interaction       -> openinference.span.kind=AGENT
├── claude_code.llm_request   -> openinference.span.kind=LLM
└── claude_code.tool          -> openinference.span.kind=TOOL
```

## Start With Debug Setup

The recommended first step is to run the repo-local Claude skill:

```text
Use the debug-setup skill.
```

The skill lives at:

```text
.agents/skills/debug-setup/SKILL.md
```

`debug-setup` does the full validation path:

1. Checks Claude settings files for telemetry env vars that could override local exports.
2. Starts an isolated local collector on a debug port.
3. Sends a Claude marker prompt.
4. Verifies local JSONL output contains mapped `TOOL` and `LLM` spans.
5. Starts a fresh debug collector with Arize forwarding enabled.
6. Sends another marker prompt.
7. Verifies both local JSONL output and upstream OTLP success, normally:

```text
forwarded telemetry path=/v1/traces status=200
```

For testing an already-running collector instead of starting an isolated debug collector, use:

```text
Use the debug-live-collector skill.
```

## Required `.env`

Create a local `.env` file:

```bash
ARIZE_API_KEY=...
ARIZE_SPACE_ID=...
ARIZE_PROJECT_NAME=claude-oltp-direct
ARIZE_OTLP_ENDPOINT=https://otlp.arize.com/v1/traces

CLAUDE_COLLECTOR_LISTEN=:14318
CLAUDE_COLLECTOR_ALLOW_BODY_REF=true
CLAUDE_COLLECTOR_EXPORT_FILE=/tmp/claude-collector-latest.jsonl
```

Optional dynamic project naming from a Claude Code resource attribute:

```bash
CLAUDE_COLLECTOR_PROJECT_FROM_RESOURCE_ATTRIBUTE=team.name
```

When this is set, the collector copies the incoming resource attribute value directly into Arize/OpenInference project attributes. For example, if the Claude Code process sets `OTEL_RESOURCE_ATTRIBUTES=team.name=search`, the forwarded spans use project name `search`. `ARIZE_PROJECT_NAME` remains the fallback when the resource attribute is missing.

Optional body-ref cleanup:

```bash
CLAUDE_COLLECTOR_BODY_REF_CLEANUP_ROOTS=/tmp/claude-code-otel-bodies
CLAUDE_COLLECTOR_BODY_REF_CLEANUP_TTL=1h
CLAUDE_COLLECTOR_BODY_REF_CLEANUP_SUFFIXES=.request.json,.response.json
```

`.env` is ignored by git and should stay local.

## Claude Code Env Vars

Set these in the shell that runs `claude`, not in Claude settings files:

```bash
export CLAUDE_CODE_ENABLE_TELEMETRY=1
export CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1
export ENABLE_ENHANCED_TELEMETRY_BETA=1

export OTEL_TRACES_EXPORTER=otlp
export OTEL_LOGS_EXPORTER=otlp
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_LOGS_PROTOCOL=http/protobuf

export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://localhost:14318/v1/traces
export OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=http://localhost:14318/v1/logs
```

To set the Arize project dynamically per Claude Code process, add the resource attribute named by `CLAUDE_COLLECTOR_PROJECT_FROM_RESOURCE_ATTRIBUTE`:

```bash
export OTEL_RESOURCE_ATTRIBUTES=team.name=search
```

Content gates for richer Arize input/output display:

```bash
export OTEL_LOG_USER_PROMPTS=1
export OTEL_LOG_TOOL_DETAILS=1
export OTEL_LOG_TOOL_CONTENT=1
export OTEL_LOG_RAW_API_BODIES=file:/tmp/claude-code-otel-bodies
```

`OTEL_LOG_RAW_API_BODIES=file:<dir>` lets Claude Code write request/response bodies to local files. The collector can use those body refs to populate LLM `input.value`, `output.value`, and `llm.output_messages` without relying on truncated inline log bodies.

Do not set these telemetry variables in Claude `settings.json` or `settings.local.json`; those settings can override local `.env` and shell exports. The `debug-setup` skill checks this before running.

## Run The Collector

Build once:

```bash
go build -o bin/claude-collector ./cmd/claude-collector
```

Run with Arize config from `.env`:

```bash
./scripts/run-arize-binary.sh
```

Or run through `go run`:

```bash
./scripts/run-arize.sh
```

Health check:

```bash
curl -fsS http://localhost:14318/healthz
```

Send a simple Claude marker prompt:

```bash
printf '%s\n' 'Use Bash to echo collector-smoke, then answer exactly with the echoed value.' \
  | claude -p --model haiku --permission-mode bypassPermissions --allowedTools Bash
```

Inspect local JSONL if `CLAUDE_COLLECTOR_EXPORT_FILE` is enabled:

```bash
jq -c 'select(.signal=="traces") | .spans[] |
  {name, status, kind:.attributes["openinference.span.kind"],
   model:.attributes["llm.model_name"],
   tool:.attributes["tool.name"],
   input:.attributes["input.value"],
   output:.attributes["output.value"]}' /tmp/claude-collector-latest.jsonl
```

## Local Isolated Run

For a manual local-only collector without Arize forwarding:

```bash
mkdir -p /tmp/claude-code-otel-bodies

go run ./cmd/claude-collector \
  --listen :14319 \
  --exporter-endpoint none \
  --export-file /tmp/claude-collector-debug.jsonl \
  --allow-body-ref \
  --forward-logs=false
```

Point Claude Code at `localhost:14319` for that isolated run.

To stop only the listener process:

```bash
lsof -ti tcp:14319 -sTCP:LISTEN | xargs -r kill
```

Do not use plain `lsof -ti tcp:<port> | xargs kill`; that can kill client processes connected to the port.

## Configuration

Flags can also be set with environment variables:

| Flag | Env | Default |
| --- | --- | --- |
| `--listen` | `CLAUDE_COLLECTOR_LISTEN` | `:4318` |
| `--exporter-endpoint` | `CLAUDE_COLLECTOR_EXPORTER_ENDPOINT` | `http://localhost:4319` |
| `--exporter-headers` | `CLAUDE_COLLECTOR_EXPORTER_HEADERS` | empty |
| `--export-file` | `CLAUDE_COLLECTOR_EXPORT_FILE` | empty |
| `--forward-logs` | `CLAUDE_COLLECTOR_FORWARD_LOGS` | `true` |
| `--project-name` | `CLAUDE_COLLECTOR_PROJECT_NAME` | empty |
| `--project-from-resource-attribute` | `CLAUDE_COLLECTOR_PROJECT_FROM_RESOURCE_ATTRIBUTE` | empty |
| `--resource-attributes` | `CLAUDE_COLLECTOR_RESOURCE_ATTRIBUTES` | empty |
| `--service-name-filter` | `CLAUDE_COLLECTOR_SERVICE_NAME_FILTER` | `claude-code` |
| `--trace-delay` | `CLAUDE_COLLECTOR_TRACE_DELAY` | `500ms` |
| `--output-ttl` | `CLAUDE_COLLECTOR_OUTPUT_TTL` | `5m` |
| `--allow-body-ref` | `CLAUDE_COLLECTOR_ALLOW_BODY_REF` | `false` |
| `--body-ref-cleanup-roots` | `CLAUDE_COLLECTOR_BODY_REF_CLEANUP_ROOTS` | empty |
| `--body-ref-cleanup-ttl` | `CLAUDE_COLLECTOR_BODY_REF_CLEANUP_TTL` | `1h` |
| `--body-ref-cleanup-suffixes` | `CLAUDE_COLLECTOR_BODY_REF_CLEANUP_SUFFIXES` | `.request.json,.response.json` |

## What It Maps

For every mapped Claude Code span, the bridge sets OTel span status for backends that display status directly: explicit Claude errors or `success=false` become `ERROR`; otherwise completed Claude spans become `OK`.

### `claude_code.llm_request`

Adds:

- `openinference.span.kind=LLM`
- `llm.system=anthropic`
- `llm.model_name`
- `llm.token_count.prompt`, `llm.token_count.completion`, and `llm.token_count.total`
- `llm.request_id`
- `input.value` from correlated request bodies when available
- `output.value` and `llm.output_messages.*` from correlated response bodies when available

### `claude_code.tool`

Adds:

- `openinference.span.kind=TOOL`
- `tool.name`
- `tool.file_path`
- `tool.command`
- `tool.url`, `tool.query`, `tool.description`, and `tool.truncated` when present
- `agent.name` for Task subagents
- `input.value` from tool input/parameters
- `output.value` from tool output events when `OTEL_LOG_TOOL_CONTENT=1`

### Claude Tool Child Spans

Adds `openinference.span.kind=CHAIN` to native tool child spans such as `claude_code.tool.blocked_on_user` and `claude_code.tool.execution`. For `claude_code.tool.blocked_on_user`, the permission `decision` is copied to `input.value` so it appears in Arize's Input tab.

### `claude_code.interaction`

Adds:

- `openinference.span.kind=AGENT`
- `trace.number` from `interaction.sequence`
- `input.value` from `user_prompt` when `OTEL_LOG_USER_PROMPTS=1`

## Body Ref Cleanup

`body_ref` reading is disabled by default because the collector must read local files to attach inputs and outputs. Cleanup is opt-in: when `CLAUDE_COLLECTOR_BODY_REF_CLEANUP_ROOTS` is set, the collector deletes only body-ref files it successfully read, only under configured directories, and only with allowed suffixes after `CLAUDE_COLLECTOR_BODY_REF_CLEANUP_TTL`.

## Non-Goals

This does not synthesize the legacy plugin's curated `Turn N`, `TaskCompleted`, `Notification`, `team.name`, or `agent.role=teammate` spans. It focuses on making Claude's native `LLM` and `TOOL` spans recognizable to OpenInference/Arize while keeping the native trace tree intact.
