#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

binary="$tmp_dir/agyn"
go build -o "$binary" ./cmd/agyn

commands=(auth apps app-proxy messages threads expose files)
for command in "${commands[@]}"; do
  "$binary" "$command" --help > /dev/null
done

"$binary" files download --help > /dev/null
