#!/usr/bin/env bash
# deploy/upload-tokens.sh -- Upload OAuth tokens and config to Fly volume.
# Run after first deploy: bash deploy/upload-tokens.sh
set -euo pipefail

FLY=~/.fly/bin/fly
TOKEN_DIR=/home/jpb/dev/workshop/data/google-auth/tokens

echo "=== uploading virgil config and OAuth tokens to /data ==="

$FLY sftp shell --app virgil-justin << 'SFTP'
put /home/jpb/dev/virgil/config/virgil.yaml /data/virgil.yaml
put /home/jpb/dev/workshop/data/google-auth/tokens/personal.json /data/google-token-personal.json
put /home/jpb/dev/workshop/data/google-auth/tokens/keep.json /data/google-token-keep.json
put /home/jpb/dev/workshop/data/google-auth/tokens/enver.json /data/google-token-enver.json
put /home/jpb/dev/workshop/data/google-auth/tokens/passion.json /data/google-token-passion.json
SFTP

echo "=== done ==="
echo "Verify with: fly ssh console --app virgil-justin -C 'ls -la /data/'"
