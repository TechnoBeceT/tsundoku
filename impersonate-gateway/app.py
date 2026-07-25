"""Impersonate gateway — a tiny plaintext-HTTP image-fetch proxy.

Some source image CDNs (e.g. Hive Scans' storage.hivetoon.com) block a request
purely on its TLS/JA3 fingerprint: the engine-host's okhttp client is fingerprinted
and 403'd, while a browser-fingerprinted client (curl_cffi's Chrome impersonation)
is served the same URL at 200. This service performs one upstream request with a
Chrome fingerprint and returns the raw bytes, so the engine-host can route the
fetches a fingerprint-blocking source rejects through here and keep okhttp for
everything else.

Wire contract:

  POST /fetch   JSON body:
      {
        "url":        "<absolute upstream url>",       # required
        "method":     "GET",                           # default GET
        "headers":    { "Referer": "...", ... },       # forwarded verbatim
        "body_b64":   null | "<base64 request body>",  # optional
        "socks":      null | "socks5://[user:pass@]host:port",
        "impersonate":"chrome"                          # default "chrome"
      }
    - reached upstream -> HTTP 200, body = RAW upstream bytes,
      Content-Type = upstream content-type, X-Upstream-Status = upstream status int.
    - gateway itself failed (bad proxy, timeout, unreachable, any exception)
      -> HTTP 502, header X-Gateway-Error: <reason>, tiny body. NEVER 200.

  GET /health   -> HTTP 200, {"status":"ok"}.

The upstream status is carried in a header (never the HTTP status) so a genuine
upstream 403/404 is still delivered to the caller as a 200 gateway response with
X-Upstream-Status set — the caller decides whether to accept it. Only a gateway-
side failure is a 502.

Concurrency: served by a threaded HTTP server (one thread per request), which
comfortably covers the ~6 concurrent image fetches the download dispatcher caps
at. No framework, one third-party dependency (curl_cffi).

The gateway URL is configured in Tsundoku Settings (Impersonate card), NOT via an
environment variable — this service only needs its listen port.
"""

import base64
import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from curl_cffi import requests

# Listen port. Fixed to 8788 (the port the Dockerfile EXPOSEs and compose maps);
# overridable via PORT only for local runs / the self-test.
PORT = int(os.environ.get("PORT", "8788"))

# Default per-request deadline (seconds) for the upstream fetch. An image is a
# single request; a slow CDN behind a proxy still resolves well within this.
DEFAULT_TIMEOUT = 60

# Hop-by-hop / content-encoding headers dropped before the curl_cffi call (GAP-111,
# defense-in-depth — the engine strips these too). curl_cffi's Chrome impersonation
# manages the transport and content-encoding itself, so replaying a caller-set
# Accept-Encoding/Host/Connection/... could mis-decode or misroute the bytes.
# Lowercased for case-insensitive matching; all other headers forward verbatim.
STRIPPED_HEADERS = frozenset({
    "accept-encoding",
    "host",
    "content-length",
    "connection",
    "proxy-connection",
    "transfer-encoding",
    "te",
    "upgrade",
    "keep-alive",
})


class Handler(BaseHTTPRequestHandler):
    """One request handler covering GET /health and POST /fetch."""

    # Quieten the default per-request stderr logging (one line per image fetch
    # would flood the container log); failures are surfaced via X-Gateway-Error.
    def log_message(self, *_args):  # noqa: D401 - stdlib override
        pass

    def do_GET(self):  # noqa: N802 - stdlib naming
        if self.path.split("?", 1)[0] == "/health":
            self._send(200, b'{"status":"ok"}', {"Content-Type": "application/json"})
            return
        self._send(404, b'{"error":"not found"}', {"Content-Type": "application/json"})

    def do_POST(self):  # noqa: N802 - stdlib naming
        if self.path.split("?", 1)[0] != "/fetch":
            self._send(404, b'{"error":"not found"}', {"Content-Type": "application/json"})
            return

        try:
            spec = self._read_json_body()
        except (ValueError, OSError) as exc:
            self._gateway_error(f"bad request body: {exc}")
            return

        url = spec.get("url")
        if not url:
            self._gateway_error("missing url")
            return

        try:
            resp = self._fetch_upstream(spec, url)
        except Exception as exc:  # noqa: BLE001 - any transport/proxy failure is a gateway error
            self._gateway_error(f"{type(exc).__name__}: {exc}")
            return

        content_type = resp.headers.get("content-type", "application/octet-stream")
        self._send(
            200,
            resp.content,
            {
                "Content-Type": content_type,
                "X-Upstream-Status": str(resp.status_code),
            },
        )

    # --- helpers ------------------------------------------------------------

    def _read_json_body(self) -> dict:
        """Read and JSON-decode the request body into a dict."""
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length) if length else b""
        if not raw:
            return {}
        parsed = json.loads(raw)
        if not isinstance(parsed, dict):
            raise ValueError("body must be a JSON object")
        return parsed

    def _fetch_upstream(self, spec: dict, url: str):
        """Perform the impersonated upstream request; raises on any gateway failure."""
        method = (spec.get("method") or "GET").upper()
        # curl_cffi's impersonation owns transport + content-encoding, so drop the hop-by-hop /
        # encoding headers a caller may have set (see STRIPPED_HEADERS) — forwarding them verbatim
        # could mis-decode or misroute the returned bytes. All other headers pass through unchanged.
        headers = {
            k: v
            for k, v in (spec.get("headers") or {}).items()
            if k.lower() not in STRIPPED_HEADERS
        }
        impersonate = spec.get("impersonate") or "chrome"

        body_b64 = spec.get("body_b64")
        data = base64.b64decode(body_b64) if body_b64 else None

        socks = spec.get("socks")
        proxies = {"http": socks, "https": socks} if socks else None

        return requests.request(
            method,
            url,
            headers=headers,
            data=data,
            proxies=proxies,
            impersonate=impersonate,
            timeout=DEFAULT_TIMEOUT,
            # curl follows the source's own redirects to the final image bytes,
            # exactly as a browser would.
            allow_redirects=True,
        )

    def _gateway_error(self, reason: str):
        """Send a 502 with the reason in X-Gateway-Error and a tiny body."""
        self._send(
            502,
            b'{"error":"gateway"}',
            {"Content-Type": "application/json", "X-Gateway-Error": reason},
        )

    def _send(self, status: int, body: bytes, headers: dict):
        """Write one response: status line, headers, Content-Length, and body."""
        self.send_response(status)
        for name, value in headers.items():
            self.send_header(name, value)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def main():
    """Start the threaded HTTP server on PORT and serve forever."""
    server = ThreadingHTTPServer(("0.0.0.0", PORT), Handler)
    print(f"impersonate-gateway listening on :{PORT}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
