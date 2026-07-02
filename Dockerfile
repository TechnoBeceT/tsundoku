# Tsundoku — all-in-one container image.
#
# This single image bundles everything needed to self-host Tsundoku:
#   - the Go/Echo backend binary (the API + job engine),
#   - the pre-built Nuxt SPA (served as static files by the same binary),
#   - a Java 21 runtime so the EMBEDDED Suwayomi download engine runs inside
#     this same container (no separate Suwayomi container needed).
#
# It is built in three stages so the final runtime image stays small: the Go
# toolchain and the Bun/Node frontend toolchain never ship in the result — only
# their compiled outputs do. The runtime pairs with a Postgres service in
# docker-compose.yml.

# ---- Stage 1: build the Go backend binary ----------------------------------
# The Go module lives in backend/ (module github.com/technobecet/tsundoku).
FROM golang:1.25-bookworm AS builder

WORKDIR /build

# Copy the dependency manifests first and download modules as their own layer,
# so edits to source code don't invalidate the (slow) module-download cache.
COPY backend/go.mod backend/go.sum ./
RUN go mod download

# Now copy the rest of the backend source and compile a static binary.
#   CGO_ENABLED=0 -> fully static, no libc dependency at runtime.
#   -ldflags="-s -w" -> strip symbol/debug tables to shrink the binary.
#   -trimpath -> remove local build paths for reproducibility.
COPY backend/ ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o /tsundoku ./cmd/tsundoku

# ---- Stage 2: build the Nuxt SPA (static export) ---------------------------
# ssr:false in nuxt.config.ts, so `nuxi generate` emits a pure static site to
# .output/public/. The generated API client (app/utils/api/schema.d.ts) is
# already committed, so no backend needs to run during this build.
FROM oven/bun:latest AS frontend

WORKDIR /build

# Install dependencies from the committed lockfile as a cached layer first.
COPY frontend/package.json frontend/bun.lock ./
RUN bun install --frozen-lockfile

# Copy the frontend source and generate the static SPA.
COPY frontend/ ./
RUN bunx nuxi generate

# ---- Stage 3: runtime (Java 21 JRE + the binary + the SPA) -----------------
# eclipse-temurin ships the Java 21 runtime that embedded Suwayomi requires.
FROM eclipse-temurin:21-jre-noble

# tini  -> a tiny init that reaps zombie processes and forwards signals cleanly
#          (important: the Go server spawns a child Java/Suwayomi process).
# gosu  -> drops privileges to the PUID/PGID user without a login shell.
# curl  -> used by the HEALTHCHECK below to probe the /health endpoint.
RUN apt-get update && \
    apt-get install -y --no-install-recommends tini gosu curl && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# WORKDIR matters: the Go server resolves its static SPA directory ("dist")
# relative to the working directory at startup, so the SPA must live at
# /app/dist and the process must start from /app.
WORKDIR /app

# The compiled backend binary.
COPY --from=builder /tsundoku /app/tsundoku

# The static SPA. Nuxt generates to .output/public/; the server serves it from
# ./dist, so land it at /app/dist.
COPY --from=frontend /build/.output/public/ /app/dist/

# The privilege-dropping entrypoint (PUID/PGID handling).
COPY entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

# Container-friendly defaults for embedded-Suwayomi mode:
#   - the manga library lives on the /series volume,
#   - Suwayomi's runtime data (its downloaded JAR + default H2 database) lives
#     on the /config volume so everything persists across container restarts.
# Leaving TSUNDOKU_SUWAYOMI_EXTERNALURL unset keeps Suwayomi in EMBEDDED mode.
ENV TSUNDOKU_STORAGE_FOLDER=/series \
    TSUNDOKU_SUWAYOMI_RUNTIMEDIR=/config/suwayomi

# 9833 = Tsundoku HTTP (API + SPA); 4567 = embedded Suwayomi (optional/debug).
EXPOSE 9833
EXPOSE 4567

# Report health via the unauthenticated /health endpoint. start-period gives the
# server time to migrate the DB and boot on first run before failures count.
HEALTHCHECK --interval=30s --timeout=5s --start-period=40s --retries=3 \
    CMD curl -fsS http://localhost:9833/health || exit 1

# tini is PID 1 so signals and child processes are handled correctly; it then
# runs the entrypoint, which drops privileges and execs the binary.
ENTRYPOINT ["tini", "--", "/app/entrypoint.sh"]
