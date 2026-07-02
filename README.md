<div align="center">

# 📚 Tsundoku

**A self-hosted manga & manhwa library manager and downloader.**

Discover sources, download chapters, and organize everything into a clean,
[Komga](https://komga.org/)-ready library of CBZ archives with embedded
`ComicInfo.xml` metadata — powered by a [Suwayomi](https://github.com/Suwayomi/Suwayomi-Server)
source engine.

[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Nuxt](https://img.shields.io/badge/Nuxt-4-00DC82?logo=nuxt&logoColor=white)](https://nuxt.com/)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15+-4169E1?logo=postgresql&logoColor=white)](https://www.postgresql.org/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)

</div>

---

## What is Tsundoku?

Tsundoku (積ん読 — *"buying books and letting them pile up"*) is a single-owner
web app for building and maintaining a personal manga/manhwa library. You point
it at online sources, adopt the series you care about, and it keeps them
downloaded, deduplicated, and neatly filed on disk in a format
[Komga](https://komga.org/) (or any CBZ reader) can serve directly.

It is designed around a few firm principles:

- **Chapters are first-class.** Every chapter has a stable identity, so the same
  chapter is never downloaded twice — even when it appears on multiple sources.
- **Quality upgrades actually happen.** When a better source for a chapter
  appears, Tsundoku swaps it in non-destructively.
- **The database is the source of truth.** The on-disk library is a clean,
  rebuildable render; a full library can be reconstructed from the CBZ files and
  their sidecars after a total database loss.
- **Never auto-delete.** Nothing removes your downloaded files or history
  automatically — deletion is always an explicit, owner-initiated action.

## Features

- **Library management** — browse your collection, view per-series detail
  (chapters, sources, download state), toggle *monitored* / *completed*.
- **Discovery & search** — search across all installed sources at once, or
  browse a single source's Popular / Latest catalog.
- **Adopt with ranked sources** — add a series backed by one or more providers,
  ranked by preference; the highest-ranked available source wins per chapter.
- **User-definable categories** — create, rename, reorder, and delete categories;
  each maps to a top-level library folder (the Komga contract).
- **Downloads activity** — cross-library Active / Failed / Queued views with
  owner-driven retry (single chapter or bulk).
- **Source health** — per-source *stale* / *erroring* detection with a live
  "needs attention" badge.
- **Covers & metadata source** — per-series cover art and a selectable
  per-source display name.
- **Runtime settings** — tune download/refresh cadences, retry policy, health
  thresholds, and the extension auto-check interval from the UI, no restart
  required.
- **Suwayomi management** — install / update / uninstall source extensions and
  manage repositories; edit FlareSolverr and SOCKS-proxy settings, all from
  within Tsundoku.
- **Real-time updates** — progress and state changes stream over Server-Sent
  Events; the SPA reflects downloads and refreshes live.
- **Single-owner auth** — claim the installation on first run, then log in with
  a password (HttpOnly session cookie; Bearer tokens also supported).

## How it works

```
┌─────────────┐     same-origin      ┌──────────────────────────┐
│  Nuxt 4 SPA │ ───── HTTP + SSE ───▶ │  Go / Echo backend       │
│  (browser)  │                      │  ┌────────────────────┐  │
└─────────────┘                      │  │ Ent + PostgreSQL   │  │
                                     │  └────────────────────┘  │
                                     │  ┌────────────────────┐  │      GraphQL
   library on disk  ◀── CBZ render ──│  │ Suwayomi client    │──┼────▶ Suwayomi
   <storage>/<Category>/<Series>/    │  └────────────────────┘  │      (embedded
        *.cbz (+ ComicInfo.xml)      └──────────────────────────┘       or external)
```

- The **backend** (Go 1.25 / [Echo](https://echo.labstack.com/) /
  [Ent](https://entgo.io/) / PostgreSQL) owns the library model, schedules
  background download & refresh jobs, and renders chapters to CBZ.
- **Suwayomi** provides the actual source catalog and page fetching. Tsundoku can
  **run and manage an embedded Suwayomi JVM for you**, or point at an **external**
  Suwayomi instance you already host.
- The **frontend** is a static Nuxt 4 SPA served same-origin from the backend.
  Its API client types are generated from the backend's OpenAPI 3.1 spec and
  guarded by a drift check, so the frontend and backend can never silently
  disagree on a contract.
- Output is a standard **CBZ + `ComicInfo.xml`** layout that Komga reads with no
  extra configuration.

## Tech stack

| Layer      | Technology                                                        |
| ---------- | ----------------------------------------------------------------- |
| Backend    | Go 1.25, Echo, Ent, PostgreSQL                                    |
| Source     | Suwayomi-Server (embedded JVM or external), via GraphQL           |
| Frontend   | Nuxt 4 / Vue 3 SPA (SSR-less), reka-ui, Bun, Storybook            |
| Contract   | OpenAPI 3.1 → generated TypeScript client (drift-gated)           |
| Realtime   | Server-Sent Events                                                |
| Output     | CBZ + ComicInfo.xml (Komga-compatible)                            |

## Deploy with Docker (recommended for self-hosting)

The repo ships an **all-in-one image** (Go API + Nuxt SPA + an embedded Suwayomi
engine with Java bundled) plus a `docker-compose.yml` that pairs it with
PostgreSQL — the simplest way to self-host, e.g. in an LXC container.

```bash
git clone git@github.com:TechnoBeceT/tsundoku.git
cd tsundoku
# Edit docker-compose.yml and set TSUNDOKU_AUTH_SECRET to a long random string
# (e.g. `openssl rand -hex 32`). The container refuses to start until you do.
docker compose up -d --build
# Then open http://<host>:9833 and claim the owner account.
```

- Library CBZs are written to `./series`; app + embedded-Suwayomi runtime data
  (including the downloaded engine JAR) persist under `./config`.
- **First boot needs outbound internet** — the embedded Suwayomi engine
  downloads its JAR from GitHub on first start (cached under `./config` after).
- Files are written as `PUID:PGID` (default `1000:1000`) so bind-mounted data
  stays accessible from the host.
- Keep `TSUNDOKU_AUTH_COOKIESECURE=false` for plain-HTTP LAN access; set it
  `true` only when serving behind HTTPS.

To run against an **external** Suwayomi instead of the bundled one, set
`TSUNDOKU_SUWAYOMI_EXTERNALURL` in the compose environment.

## Getting started (from source)

### Prerequisites

- **Go** 1.25+
- **Bun** (for the frontend) — https://bun.sh
- **PostgreSQL** 15+ (a running database Tsundoku can connect to)
- **Java 21+** — *only* if you use the embedded Suwayomi engine. Not needed if
  you point Tsundoku at an external Suwayomi instance.

### 1. Clone

```bash
git clone git@github.com:TechnoBeceT/tsundoku.git
cd tsundoku
```

### 2. Configure

Tsundoku is configured entirely through `TSUNDOKU_`-prefixed environment
variables (nested keys use `_` as the separator). At minimum, set an auth secret
and the database connection, and point the storage folder at your library disk:

```bash
export TSUNDOKU_AUTH_SECRET="a-long-random-string-at-least-16-chars"
export TSUNDOKU_DATABASE_HOST=localhost
export TSUNDOKU_DATABASE_PORT=5432
export TSUNDOKU_DATABASE_USER=tsundoku
export TSUNDOKU_DATABASE_PASSWORD=change-me
export TSUNDOKU_DATABASE_NAME=tsundoku
export TSUNDOKU_STORAGE_FOLDER=/path/to/your/manga/library
```

See the [Configuration reference](#configuration-reference) for all keys and
defaults. The server fails to start (fail-closed) if a required secret is
missing or too short.

### 3. Choose a Suwayomi mode

- **Embedded (default):** leave `TSUNDOKU_SUWAYOMI_EXTERNALURL` unset. Tsundoku
  downloads a pinned Suwayomi release on first run and manages the JVM process
  itself. Requires Java 21+ (override the binary with
  `TSUNDOKU_SUWAYOMI_JAVAPATH` if `java` on your `PATH` is too old).
- **External:** set `TSUNDOKU_SUWAYOMI_EXTERNALURL=http://your-suwayomi:4567`.
  Tsundoku will not launch or manage any process; it just talks to that server.

### 4. Build the frontend into the backend

The backend serves the SPA as static files. Build it once (re-run after frontend
changes):

```bash
cd frontend
bun install
bun run build:app   # builds the SPA into backend/dist/
cd ..
```

### 5. Build & run the backend

```bash
cd backend
go build -o tsundoku ./cmd/tsundoku
./tsundoku
```

The API and web UI are served on `http://localhost:9833` by default
(`TSUNDOKU_SERVER_PORT`). Open it in a browser, **claim** the installation to
create the single owner account, log in, and start adding series.

> **Local / plain-HTTP LAN:** set `TSUNDOKU_AUTH_COOKIESECURE=false` so the
> session cookie is accepted over plain HTTP. Keep it `true` (the default) behind
> HTTPS.

## Configuration reference

| Variable | Description | Default |
| --- | --- | --- |
| `TSUNDOKU_AUTH_SECRET` | HMAC secret for session/Bearer tokens (**required**, ≥16 chars) | — |
| `TSUNDOKU_AUTH_COOKIESECURE` | `Secure` flag on the session cookie | `true` |
| `TSUNDOKU_SERVER_PORT` | HTTP listen port | `9833` |
| `TSUNDOKU_STORAGE_FOLDER` | Absolute path to the on-disk library | `/data/manga` |
| `TSUNDOKU_DATABASE_HOST` / `_PORT` | PostgreSQL host / port | `127.0.0.1` / `5432` |
| `TSUNDOKU_DATABASE_USER` / `_NAME` | PostgreSQL user / database name | `tsundoku` / `tsundoku` |
| `TSUNDOKU_DATABASE_PASSWORD` | PostgreSQL password (**required**) | — |
| `TSUNDOKU_DATABASE_SSLMODE` | libpq SSL mode | `disable` |
| `TSUNDOKU_SUWAYOMI_EXTERNALURL` | External Suwayomi URL; blank ⇒ embedded mode | *(blank)* |
| `TSUNDOKU_SUWAYOMI_JAVAPATH` | Java binary for the embedded engine | `java` |
| `TSUNDOKU_SUWAYOMI_VERSION` | Pinned embedded Suwayomi release | *(pinned)* |
| `TSUNDOKU_JOBS_DOWNLOADINTERVAL` | Download cycle cadence | `15m` |
| `TSUNDOKU_JOBS_REFRESHINTERVAL` | Source refresh cadence | `2h` |
| `TSUNDOKU_JOBS_REFRESHCONCURRENCY` | Parallel source refreshes | `4` |
| `TSUNDOKU_JOBS_MAXRETRIES` | Download retry attempts before parking | `3` |
| `TSUNDOKU_JOBS_RETRYBACKOFF` | Base retry backoff (doubles per attempt, 1h cap) | `1m` |
| `TSUNDOKU_JOBS_EXTENSIONCHECKINTERVAL` | Extension update-check cadence (`0` disables) | `24h` |
| `TSUNDOKU_HEALTH_STALEGRACEDAYS` | Grace period before a source is "stale" | `14` |

Many of the `JOBS_*` and `HEALTH_*` values are also editable at runtime from the
Settings screen (they override the env-provided defaults without a restart).

## Development

### Backend

```bash
cd backend
go build ./...            # compile
go vet ./...              # vet
golangci-lint run ./...   # lint
go test ./...             # unit tests
go test -tags integration ./...   # integration tests (Docker required)
go generate ./internal/ent        # after editing any Ent schema
```

### Frontend

```bash
cd frontend
bun install
bun run dev                    # dev server (proxies /api to the backend)
bun run storybook              # component workshop
bun run test                   # Vitest (composable logic)
bunx vue-tsc --noEmit          # type-check
bun run lint                   # eslint
bun run check:api-drift        # fail if the generated API client is stale
```

The TypeScript API client in `frontend/app/utils/api/` is **generated** from the
backend's OpenAPI spec — never hand-edit it. Run `bun run gen:api` after changing
the spec, and `check:api-drift` gates commits where the two have diverged.

## Project structure

```
backend/
  cmd/tsundoku/        Binary entry point
  internal/
    config/            The single environment-variable boundary
    ent/schema/        Ent entity definitions (generated code elsewhere in ent/)
    handler/<domain>/  Thin HTTP handlers (bind → validate → service → DTO)
    <domain>/          Domain services (series, imports, downloads, category, …)
    suwayomi/          Suwayomi lifecycle: provision, process, client, ingest
    disk/              CBZ render + ComicInfo + library reconcile
    api/               Embedded OpenAPI 3.1 spec
    server/            Echo wiring, static SPA serving, routes
frontend/
  app/                 Nuxt 4 SPA — components, pages, composables, screens
    utils/api/         Generated TypeScript client (do not hand-edit)
```

## License

Released under the [MIT License](./LICENSE).

---

<div align="center">
<sub>Tsundoku is an independent project and is not affiliated with Komga or Suwayomi.</sub>
</div>
