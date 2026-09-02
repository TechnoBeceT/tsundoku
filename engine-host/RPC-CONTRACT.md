# Engine-host RPC contract

The engine host serves HTTP/JSON on one port. Existing source, image, configuration, and extension
routes keep their request and response fields unchanged. The status route below is additive.

## `GET /extensions/{pkgName}/installed-apk`

Streams the exact APK backing the installed extension record. This loopback-only control route
requires `Authorization: Bearer <control-token>` and returns HTTP 401 when the token is missing or
wrong. Non-GET requests return HTTP 405.

The success response is bounded to 256 MiB, uses a fixed `Content-Length`, and has
`Content-Type: application/vnd.android.package-archive`. Identity headers are
`X-Tsundoku-Extension-Package`, `X-Tsundoku-Extension-Version-Code`, and the optional
`X-Tsundoku-Extension-Version-Name`. The host holds its extension mutation lock until streaming
finishes, so the bytes and identity headers describe one installed generation.

## Prepared extension updates

The three loopback-only routes below require `Authorization: Bearer <control-token>` and return 401
when it is absent or wrong.

`POST /extensions/{pkgName}/prepare-update` downloads, verifies, converts, and instantiates the next
repository candidate without changing installed files, the manifest, or the live source registry. It
returns an opaque `token` plus `pkgName`, installed/candidate version codes, `installedSourceIds`,
`candidateSourceIds`, `removedSourceIds`, and `mutationSequence`. One candidate is retained per
package for five minutes; replacement, expiry, discard, activation, rejection, or host shutdown
removes its temporary APK/JAR and classloader.

`POST /extensions/{pkgName}/prepare-reinstall` accepts an exact local `apkUrl`, `pkgName`, and
`candidateVersionCode`. It verifies package/version identity and signer continuity, then returns the
same source-ID witness and token as prepare-update. Direct `/extensions/install` cannot replace a
package discovered to be installed after APK preparation; held-version replacement must use this
protected activation path.

`POST /extensions/{pkgName}/activate-prepared-update` accepts the complete prepared response echoed
unchanged plus `protectedSourceIds`. The host rejects token, package, version, mutation-sequence, and
source-ID mismatches before filesystem, manifest, or registry mutation. It also re-instantiates the
retained candidate and verifies its source IDs again during activation. If any removed source is
protected, it returns HTTP 409:

```json
{"error":"...","code":"source_retirement_conflict","pkgName":"...","sourceIds":[123]}
```

`DELETE /extensions/{pkgName}/prepared-update` accepts `{"token":"..."}` and explicitly releases a
candidate. Discard is idempotent when that package has no candidate; a token mismatch is HTTP 400.

`POST /extensions/{pkgName}/prepared-update-outcome` accepts `{"token":"..."}` and returns the
durable activation state (`pending`, `committed`, `rejected`, or `unknown`) plus package and candidate
version identity. Outcome files are named by a SHA-256 token digest, survive host restart, and expire
on a bounded cleanup window. A `pending` or `unknown` result is ambiguous and must not trigger another
activation. The legacy direct `POST /extensions/{pkgName}/update` route is retired with HTTP 410.

## `PUT /config/image-transport`

Applies a partial image connection-policy update and returns the normalized
current config.

| Field | JSON type | Meaning |
|---|---:|---|
| `reuseSourceIds` | array of integers, optional | Source IDs whose fallback image requests use their normal pooled client. Omitted preserves the current selection; `[]` clears it. The response is ascending and duplicate-free. |

The impersonate gateway remains first: a gateway success returns before source-client
selection. On the fallback path, listed sources use the normal source client through the
cacheless call API; all others derive the existing no-idle-pool client.

## Image upstream failures

`POST /image` continues to return HTTP 502 when the source image server rejects the request. Its
JSON error envelope may additionally include `upstreamStatus` and `retryAfterSeconds` when the host
observed them directly. `retryAfterSeconds` accepts the standard delta-seconds and HTTP-date forms,
is omitted unless it is future-facing and at most 86,400 seconds, and never exposes other upstream
headers. Older string-only `{"error":"..."}` responses remain valid.

## `GET /status`

Returns HTTP 200 with one bounded runtime snapshot:

| Field | JSON type | Meaning |
|---|---:|---|
| `ready` | boolean | The front door is accepting control requests. |
| `source_workers` | integer | Configured physical source-worker limit. |
| `per_source_limit` | integer | Configured physical running limit for one source id. |
| `queued` | integer | Source calls waiting for physical admission. |
| `running` | integer | Physically occupied source workers, including timed-out calls whose code has not returned. |
| `completion_sequence` | integer | Monotonic sequence incremented whenever one physical source callable returns. |
| `oldest_running_millis` | integer | Age of the oldest physically running call, or zero when none is running. |
| `completed` | integer | Public results completed normally or exceptionally, excluding cancellation and host timeout. |
| `cancelled` | integer | Public results cancelled before or during execution. |
| `timed_out` | integer | Public results completed by the host source-call deadline. |
| `rejected` | integer | Source submissions rejected at capacity or after scheduler shutdown. |
| `busiest_sources` | array | At most ten `{source_id, queued, running}` entries. |
| `extension_running` | boolean | Whether either bounded extension lane is running a task. |
| `extension_queued` | integer | Tasks waiting across the bounded extension mutation and network lanes. |
| `kcef` | object | Embedded-browser capability as `{state,errorCode}`. State is `disabled`, `initializing`, `ready`, or `failed`; errorCode is null, `init_timeout`, or `init_failed`. |

`busiest_sources` is ordered by running descending, queued descending, then source id ascending.
The response never includes request bodies, URLs, headers, cookies, tokens, preferences, or stack
traces. Source ids are the engine's existing public identifiers. A non-GET request returns HTTP 405.
`/health` remains independent RPC liveness and may return HTTP 200 while KCEF is initializing or
failed.
