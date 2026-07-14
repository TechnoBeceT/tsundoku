# Tsundoku engine-host

A stateless JVM extension-host that loads real Mihon/Tachiyomi extensions and answers
`(sourceId, source-relative url)`-addressed source calls over a thin HTTP/JSON RPC — with **no
Suwayomi server, no database, no GraphQL**. It replaces the embedded Suwayomi engine: Tsundoku
(the Go/Nuxt app) owns the library; this host only fetches from sources and manages the extension
working-set (installed APKs + per-source preferences) on a mounted volume.

Built on Suwayomi-Server's own JVM-native code (AndroidCompat runtime + the `eu.kanade.tachiyomi`
source API + extension loaders) via a Gradle **composite build** — no IKVM, no Suwayomi fork.

## License

This subdirectory is **Mozilla Public License 2.0** (see `LICENSE`) — it adapts MPL-2.0 code from
[Suwayomi-Server](https://github.com/Suwayomi/Suwayomi-Server). MPL is file-level: the surrounding
Tsundoku repository stays MIT, and no cross-infection occurs. Files adapted from Suwayomi keep their
MPL-2.0 headers.

## Build & run (local dev)

JDK 21+ is required (JDK 17 CANNOT build AndroidCompat's `--release 21` Java sources). The Gradle
toolchain is pinned to 21 and auto-provisioned. A modern JDK (26) is fine as the Gradle daemon JVM.

```
JAVA_HOME=/usr/lib/jvm/java-26-openjdk ./gradlew run \
  --args="https://raw.githubusercontent.com/keiyoushi/extensions/repo/apk/tachiyomi-all.mangadex-v1.4.211.apk 7777"
curl localhost:7777/health
```

The composite build points at a local Suwayomi checkout; override with
`-PsuwayomiSrc=/path/to/Suwayomi-Server` (the Docker build stage vendors it).

## RPC contract

Frozen in `RPC-CONTRACT.md` (the P2 interface). Every source/manga/chapter call is addressed by
`(sourceId, url)` — never an opaque engine id — so a DB rebuild + extension reinstall resolves the
same series (killing the wrong-series bug).
