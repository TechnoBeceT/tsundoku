"""One-shot self-test for the impersonate gateway (not shipped in the image).

Starts app.py on a throwaway port, then:
  1. fetches a public https image THROUGH /fetch -> asserts 200 + non-empty bytes
     + X-Upstream-Status 200.
  2. fetches a bogus/unreachable url -> asserts 502 + an X-Gateway-Error header.
  3. hits /health -> asserts 200.
Exits non-zero on any failure.
"""

import http.client
import json
import os
import subprocess
import sys
import time

PORT = 8799
BASE = f"127.0.0.1:{PORT}"


def post_fetch(spec: dict):
    conn = http.client.HTTPConnection(BASE, timeout=90)
    conn.request("POST", "/fetch", body=json.dumps(spec), headers={"Content-Type": "application/json"})
    resp = conn.getresponse()
    body = resp.read()
    hdrs = {k.lower(): v for k, v in resp.getheaders()}
    conn.close()
    return resp.status, hdrs, body


def get_health():
    conn = http.client.HTTPConnection(BASE, timeout=10)
    conn.request("GET", "/health")
    resp = conn.getresponse()
    resp.read()
    status = resp.status
    conn.close()
    return status


def main() -> int:
    env = dict(os.environ, PORT=str(PORT))
    proc = subprocess.Popen([sys.executable, "app.py"], env=env)
    try:
        # Wait for the listener.
        for _ in range(50):
            try:
                if get_health() == 200:
                    break
            except OSError:
                time.sleep(0.1)
        else:
            print("FAIL: server never became healthy")
            return 1

        # 1. A real public image through the gateway.
        status, hdrs, body = post_fetch({
            "url": "https://httpbin.org/image/png",
            "method": "GET",
            "headers": {"Accept": "image/png"},
            "impersonate": "chrome",
        })
        print(f"[fetch image] status={status} upstream={hdrs.get('x-upstream-status')} "
              f"ct={hdrs.get('content-type')} bytes={len(body)}")
        if status != 200 or hdrs.get("x-upstream-status") != "200" or len(body) == 0:
            print("FAIL: image fetch did not return 200 + upstream 200 + bytes")
            return 1
        if not hdrs.get("content-type", "").startswith("image/"):
            print("FAIL: image fetch content-type is not an image")
            return 1

        # 1b. The SAME image, but with a caller-set Accept-Encoding: the gateway strips it so
        #     curl_cffi's impersonation owns content-encoding — the returned bytes must be the
        #     identical raw image, never a still-compressed (mis-decoded) body.
        status2, _hdrs2, body2 = post_fetch({
            "url": "https://httpbin.org/image/png",
            "method": "GET",
            "headers": {"Accept": "image/png", "Accept-Encoding": "gzip, br"},
            "impersonate": "chrome",
        })
        print(f"[fetch image + Accept-Encoding] status={status2} bytes={len(body2)} identical={body2 == body}")
        if status2 != 200 or body2 != body:
            print("FAIL: a forwarded Accept-Encoding corrupted the returned bytes (strip not applied)")
            return 1

        # 2. A bogus url -> gateway error.
        status, hdrs, body = post_fetch({
            "url": "https://nonexistent.invalid-tld-zzz.example/never",
            "method": "GET",
        })
        print(f"[fetch bogus] status={status} gateway_error={hdrs.get('x-gateway-error')!r}")
        if status != 502 or not hdrs.get("x-gateway-error"):
            print("FAIL: bogus fetch did not return 502 + X-Gateway-Error")
            return 1

        print("SELF-TEST PASS")
        return 0
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()


if __name__ == "__main__":
    sys.exit(main())
