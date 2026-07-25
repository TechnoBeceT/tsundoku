# Impersonate gateway — the optional Chrome-fingerprint image-fetch proxy.
#
# A tiny plaintext-HTTP service that fetches one upstream image with a browser
# TLS/JA3 fingerprint (curl_cffi's Chrome impersonation) and returns the raw
# bytes. The engine-host routes fetches through it only for sources whose CDN
# blocks the default (okhttp) fingerprint; its URL is set in Tsundoku Settings,
# never via an environment variable.
FROM python:3.12-slim

WORKDIR /app

# Install the single runtime dependency first so the layer caches across app edits.
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt

COPY app.py ./

EXPOSE 8788

CMD ["python", "app.py"]
