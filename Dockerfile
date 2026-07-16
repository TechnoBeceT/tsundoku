# Tsundoku — all-in-one container image (P1: Suwayomi engine replaced by engine-host).
#
# This single image bundles everything needed to self-host Tsundoku:
#   - the Go/Echo backend binary (the API + job engine),
#   - the pre-built Nuxt SPA (served as static files by the same binary),
#   - the JVM extension-host (engine-host) that loads Mihon extensions and serves
#     url-addressed source calls over HTTP/JSON — REPLACING the embedded Suwayomi
#     engine — with its Chromium (KCEF) runtime BUNDLED for off-screen WebView
#     sources (no Xvfb, no first-run download).
#
# Built in stages so the final runtime image stays small: the Go, Bun/Node, and
# Gradle/JDK toolchains never ship in the result — only their compiled outputs do.
# Pairs with a Postgres service in docker-compose.yml.

# ---- Stage 1: build the Go backend binary ----------------------------------
FROM golang:1.25-bookworm AS builder
WORKDIR /build
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o /tsundoku ./cmd/tsundoku

# ---- Stage 2: build the Nuxt SPA (static export) ---------------------------
FROM oven/bun:latest AS frontend
WORKDIR /build
COPY frontend/package.json frontend/bun.lock ./
RUN bun install --frozen-lockfile
COPY frontend/ ./
RUN bunx nuxi generate

# ---- Stage 3: build the engine-host (Gradle composite over Suwayomi) --------
# Replaces the old "embedded Suwayomi JAR" stage. A JDK-21 toolchain is REQUIRED
# (JDK 17 cannot build AndroidCompat's --release 21 Java sources). The composite
# build needs Suwayomi-Server's own modules, so we clone it at the pinned commit
# and point Gradle at it via -PsuwayomiSrc (no fork, no IKVM).
FROM eclipse-temurin:21-jdk-noble AS engine
ARG SUWAYOMI_REPO=https://github.com/Suwayomi/Suwayomi-Server.git
ARG SUWAYOMI_COMMIT=b0bc8c6fb3cdd050dbbfdeb50a9ee1b0d2cbad45
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /src
RUN git clone "$SUWAYOMI_REPO" suwayomi && \
    cd suwayomi && git checkout "$SUWAYOMI_COMMIT" && git submodule update --init --recursive || true
COPY engine-host/ /src/engine-host/
WORKDIR /src/engine-host
# Foojay auto-provisions the pinned toolchain; -Xmx keeps the Suwayomi :server
# Kotlin compile from OOMing. installDist emits a self-contained launcher + libs.
ENV GRADLE_OPTS="-Dorg.gradle.jvmargs=-Xmx2g"
RUN ./gradlew --no-daemon -PsuwayomiSrc=/src/suwayomi installDist

