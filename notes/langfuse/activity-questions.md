**The Activity questions, to match against this spike's evidence.** Each with where the answer could come from today. Spool's store already holds, per turn: trigger kind, start and end, error flag, cost, input/output/cache tokens, duration (`turns`), plus every stream event of the turn (`events`). Langfuse's reads are the Observations API v2 (rows) and the Metrics API v2 (aggregates: cost, usage, latency, volume, grouped by trace name, session, model or time).

| Question | Spool's own data | Langfuse |
|---|---|---|
| Spend per loop per day, and per trigger kind | yes, `turns` | yes, Metrics API, if the trace carries the loop as user or session |
| Turn duration distribution, slow turns | yes, `turns.duration_ms` | yes, latency per trace |
| Tool-call mix per loop | derivable from `events` payloads | yes, tool_use children per generation |
| Failures: errored turns, API errors, retries exhausted | errored turns yes; API errors and retries only if the stream shows them | OTel events yes (`api_error`, `api_retries_exhausted`); the Stop hook no |
| Wake cadence against the loop's own next-wake | yes, `turns.trigger_kind` + times | no |
| Messages per loop per surface, undelivered ones | yes, `messages` | no |
| Compaction frequency and context pressure | only if the stream shows it | OTel `compaction` event yes; the Stop hook no |
| Inside one slow turn: which model calls, how long, how many tokens each | no | yes, generations with latency and usage, both ways |
| Cross-loop comparison on one screen | yes | yes |

Reading: most loop-level questions are answerable from Spool's store with no new dependency; what Langfuse adds is the inside of a turn and, via OTel only, the API-error and compaction events. The spike's evidence should say whether the per-turn trace is worth the six containers, and whether the loop is identifiable in Langfuse (session or user ID per loop) without content.

 — Milo · Product Owner
