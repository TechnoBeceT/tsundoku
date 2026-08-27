# Engine-host RPC contract

The engine host serves HTTP/JSON on one port. Existing source, image, configuration, and extension
routes keep their request and response fields unchanged. The status route below is additive.

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
| `extension_running` | boolean | Whether the single extension executor is running a task. |
| `extension_queued` | integer | Tasks waiting for the extension executor. |

`busiest_sources` is ordered by running descending, queued descending, then source id ascending.
The response never includes request bodies, URLs, headers, cookies, tokens, preferences, or stack
traces. Source ids are the engine's existing public identifiers. A non-GET request returns HTTP 405.