# ---- Stage 4: bundle the KCEF (Chromium) runtime ---------------------------
# CEFManager downloads a pinned JCEF/Chromium into <dataRoot>/bin/kcef on first
# run. To BUNDLE it (reproducible, offline, no first-run stall) we trigger that
# download once at build time into a fixed path and bake the result in. Needs the
# Chromium native libs even to initialize off-screen.
FROM eclipse-temurin:21-jre-noble AS kcef
RUN apt-get update && apt-get install -y --no-install-recommends \
        curl \
        libxss1 libxext6 libxrender1 libxcomposite1 libxdamage1 \
        libxkbcommon0 libxtst6 libxcursor1 libxi6 libxrandr2 libx11-6 libxcb1 \
        libglib2.0-0t64 libnss3 libdbus-1-3 libpango-1.0-0 \
        libcairo2 libasound2t64 libatk-bridge2.0-0t64 libatk1.0-0t64 \
        libcups2t64 libdrm2 libgbm1 libegl1 libgtk-3-0t64 && \
    rm -rf /var/lib/apt/lists/*
COPY --from=engine /src/engine-host/build/install/tsundoku-engine-host /opt/engine-host
# Off-screen Chromium download-and-extract: run the host with KCEF enabled just
# long enough for CEFManager to fetch + install Chromium into /opt/kcef-runtime,
# then stop. The kcef/ tree is what the runtime image bundles.
ENV TSUNDOKU_ENGINE_DATA=/opt/kcef-runtime
# The gate asserts the actual installed artifacts — CEFManager writes the `release` marker only
# after a successful download+extract, and `cef_server` is the native runtime — NOT a log line
# (log levels are not a stable contract). "Failed to set up CEF" / an early host exit fail fast.
RUN set -e; \
    KCEF=/opt/kcef-runtime/bin/kcef; \
    TSUNDOKU_ENGINE_KCEF=true /opt/engine-host/bin/tsundoku-engine-host > /tmp/kcef.log 2>&1 & \
    HPID=$!; \
    for i in $(seq 1 150); do \
      [ -f "$KCEF/release" ] && [ -f "$KCEF/cef_server" ] && break; \
      grep -q 'Failed to set up CEF' /tmp/kcef.log && { echo "KCEF bundle FAILED"; cat /tmp/kcef.log; exit 1; }; \
      kill -0 "$HPID" 2>/dev/null || { echo "host exited before KCEF ready"; cat /tmp/kcef.log; exit 1; }; \
      sleep 2; \
    done; \
    kill "$HPID" 2>/dev/null || true; \
    { [ -f "$KCEF/release" ] && [ -f "$KCEF/cef_server" ]; } \
      || { echo "KCEF bundle incomplete"; ls -la "$KCEF" 2>/dev/null; cat /tmp/kcef.log; exit 1; }

# ---- Stage 5: runtime (JRE 21 + Go binary + SPA + engine-host + KCEF) -------
FROM eclipse-temurin:21-jre-noble
# tini -> reaps zombies + forwards signals (the Go server + engine-host are children).
# gosu -> drop privileges. curl -> HEALTHCHECK. The lib* set is the Chromium runtime
# KCEF needs. xvfb + dbus-x11/dbus -> CEF/Aura still requires a real (or virtual) X
# display and a D-Bus session even with `--off-screen-rendering-enabled
# --disable-gpu`: without them Chromium's Aura/Ozone platform layer fails to
# initialize ("The platform failed to initialize. Exiting." / "ContentMainRun failed
# with exit code 1"), so every WebView-gated source (Comix et al.) hangs until the
# 2-minute timeout and 502s. Upstream Suwayomi-Server runs its bundled Chromium under
# Xvfb for the same reason — the earlier "NO Xvfb, windowless_rendering_enabled
# removes the X-display requirement" assumption in this stage was wrong and is
# reverted here (entrypoint.sh starts Xvfb + dbus before launching engine-host).
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        tini gosu curl \
        xvfb dbus-x11 dbus \
        libxss1 libxext6 libxrender1 libxcomposite1 libxdamage1 \
        libxkbcommon0 libxtst6 libxcursor1 libxi6 libxrandr2 libx11-6 libxcb1 \
        libglib2.0-0t64 libnss3 libdbus-1-3 libpango-1.0-0 \
        libcairo2 libasound2t64 libatk-bridge2.0-0t64 libatk1.0-0t64 \
        libcups2t64 libdrm2 libgbm1 libegl1 libgtk-3-0t64 && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /tsundoku /app/tsundoku
COPY --from=frontend /build/.output/public/ /app/dist/
COPY --from=engine /src/engine-host/build/install/tsundoku-engine-host /app/engine-host
# The pre-downloaded Chromium runtime (bin/kcef). entrypoint points the engine-host
# data dir's bin/kcef at this bundled tree so there is no first-run download.
COPY --from=kcef /opt/kcef-runtime/bin/kcef /app/kcef-runtime/bin/kcef
COPY entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

# Container defaults. The engine-host owns its extension working-set (installed
# APKs + prefs) on the /config volume; KCEF is enabled (off-screen). The Go server
# is a PURE CLIENT of the engine-host over localhost (P2 repoint complete — Suwayomi
# is gone): the entrypoint starts the host on TSUNDOKU_ENGINE_PORT (7777) and the Go
# server connects to it via TSUNDOKU_ENGINE_URL. TSUNDOKU_ENGINE_RUNTIMEDIR roots the
# Go-side extension-.apk byte cache on the persistent /config volume.
ENV TSUNDOKU_STORAGE_FOLDER=/series \
    TSUNDOKU_ENGINE_DATA=/config/engine \
    TSUNDOKU_ENGINE_PORT=7777 \
    TSUNDOKU_ENGINE_KCEF=true \
    ENGINE_KCEF_BUNDLE=/app/kcef-runtime/bin/kcef \
    TSUNDOKU_ENGINE_URL=http://localhost:7777 \
    TSUNDOKU_ENGINE_RUNTIMEDIR=/config/engine-cache

# 9833 = Tsundoku HTTP (API + SPA); 7777 = engine-host RPC (internal).
EXPOSE 9833
EXPOSE 7777

# The engine-host answers /health on 7777; the Go server answers /health on 9833.
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 \
    CMD curl -fsS http://localhost:7777/health && curl -fsS http://localhost:9833/health || exit 1

ENTRYPOINT ["tini", "--", "/app/entrypoint.sh"]
