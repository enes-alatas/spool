#!/bin/sh
# Prints what a Langfuse project holds, as shapes: how many observations there
# are and, for each field path, its types, how many distinct values it takes
# and the longest string in it. Input and output are parsed as JSON where they
# are JSON, so their inner fields show too. It prints no field value, except
# those of fields named in SHOW (default: type, model, level), so its output
# can go on an issue without the traces' content. #1187.
#
#   LANGFUSE_URL=http://127.0.0.1:3000 LANGFUSE_PUBLIC_KEY=pk-lf-... \
#   LANGFUSE_SECRET_KEY=sk-lf-... ./langfuse-report.sh [limit] [since]
#
# Reads Langfuse v4's GET /api/public/v2/observations, every field group, at
# most `limit` (default 200, at most 1000) observations started since `since`
# (default three days ago, ISO 8601). Needs curl, jq and GNU date.
set -eu
: "${LANGFUSE_URL:?}" "${LANGFUSE_PUBLIC_KEY:?}" "${LANGFUSE_SECRET_KEY:?}"
limit=${1:-200}
since=${2:-$(date -u -d '3 days ago' +%Y-%m-%dT%H:%M:%SZ)}
SHOW=${SHOW:-type model level}
fields=core,basic,time,io,metadata,model,usage,prompt,metrics,trace_context

curl -fsS -G -u "$LANGFUSE_PUBLIC_KEY:$LANGFUSE_SECRET_KEY" \
	--data-urlencode "fields=$fields" --data-urlencode "limit=$limit" \
	--data-urlencode "fromStartTime=$since" \
	"$LANGFUSE_URL/api/public/v2/observations" |
jq -r --arg show "$SHOW" --arg since "$since" '
  ($show | split(" ")) as $show
  | [ .data[] | .input |= (if type == "string" then (fromjson? // .) else . end)
              | .output |= (if type == "string" then (fromjson? // .) else . end) ] as $rows
  | "== observations since \($since): \($rows | length)",
    "   traces: \([$rows[].traceId] | unique | length), sessions: \([$rows[].sessionId | select(. != null)] | unique | length)",
    ([ $rows[] | [paths(scalars) as $p
        | { path: ($p | map(if type == "number" then "[]" else . end) | join(".")),
            leaf: ($p | map(select(type == "string")) | last),
            v: getpath($p) } ] | unique_by(.path, .v) | .[] ]
     | group_by(.path) | .[]
     | { path: .[0].path, leaf: .[0].leaf,
         types: (map(.v | type) | unique | join("|")),
         n: length,
         maxlen: (map(select(.v | type == "string") | .v | length) | max // 0),
         values: (if (.[0].leaf as $l | $show | index([$l])) then (map(.v | tostring) | unique | .[:12] | join(", ")) else null end) }
     | "  \(.path)  [\(.types)]  distinct:\(.n)  maxlen:\(.maxlen)" + (if .values then "  = \(.values)" else "" end))
'
