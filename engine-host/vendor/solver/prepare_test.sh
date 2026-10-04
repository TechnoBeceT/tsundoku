#!/bin/sh
set -eu
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT HUP INT TERM
source_input=${1:?usage: prepare_test.sh unmodified-pinned-source-tree}
source_tree=$fixture/source
mkdir -p "$source_tree/server/src/main/kotlin/eu/kanade/tachiyomi/network/interceptor"
cp "$source_input/server/src/main/kotlin/eu/kanade/tachiyomi/network/interceptor/CloudflareInterceptor.kt" "$source_tree/server/src/main/kotlin/eu/kanade/tachiyomi/network/interceptor/CloudflareInterceptor.kt"
printf 'preserved\n' > "$source_tree/settings.gradle.kts"
original=$(sha256sum "$source_tree/server/src/main/kotlin/eu/kanade/tachiyomi/network/interceptor/CloudflareInterceptor.kt")
"$here/prepare.sh" "$source_tree" "$fixture/prepared" >/dev/null
[ "$original" = "$(sha256sum "$source_tree/server/src/main/kotlin/eu/kanade/tachiyomi/network/interceptor/CloudflareInterceptor.kt")" ]
cmp "$source_tree/settings.gradle.kts" "$fixture/prepared/settings.gradle.kts"
"$here/prepare.sh" "$source_tree" "$fixture/prepared" >/dev/null
if "$here/prepare.sh" "$source_tree" "$source_tree" >/dev/null 2>&1; then exit 1; fi
mkdir "$fixture/unknown"
printf 'preserve\n' > "$fixture/unknown/data"
if "$here/prepare.sh" "$source_tree" "$fixture/unknown" >/dev/null 2>&1; then exit 1; fi
[ "$(cat "$fixture/unknown/data")" = preserve ]
# A prepared-tree modification cannot silently substitute different runtime code.
printf '\nmodified\n' >> "$fixture/prepared/server/src/main/kotlin/eu/kanade/tachiyomi/network/interceptor/SolverTransport.kt"
if "$here/prepare.sh" "$source_tree" "$fixture/prepared" >/dev/null 2>&1; then exit 1; fi
ln -s "$source_tree" "$fixture/link"
if "$here/prepare.sh" "$source_tree" "$fixture/link" >/dev/null 2>&1; then exit 1; fi
printf '\nmodified\n' >> "$source_tree/server/src/main/kotlin/eu/kanade/tachiyomi/network/interceptor/CloudflareInterceptor.kt"
if "$here/prepare.sh" "$source_tree" "$fixture/rejected" >/dev/null 2>&1; then exit 1; fi
[ ! -e "$fixture/rejected" ]
printf 'preparation preservation and fail-closed checks passed\n'
