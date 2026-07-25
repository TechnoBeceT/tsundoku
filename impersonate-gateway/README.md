# impersonate-gateway

An **optional** Chrome-fingerprint image-fetch proxy for Tsundoku.

## Why it exists

Most self-hosted scanlator CDNs serve images fine to Tsundoku's engine-host. A few
block a request purely on its **TLS/JA3 fingerprint** — the engine-host's default
HTTP client (okhttp) is fingerprinted and served a `403`/stall, while a request
carrying a real browser fingerprint gets the same URL at `200`. This service performs
one upstream request with a Chrome fingerprint (via [`curl_cffi`](https://github.com/lexiforest/curl_cffi))
and hands back the raw bytes, so the engine-host can route the fetches a
fingerprint-blocking source rejects through here and keep okhttp for everything else.

It is **off by default** and **general** — no source is special-cased. When enabled,
the engine-host tries the gateway first for image bytes and transparently falls back
to its normal client on any gateway failure.

## Configuration

The gateway URL is set in **Tsundoku → Settings → Server config → Chrome-fingerprint
image proxy** — it is **not** an environment variable. Enable the toggle and enter the
gateway's URL (e.g. `http://impersonate-gateway:8788` on the compose network). A blank
URL disables it regardless of the toggle. Any per-source SOCKS/VPN egress configured
in Tsundoku is passed through to the gateway, so a routed source keeps its own egress.

The service itself needs no configuration beyond its listen port (`8788`, or `PORT`
for a local run).

## HTTP contract

- `POST /fetch` — JSON body:

  ```json
  {
    "url": "https://cdn.example/img.webp",
    "method": "GET",
    "headers": { "Referer": "https://example/", "User-Agent": "..." },
    "body_b64": null,
    "socks": null,
    "impersonate": "chrome"
  }
  ```

  - **Reached upstream** → `200`, body = raw upstream bytes, `Content-Type` = the
    upstream content-type, `X-Upstream-Status` = the upstream status code. The upstream
    status rides a header (not the HTTP status), so a genuine upstream `403`/`404` is
    still returned as a `200` gateway response for the caller to judge.
  - **Gateway failure** (bad proxy, timeout, unreachable, any exception) → `502` with
    `X-Gateway-Error: <reason>` and a tiny body. Never `200`.

- `GET /health` → `200 {"status":"ok"}`.

SOCKS5 and SOCKS4 proxies are supported via the `socks` field
(`socks5://[user:pass@]host:port`).

## Run

```bash
docker build -t impersonate-gateway .
docker run --rm -p 8788:8788 impersonate-gateway
```

Or via the repo's `docker-compose.yml` (the `impersonate-gateway` service), then point
Tsundoku Settings at `http://impersonate-gateway:8788`.
