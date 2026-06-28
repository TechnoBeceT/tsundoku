#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
bunx nuxi generate
rm -rf ../backend/dist
cp -r .output/public ../backend/dist
echo "SPA built into backend/dist"
