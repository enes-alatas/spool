# Langfuse for Activity — reading notes (2026-10-29)

Sources: langfuse-docs content/integrations/developer-tools/claude-code.mdx, langfuse docker-compose.yml, code.claude.com/docs/en/monitoring-usage (dumped to cc-monitoring.txt).

## Langfuse's Claude Code integration
- A Python **Stop hook** (`~/.claude/hooks/langfuse_hook.py`, or the langfuse-observability plugin). Runs after every response, reads the session's transcript JSONL incrementally, groups rows into turns, emits one trace per turn: input = user text, one generation per assistant message (model, output text, tool calls), tool_use children, tool results as next generation's input. Metadata: session_id, turn_number, transcript_path.
- Needs Python 3.9+ and `langfuse>=4.0,<5`; reaches into SDK internals to backdate observations.
- Sends full prompt and response text and tool inputs — a second copy of the transcript, owner DMs included.
- Spool already owns the Stop hook on cloud loops (internal/httpapi/cloud_hooks.go) and injects hooks via args.go — a second Stop hook must be merged, not replace ours.

## Alternative: Claude Code native OpenTelemetry
- `CLAUDE_CODE_ENABLE_TELEMETRY=1` + OTLP endpoint; metrics (session, cost, tokens, LoC, active time), events (user_prompt, assistant_response, tool_result, api_request/error, mcp connection, hook execution, compaction, subagent…), traces (beta, OTEL_TRACES_EXPORTER). Content redacted by default; OTEL_LOG_USER_PROMPTS / ASSISTANT_RESPONSES / TOOL_DETAILS / TOOL_CONTENT opt in.
- Langfuse ingests OTLP traces (OpenTelemetry endpoint since 2025-02). Metrics/events need a different backend (Prometheus etc.).
- Spool sets env for its sessions, so it can point every loop at an OTLP endpoint without a Python hook. Cloud sessions: env set in the environment, or via the setup script.

## Self-hosting weight
docker-compose.yml: langfuse-web, langfuse-worker, clickhouse 25.12, minio, redis 7, postgres 17 — six containers.

## Activity questions (draft)
spend per loop per day; turn duration distribution; tool-call mix per loop; failures (api errors, retries exhausted, refused turns); wake cadence vs schedule; messages per loop per surface; compaction frequency; which turn was slow and why (trace).
