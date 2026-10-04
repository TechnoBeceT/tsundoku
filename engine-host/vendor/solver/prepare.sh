#!/bin/sh
# Compile the repository's solver correction in a disposable dependency copy.
set -eu
source_tree=$(CDPATH= cd -- "${1:?usage: prepare.sh source-tree output-tree}" && pwd -P)
output=${2:?usage: prepare.sh source-tree output-tree}
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
relative=server/src/main/kotlin/eu/kanade/tachiyomi/network/interceptor/CloudflareInterceptor.kt
expected=df02e1ae636c84b404e53f0225e2735b6faa21aca9cb5af1fdd4ec6da24bcd0d
actual=$(sha256sum "$source_tree/$relative" | cut -d ' ' -f1)
[ "$actual" = "$expected" ] || { printf 'solver preparation: pinned source does not match\n' >&2; exit 1; }
if [ -e "$source_tree/.git" ]; then
 revision=$(git -C "$source_tree" rev-parse HEAD)
 [ "$revision" = b0bc8c6fb3cdd050dbbfdeb50a9ee1b0d2cbad45 ] || { printf 'solver preparation: wrong dependency revision\n' >&2; exit 1; }
fi
mkdir -p "$(dirname -- "$output")"
output_parent=$(CDPATH= cd -- "$(dirname -- "$output")" && pwd -P)
output=$output_parent/$(basename -- "$output")
case "$output" in "$source_tree"|"$source_tree"/*) printf 'solver preparation: output must be outside the source tree\n' >&2; exit 1;; esac
[ ! -L "$output" ] || { printf 'solver preparation: output symlink refused\n' >&2; exit 1; }
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT HUP INT TERM
marker=$output/.tsundoku-solver-copy
[ ! -L "$marker" ] || { printf 'solver preparation: marker symlink refused\n' >&2; exit 1; }
if [ -d "$output" ]; then
 [ -f "$marker" ] && [ "$(head -n1 "$marker")" = "$source_tree" ] || { printf 'solver preparation: refusing to overwrite an unowned output\n' >&2; exit 1; }
else
 mkdir "$output"
 (cd "$source_tree" && tar --exclude='./.git' --exclude='*/build' --exclude='*/.gradle' --exclude='*/.kotlin' -cf "$fixture/source.tar" .)
 (cd "$output" && tar -xf "$fixture/source.tar")
 printf '%s\n' "$source_tree" > "$marker"
fi
patch_hash=$(cat "$here/cloudflare.patch" "$here/SolverTransport.kt" | sha256sum | cut -d ' ' -f1)
mkdir -p "$fixture/expected/$(dirname "$relative")"
cp "$source_tree/$relative" "$fixture/expected/$relative"
git -C "$fixture/expected" apply --check "$here/cloudflare.patch"
git -C "$fixture/expected" apply "$here/cloudflare.patch"
helper=$(dirname "$relative")/SolverTransport.kt
[ "$(realpath "$output/$(dirname "$relative")")" = "$output/$(dirname "$relative")" ] || { printf 'solver preparation: source directory symlink refused\n' >&2; exit 1; }
if [ "$(sed -n '2p' "$marker")" = "$patch_hash" ]; then
 cmp -s "$fixture/expected/$relative" "$output/$relative" && cmp -s "$here/SolverTransport.kt" "$output/$helper" || {
  printf 'solver preparation: prepared sources were modified\n' >&2; exit 1;
 }
else
 [ ! -L "$output/$relative" ] && [ ! -L "$output/$helper" ] || { printf 'solver preparation: source symlink refused\n' >&2; exit 1; }
 cp "$fixture/expected/$relative" "$output/$relative"
 cp "$here/SolverTransport.kt" "$output/$helper"
 printf '%s\n%s\n' "$source_tree" "$patch_hash" > "$marker"
fi
printf '%s\n' "$output"
