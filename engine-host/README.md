# Tsundoku engine-host

A stateless JVM extension-host that loads real Mihon/Tachiyomi extensions and answers
`(sourceId, source-owned address)` calls over a thin HTTP/JSON RPC. Addresses may be relative or
opaque extension keys, or absolute cross-origin URLs when the source's URL-search path owns request
state. There is **no Suwayomi server, no database, no GraphQL**. It replaces the embedded engine: Tsundoku
(the Go/Nuxt app) owns the library; this host only fetches from sources and manages the extension
working-set (installed APKs + per-source preferences) on a mounted volume.

Built on Suwayomi-Server's own JVM-native code (AndroidCompat runtime + the `eu.kanade.tachiyomi`
source API + extension loaders) via a Gradle **composite build**. The pinned dependency receives
a small repository-maintained solver correction in a disposable source copy.

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

### Solver transport ownership

Gradle settings call `vendor/solver/prepare.sh` for local, CI and Docker builds. It verifies the
pinned original interceptor and dependency revision, copies sources into `build/suwayomi`, and
applies the MPL-licensed correction there. The input checkout is never patched. Changed or
unexpected prepared sources fail the build. Update the revision, original hash and patch together
when upgrading the dependency.

Only the exact empty session string selects disposable browser solves; whitespace-only names remain
named sessions with their exact identity. Disposable solves can run independently. Cancelling an app request cancels their nested
HTTP call, including response-body reads. A configured named session keeps its exact endpoint,
name and TTL. Its browser lease remains exclusive while an active solve drains after caller
cancellation; app source workers return promptly. A fully consumed, valid solver response permits
reuse. Transport failure, timeout, malformed body or unsuccessful HTTP response leaves that named
session unresolved. If the solver advertises `fenced-drain-close-v1`, the host confirms an
authoritative drain-and-close for the exact session name and recreates it with an opaque
process-incarnation generation token; the replacement starts with fresh cookies. A capability
probe, prepare or confirmation failure never falls back to an untagged solve. Unresolved sessions
on solvers without that explicit capability remain blocked for manual recovery; HTTP status, socket closure and
elapsed time do not establish remote completion.

Ownership is bounded to eight physical solver transports, 128 app callers and 128 session entries.
The caller deadline includes admission and response reading, using the configured solver timeout
plus the existing ten-second transport allowance. Once admitted, a named solve receives its own full
transport allowance to drain safely even if its caller expires. Disposable solves retain the caller
deadline and cancel with their caller. Existing cookie handling, user agent, response
fallback and the configured network client's egress remain in use.
Solver payloads and source retry headers share native cookie scope matching (domain,
host-only, path and HTTPS) plus expiry checks; cookies outside that request's scope stay
stored without being transmitted.

Leases are process-local. Multiple hosts must not share a named browser without external
serialization. Restarting a host clears local knowledge; it does **not** establish remote completion.
After an unresolved solve, verify remote termination or recreate the remote browser before restarting
or reusing that session. HTTP cancellation alone is not a remote browser cancellation acknowledgement;
unsupported solvers require verified remote termination or manual recreation before reuse.

Verify preparation and runtime behavior with:

```sh
sh vendor/solver/prepare_test.sh /path/to/pinned/Suwayomi-Server
./gradlew -PsuwayomiSrc=/path/to/pinned/Suwayomi-Server test installDist
```

### Comix 1.6.42 and 1.6.43 compatibility

The verified official APK is kept unchanged. During APK preparation, the derived JVM jar receives
an exact package/version correction for chapter responses that omit `pages.baseUrl`: every image
must have an absolute HTTP(S) URL before the parser supplies an empty base. Relative URLs without a
base fail explicitly; the original required `items` field, page order, scrambling and image headers
remain in use. This response correction applies only to 1.6.42; the official 1.6.43 already defaults
the missing base.

Both versions locate the chapter API among at most 32 same-origin static JavaScript imports instead
of depending on the site's old `env-` bundle filename prefix. The existing site client performs the
chapter requests, with original pagination and filtering. Unexpected released script or DTO bytecode
fails preparation. Other packages and versions bypass these corrections. Official package identity,
version, source IDs and signer continuity remain unchanged, so normal official updates remain available.

Existing installed jars are reused on startup and preference reload. After deploying this correction,
use the existing prepared reinstall/update and activation flow with the official APK to regenerate
its jar safely; restarting alone does not regenerate it. Activation publishes a new jar generation and
keeps replaced loaders available to in-flight calls.

The corrected jar calls the host URL validator. If reverting to an engine build that predates this
correction, regenerate Comix from the preserved official APK through that build's protected reinstall
flow as well; an image rollback alone retains the corrected jar on the shared volume.


### Kayn Scans chapter metadata

Kayn Scans uses opaque chapter addresses and requires metadata from its own chapter list. Page
resolution hydrates the source manga and selects the exact matching chapter before calling the
extension. Hidden chapters stay unavailable, and visible locked chapters keep the extension's
refusal. Other sources retain the existing bare-chapter path and explicit chapter-refresh signal.

### The Blank 1.6.1 compatibility

The verified official APK stays unchanged. Its derived JVM jar adapts the reader's shuffled
semantic/export-name pairs to the object table expected by this release. The original WASM export
resolution, restricted host imports, signing, chapter authorization and image decryption remain in
use. Incomplete or ambiguous pair tables fail explicitly. Preparation applies this correction only
to the official package, signer and exact version; later official versions bypass it.

After deployment, regenerate the existing jar through the prepared same-version reinstall and
activation flow. Profile hosts share that jar on disk but keep their own loaded classes; after
activation, drain and restart the app so every host loads the corrected generation. A restart alone
reuses the installed jar. When reverting to an engine build without
this adapter, regenerate from the preserved official APK through that build's protected reinstall
flow too; the adapted jar references a host helper absent from older builds.
