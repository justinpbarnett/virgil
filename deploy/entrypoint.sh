#!/bin/sh
# Starts virgil serve, optionally wrapped with litestream replication.
# Falls back to the embedded default config if /data/virgil.yaml doesn't exist.
# Litestream is enabled only when all three R2 env vars are present.
set -e

CONFIG=/data/virgil.yaml
if [ ! -f "$CONFIG" ]; then
    CONFIG=/etc/virgil/default.yaml
fi

if [ -n "${R2_ENDPOINT:-}" ] && [ -n "${R2_ACCESS_KEY:-}" ] && [ -n "${R2_SECRET_KEY:-}" ]; then
    exec litestream replicate -exec "virgil serve --config $CONFIG"
else
    exec virgil serve --config "$CONFIG"
fi
