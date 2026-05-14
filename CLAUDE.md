# Claude Code Collector

Repo-local Claude skills live in `.agents/skills/`.

Use these skills for setup/debugging:

- `debug-setup`: validate the full collector setup without touching any live collector. It starts its own debug collector on a separate port, verifies Claude -> collector -> local JSONL, then verifies authenticated OTLP forwarding to Arize with the same kind of marker trace.
- `debug-live-collector`: test an already-running collector. It checks the live health endpoint, sends a marker trace to the configured live port, and inspects local export/log output when available.

When the user asks to debug setup, validate telemetry, test Arize forwarding, or run a marker trace, read the relevant `SKILL.md` first and follow it.
