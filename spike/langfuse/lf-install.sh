#!/bin/sh
# Sets up one way of tracing a contained loop into Langfuse, in that loop's
# home, for the #1187 spike. Run it in the loop's workstation volume, as the
# loop's user, with the operator's own network, never from inside a turn:
#
#   docker run --rm -u loop -e HOME=/home/loop \
#     -v spool-ws-<loop id>:/home/loop -v "$PWD":/spike:ro \
#     --entrypoint sh spool-workstation /spike/lf-install.sh way1 https://langfuse.example.com <loop name>
#
#   way1    Langfuse's Claude Code Stop hook (plugin b1619f9), on a pinned
#           Python 3.12 venv with langfuse 4.18.0. Sends the whole transcript.
#   way2    Claude Code's own OpenTelemetry traces. Content stays redacted
#           (no OTEL_LOG_* set); turning one on is a separate operator choice.
#   remove  Takes out everything this script added, either way.
#
# The keys never go in here: they are the loop's env-var connections
# (LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY for way1,
# OTEL_EXPORTER_OTLP_TRACES_HEADERS for way2), so the hub redacts them like
# any secret. Everything this script installs sits under ~/.claude/langfuse,
# and its settings in ~/.claude/settings.json, merged beside what is there.
set -eu
way=${1:?way1, way2 or remove}
url=${2:-}
name=${3:-loop}

dir=$HOME/.claude/langfuse
settings=$HOME/.claude/settings.json
UV_VERSION=0.13.6
PLUGIN_SHA=b1619f994240ad49de4a97caa01d7d7853944fc0
HOOK_SHA256=c6316bcef445b78e622d6f164c861074762fa479126e8f2751393c609bd192e9
LANGFUSE_PY=4.18.0

case $(uname -m) in
x86_64) uv_arch=x86_64 uv_sha=f29c3e94ffabd7e6f3ba521dba169a9b7ec12100bf3257087f0f24f6123f3645 ;;
aarch64) uv_arch=aarch64 uv_sha=229e194255c7958d86d56e8790d969a6f74736592133f0dfd0076f754c642c1d ;;
*) echo "lf-install: no uv pin for $(uname -m)" >&2; exit 1 ;;
esac

python() {
	if [ ! -x "$dir/venv/bin/python" ]; then
		mkdir -p "$dir"
		tgz=$(mktemp)
		curl -fsSL -o "$tgz" "https://github.com/astral-sh/uv/releases/download/$UV_VERSION/uv-$uv_arch-unknown-linux-gnu.tar.gz"
		echo "$uv_sha  $tgz" | sha256sum -c - >/dev/null
		tar -xzf "$tgz" -C "$dir" --strip-components=1 "uv-$uv_arch-unknown-linux-gnu/uv"
		rm -f "$tgz"
		UV_PYTHON_INSTALL_DIR=$dir/python "$dir/uv" venv -q --python 3.12 "$dir/venv"
	fi
}

# merge MODE: adds (or, with MODE remove, takes out) this script's hook and env
# keys in ~/.claude/settings.json, leaving every other key as it was.
merge() {
	"$dir/venv/bin/python" - "$1" "$settings" "$dir" "$url" "$name" <<'PY'
import json, os, sys
mode, path, d, url, name = sys.argv[1:6]
s = json.load(open(path)) if os.path.exists(path) else {}
env = s.setdefault("env", {})
ours = ("LANGFUSE_BASE_URL", "LANGFUSE_USER_ID", "CC_LANGFUSE_CAPTURE_IMAGES",
        "CC_LANGFUSE_DEBUG", "CC_LANGFUSE_STATE_DIR", "CLAUDE_CODE_ENABLE_TELEMETRY",
        "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA", "OTEL_TRACES_EXPORTER",
        "OTEL_METRICS_EXPORTER", "OTEL_LOGS_EXPORTER",
        "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
for k in ours:
    env.pop(k, None)
hooks = s.setdefault("hooks", {})
for event in ("Stop", "SessionEnd"):
    kept = [g for g in hooks.get(event, [])
            if not any(d in h.get("command", "") for h in g.get("hooks", []))]
    if kept:
        hooks[event] = kept
    else:
        hooks.pop(event, None)
cmd = d + "/venv/bin/python " + d + "/langfuse_hook.py"
if mode == "way1":
    env.update({"LANGFUSE_BASE_URL": url, "LANGFUSE_USER_ID": name,
                "CC_LANGFUSE_CAPTURE_IMAGES": "false", "CC_LANGFUSE_DEBUG": "false",
                "CC_LANGFUSE_STATE_DIR": d + "/state"})
    for event in ("Stop", "SessionEnd"):
        hooks.setdefault(event, []).append({"hooks": [{"type": "command", "command": cmd}]})
elif mode == "way2":
    env.update({"CLAUDE_CODE_ENABLE_TELEMETRY": "1",
                "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1",
                "OTEL_TRACES_EXPORTER": "otlp", "OTEL_METRICS_EXPORTER": "none",
                "OTEL_LOGS_EXPORTER": "none",
                "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL": "http/protobuf",
                "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": url + "/api/public/otel/v1/traces"})
for k in ("env", "hooks"):
    if not s[k]:
        del s[k]
os.makedirs(os.path.dirname(path), exist_ok=True)
tmp = path + ".lf-tmp"
with open(tmp, "w") as f:
    json.dump(s, f, indent=2)
    f.write("\n")
os.replace(tmp, path)
PY
}

case $way in
way1 | way2)
	case $url in https://* | http://*) ;; *) echo "lf-install: give the Langfuse URL the loop reaches" >&2; exit 1 ;; esac
	python
	if [ "$way" = way1 ]; then
		UV_PYTHON_INSTALL_DIR=$dir/python "$dir/uv" pip install -q --python "$dir/venv/bin/python" "langfuse==$LANGFUSE_PY"
		curl -fsSL -o "$dir/langfuse_hook.py" "https://raw.githubusercontent.com/langfuse/claude-observability-plugin/$PLUGIN_SHA/hooks/langfuse_hook.py"
		echo "$HOOK_SHA256  $dir/langfuse_hook.py" | sha256sum -c - >/dev/null
	fi
	merge "$way"
	echo "lf-install: $way set for $name; it applies from the loop's next turn" >&2
	;;
remove)
	[ ! -x "$dir/venv/bin/python" ] || merge remove
	rm -rf "$dir"
	echo "lf-install: removed" >&2
	;;
*) echo "lf-install: way1, way2 or remove" >&2; exit 1 ;;
esac
