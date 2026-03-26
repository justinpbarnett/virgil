#!/bin/sh
# Starts virgil serve, optionally wrapped with litestream replication.
# Litestream is enabled only when all three R2 env vars are present.
set -e

if [ -n "${R2_ENDPOINT:-}" ] && [ -n "${R2_ACCESS_KEY:-}" ] && [ -n "${R2_SECRET_KEY:-}" ]; then
    exec litestream replicate -exec "virgil serve --config /data/virgil.yaml"
else
    exec virgil serve --config /data/virgil.yaml
fi
