#!/bin/sh
# Fetch the ffmpeg.wasm core (32 MB, GPL) that the 0x0 page loads for video
# metadata stripping and compression. It is served from this site so it also
# works over Tor without reaching a CDN.
#   tools/fetch-ffmpeg-core.sh [frontend dir, default frontends/0x0]
set -eu
dest="${1:-frontends/0x0}/scripts/vendor/ffmpeg/core"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
(cd "$tmp" && npm pack --silent @ffmpeg/core@0.12.10 >/dev/null && tar -xzf ffmpeg-core-0.12.10.tgz)
mkdir -p "$dest"
cp "$tmp/package/dist/umd/ffmpeg-core.js" "$tmp/package/dist/umd/ffmpeg-core.wasm" "$dest/"
ls -l "$dest"
