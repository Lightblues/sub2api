#!/bin/sh
set -e

# Fix data directory permissions when running as root.
# Docker named volumes / host bind-mounts may be owned by root,
# preventing the non-root sub2api user from writing files.
if [ "$(id -u)" = "0" ]; then
    mkdir -p /app/data
    # Use || true to avoid failure on read-only mounted files (e.g. config.yaml:ro)
    chown -R sub2api:sub2api /app/data 2>/dev/null || true
    # Re-invoke this script as sub2api so the flag-detection below
    # also runs under the correct user.
    exec su-exec sub2api "$0" "$@"
fi

# Start youtu LLM proxy sidecar if YOUTU_LLM_TOKEN is set
if [ -n "${YOUTU_LLM_TOKEN:-}" ]; then
    python3 /app/youtu_llm_proxy.py --host 127.0.0.1 --port 8088 \
        >> /app/data/youtu_proxy.log 2>&1 &
    # Wait for proxy to be ready (up to 5 seconds)
    for i in $(seq 1 10); do
        wget -q -T 1 -O /dev/null http://127.0.0.1:8088/health 2>/dev/null && break
        sleep 0.5
    done
fi

# Compatibility: if the first arg looks like a flag (e.g. --help),
# prepend the default binary so it behaves the same as the old
# ENTRYPOINT ["/app/sub2api"] style.
if [ "${1#-}" != "$1" ]; then
    set -- /app/sub2api "$@"
fi

exec "$@"
