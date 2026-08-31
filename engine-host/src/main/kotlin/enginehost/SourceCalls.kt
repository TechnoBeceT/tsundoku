package enginehost

/*
 * SourceCalls bridges the RPC layer to a Mihon source's suspend API. Content is always
 * addressed by a source-relative URL. Most SManga/SChapter objects are reconstructed from that
 * url directly; sources that retain request state only on search results are rehydrated through
 * their own URL-search path. No opaque engine id ever enters the flow.
 *
 * Uses a caller-cancellable runBlocking job to cross the Kotlin suspend boundary — the source
 * workers are plain blocking threads, while coroutine and OkHttp cancellation still propagate.
 */

import com.fasterxml.jackson.annotation.JsonProperty
import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import enginehost.vendor.ChapterRecognition
import enginehost.vendor.ChapterSanitizer.sanitize
import eu.kanade.tachiyomi.network.GET
import eu.kanade.tachiyomi.network.awaitSuccess
import eu.kanade.tachiyomi.network.newCachelessCallWithProgress
import eu.kanade.tachiyomi.source.CatalogueSource
import eu.kanade.tachiyomi.source.Source
import eu.kanade.tachiyomi.source.model.FilterList
import eu.kanade.tachiyomi.source.model.Page
import eu.kanade.tachiyomi.source.model.SChapter
import eu.kanade.tachiyomi.source.model.SManga
import eu.kanade.tachiyomi.source.online.HttpSource
import io.github.oshai.kotlinlogging.KotlinLogging
import okhttp3.ConnectionPool
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okio.Buffer
import suwayomi.tachidesk.server.serverConfig
import java.util.Base64
import java.util.concurrent.TimeUnit

private val logger = KotlinLogging.logger {}

/** application/json media type for the impersonate-gateway POST body. */
private val jsonMediaType = "application/json".toMediaType()

/** JSON mapper for the impersonate-gateway request body (its own, so it never shares config). */
private val impersonateMapper = jacksonObjectMapper()

/** The narrowly-scoped pre-network memo-refresh signal emitted by keiyoushi sources. */
private fun isRefreshChapterListSignal(error: Throwable): Boolean =
    generateSequence(error) { it.cause }
        .any { it.message?.trim() == "Refresh Chapter List" }

/**
 * Headers stripped before an upstream request is forwarded to the impersonate gateway (GAP-111).
 * Two reasons a header is on this list:
 *  1. Hop-by-hop / content-encoding (accept-encoding, host, connection, ...): curl_cffi's Chrome
 *     impersonation manages the transport and content-encoding itself, so a source that explicitly
 *     set one of these could get mis-decoded or misrouted bytes.
 *  2. Request-side caching headers (cache-control, pragma): a real browser never sends these on an
 *     image GET, so forwarding a source's okhttp-set "Cache-Control: max-age=..." is a bot signal
 *     that DEFEATS the Chrome impersonation — a fingerprint-gating CDN (confirmed: Hive Scans) 403s
 *     the otherwise-perfect fingerprint purely because of them. Stripped here too (defense-in-depth)
 *     so the engine never forwards them even though the gateway also strips them.
 * The strip is deliberately MINIMAL — ONLY transport/encoding + caching headers. Every semantic
 * header (Referer, User-Agent, Accept, Origin, Cookie, sec-*, custom source headers) is forwarded
 * verbatim, because over-stripping starves other sources of headers they genuinely need (confirmed:
 * dropping the UA/Accept set makes Thunder Scans serve an HTML anti-bot page instead of the image).
 * Lowercased for case-insensitive matching.
 */
private val strippedGatewayHeaders = setOf(
    "accept-encoding",
    "host",
    "content-length",
    "connection",
    "proxy-connection",
    "transfer-encoding",
    "te",
    "upgrade",
    "keep-alive",
    "cache-control",
    "pragma",
)

/**
 * The wire body of a `POST /fetch` to the impersonate gateway (GAP-111). Field names match the
 * gateway's Python contract EXACTLY — note [bodyB64] serialises as `body_b64`. [impersonate] is the
 * curl_cffi impersonation target ("chrome").
 */
private data class GatewayFetchRequest(
    val url: String,
    val method: String,
    val headers: Map<String, String>,
    @JsonProperty("body_b64") val bodyB64: String?,
    val socks: String?,
    val impersonate: String = "chrome",
)

/**
 * Reads a possibly-uninitialized `lateinit` [SManga] String field, yielding [fallback] instead of
 * throwing when a details parser legitimately left it unset. A details parser builds a FRESH SManga
 * and only sets the fields it cares about; in the normal Mihon/Suwayomi flow the identity fields
 * (`url`, `title`) are already known, so a parser may never assign them — reading such a `lateinit`
 * throws [UninitializedPropertyAccessException]. Mirrors Suwayomi's `Manga.updateMangaDatabase`,
 * which wraps every parser-return field read in the same guard and falls back to the known identity
 * rather than surfacing the exception (which reaches Tsundoku as an ingest-breaking HTTP 502).
 */
private inline fun lateinitOr(
    fallback: String,
    read: () -> String,
): String =
    try {
        read()
    } catch (_: UninitializedPropertyAccessException) {
        fallback
    }

/** Human-readable label for Mihon's integer manga-status codes. */
private fun statusLabel(status: Int): String =
    when (status) {
        1 -> "ONGOING"
        2 -> "COMPLETED"
        3 -> "LICENSED"
        4 -> "PUBLISHING_FINISHED"
        5 -> "CANCELLED"
        6 -> "ON_HIATUS"
        else -> "UNKNOWN"
    }

object SourceCalls {
    /** Search the source; returns url-addressed manga entries. */
    fun search(
        source: Source,
        query: String,
        page: Int,
        cancellation: SourceCallCancellation = SourceCallCancellation(),
    ): SearchResponse =
        cancellation.run {
            val result = source.getSearchManga(page, query, FilterList())
            SearchResponse(
                manga = result.mangas.map { it.toEntryDto(source) },
                hasNextPage = result.hasNextPage,
            )
        }

    /** Browse the source's popular catalogue; returns url-addressed manga entries. */
    fun popular(
        source: Source,
        page: Int,
        cancellation: SourceCallCancellation = SourceCallCancellation(),
    ): SearchResponse =
        cancellation.run {
            val cat = source as? CatalogueSource ?: error("Source ${source.name} is not a CatalogueSource")
            val result = cat.getPopularManga(page)
            SearchResponse(result.mangas.map { it.toEntryDto(source) }, result.hasNextPage)
        }

    /** Browse the source's latest-updates catalogue; returns url-addressed manga entries. */
    fun latest(
        source: Source,
        page: Int,
        cancellation: SourceCallCancellation = SourceCallCancellation(),
    ): SearchResponse =
        cancellation.run {
            val cat = source as? CatalogueSource ?: error("Source ${source.name} is not a CatalogueSource")
            val result = cat.getLatestUpdates(page)
            SearchResponse(result.mangas.map { it.toEntryDto(source) }, result.hasNextPage)
        }

    /** Fetch full manga details for a source-relative url. */
    fun mangaDetails(
        source: Source,
        url: String,
        cancellation: SourceCallCancellation = SourceCallCancellation(),
    ): MangaDetailsDto =
        cancellation.run {
            val seed = source.reconstructManga(url)
            val update = source.getMangaUpdate(seed, emptyList(), fetchDetails = true, fetchChapters = false)
            // A details parser returns a fresh SManga and may never set the `lateinit` identity `url`
            // (already known in the normal Mihon/Suwayomi flow). Re-seed it with the requested url —
            // the requested url IS the identity — so the toDetailsDto url read AND getMangaUrl below
            // cannot throw UninitializedPropertyAccessException (the Flame Comics / Manhuascan.us 502).
            update.manga.url = url
            update.manga.toDetailsDto(url, source)
        }

    /**
     * Fetch the chapter list for a source-relative manga url, running Suwayomi's own
     * service-layer chapter post-processing (Chapter.kt's `updateChapterListDatabase`) on the raw
     * extension output before returning it — see [SChapter.toChapterDto] for the per-chapter steps.
     * [mangaTitle] (optional; "" when unknown) improves number recognition and is passed to the
     * source's own [HttpSource.prepareNewChapter] hook exactly like Suwayomi does.
     */
    fun chapters(
        source: Source,
        url: String,
        mangaTitle: String = "",
        cancellation: SourceCallCancellation = SourceCallCancellation(),
    ): ChaptersResponse =
        cancellation.run {
            val seed = source.reconstructManga(url, mangaTitle)
            val update = source.getMangaUpdate(seed, emptyList(), fetchDetails = false, fetchChapters = true)
            val http = source as? HttpSource
            // A7 (P2 mapper audit): a source can return the same chapter url twice — dedup BEFORE
            // any other processing, mirroring Chapter.kt:150's `chapters.distinctBy { it.url }`.
            // Keeps the FIRST occurrence (distinctBy's own order guarantee), so this never reorders
            // the list. Low-impact self-healer: chapter_key collapse absorbs most duplicates
            // downstream anyway, but an un-deduped list skews Go's `ProviderIndex` (the ordering
            // fallback for unnumbered chapters) by counting the duplicate.
            val uniqueChapters = update.chapters.distinctBy { it.url }
            ChaptersResponse(
                uniqueChapters.map { chapter ->
                    // I1: a source may override prepareNewChapter to set fields (name/number)
                    // BEFORE recognition runs — mirrors Chapter.kt:172. Deprecated upstream, but
                    // still honored so a source relying on it isn't silently broken here.
                    http?.prepareNewChapter(chapter, seed)
                    chapter.toChapterDto(mangaTitle, http)
                },
            )
        }

    /**
     * Fetch the page list for a source-relative chapter url. Each page is returned as the source's
     * OWN address PAIR ([Page.url], [Page.imageUrl]) verbatim — NO image-URL resolution happens here.
     * Resolution (calling getImageUrl when imageUrl is null) is deferred to [image], which
     * reconstructs the exact Page and fetches the bytes, so the page list stays a cheap metadata call.
     *
     * GAP-109 — bare-seed FIRST, warm-and-match ONLY on failure. The page fetch first calls
     * [Source.getPageList] with a bare [SChapter] reconstructed from [chapterUrl] alone. For the vast
     * majority of sources this succeeds with ZERO extra requests — a url-only seed is everything their
     * getPageList needs. Only the bare attempt's `Refresh Chapter List` signal permits [mangaUrl]
     * (the source-relative SERIES url; "" when unknown) to trigger a series-scoped chapter fetch (the
     * same `fetchChapters=true` [Source.getMangaUpdate] call [chapters] runs). When it yields a chapter
     * whose url equals [chapterUrl], getPageList is retried with that REAL SChapter.
     *
     * The warm path exists for the keiyoushi API-extension family (AsuraScans / HiveScans /
     * VortexScans — all extend `KeiSource`): their getPageList calls `getChapterUrl`, which reads a
     * per-chapter `memo["mangaSlug"]` the source stamps onto each SChapter ONLY during the
     * series-scoped fetch. A bare seed has an empty `memo`, so getChapterUrl throws
     * `Exception("Refresh Chapter List")` PRE-NETWORK — making the bare attempt essentially free even
     * for keiyoushi. Because the series fetch reuses the same getMangaUpdate path [chapters] runs, the
     * matched chapter's url is byte-identical to the [chapterUrl] Tsundoku stored.
     *
     * The warm fetch is authoritative once attempted. Every other bare-seed exception is rethrown
     * unchanged, including source-wide timeout, rate-limit, and challenge errors. A blank [mangaUrl]
     * also rethrows the refresh signal because there is no refresh boundary to consult. With a non-blank
     * manga url, a throwing refresh propagates its own exception unchanged, while a successful refresh
     * with no exact chapter-url match reports that stale offer explicitly. An exact match retries
     * getPageList with the refreshed SChapter INSTANCE so extension-only memo state survives.
     */
    fun pages(
        source: Source,
        chapterUrl: String,
        mangaUrl: String = "",
        cancellation: SourceCallCancellation = SourceCallCancellation(),
    ): PagesResponse =
        cancellation.run {
            val bareSeed = SChapter.create().apply { this.url = chapterUrl }
            val pageList =
                try {
                    source.getPageList(bareSeed)
                } catch (bareError: Exception) {
                    // Only keiyoushi's pre-network memo signal may enter stale-offer recovery. A
                    // genuine source failure must preserve its original source-wide classification.
                    if (!isRefreshChapterListSignal(bareError) || mangaUrl.isBlank()) throw bareError

                    val mangaSeed = source.reconstructManga(mangaUrl)
                    val warmChapter =
                        source
                            .getMangaUpdate(mangaSeed, emptyList(), fetchDetails = false, fetchChapters = true)
                            .chapters
                            .firstOrNull { it.url == chapterUrl }
                            ?: throw NoSuchElementException(
                                "chapter not found in refreshed chapter list: $chapterUrl",
                            )
                    source.getPageList(warmChapter)
                }
            PagesResponse(
                pageList.map { page -> PageDto(index = page.index, url = page.url, imageUrl = page.imageUrl) },
            )
        }

    /**
     * Fetch the raw image bytes + content type for a page or a cover, distinguished by [pageUrl]:
     * blank = COVER, non-blank = reader PAGE.
     *
     * Reader pages reconstruct the source's exact Page(url, imageUrl) and resolve imageUrl first via
     * getImageUrl (Suwayomi's getTrueImageUrl pattern) when absent — this covers sources whose
     * page.url is an intermediate HTML page — then fetch via [HttpSource.imageRequest], the same
     * request [HttpSource.getImage] itself builds.
     *
     * Covers are fetched with a PLAIN GET of [imageUrl] via the source's own headers (so the
     * CloudflareInterceptor still supplies cf_clearance), deliberately bypassing
     * [HttpSource.imageRequest] — some extensions override imageRequest to validate a reader-page
     * URL shape (e.g. "The Blank"), and a cover URL never matches that shape.
     *
     * GAP-110 — FRESH CONNECTION PER IMAGE. Some source image CDNs behind Cloudflare (confirmed:
     * Hive Scans' storage.hivetoon.com) flag a REUSED keep-alive HTTP/2 connection after ~a dozen
     * requests and start returning 403, while serving every request that arrives on a FRESH
     * connection with 200. okhttp pools and reuses one connection across a chapter's sequential
     * page-image fetches, so it gets flagged mid-chapter → the CloudflareInterceptor then hangs ~64s
     * on FlareSolverr (which structurally cannot fetch a raw image) → the chapter stalls forever and
     * the hangs exhaust the engine. Deriving a client with connection pooling DISABLED
     * (maxIdleConnections=0) makes every image request open — and close — its own connection, exactly
     * as curl/a browser do, sidestepping the flag. Cheap: one image is one request, and the pooling
     * win never applied to a per-image path anyway. Applied to BOTH branches — a cover on the same
     * CDN can flag too.
     *
     * GAP-111 — IMPERSONATE GATEWAY FIRST, but only for an OPTED-IN source (GAP-131). A few CDNs
     * (Hive's storage.hivetoon.com is the confirmed one) block a request on its TLS/JA3 fingerprint
     * alone: okhttp is 403'd/stalled while a browser-fingerprinted client (curl_cffi's Chrome
     * impersonation) gets 200. When the pushed [ImpersonateConfig] is enabled AND has a url AND
     * lists THIS source's id, the SAME okhttp request built below (verbatim headers:
     * Referer/User-Agent/cookies) is forwarded to the gateway's `/fetch`, carrying this instance's
     * SOCKS egress so a routed source keeps its VPN. A gateway success (gateway 200 with an upstream
     * 2xx) returns those bytes; ANY other outcome (gateway failure, an upstream non-2xx, a transport
     * error, an exception) logs a concise reason and falls through to the okhttp path below.
     *
     * 🔴 THE TWO PATHS ARE NOT EQUIVALENT — this is why the gateway is per-source and default-off.
     * The fallback is safe for REACHABILITY and silently LOSSY for CONTENT. The gateway client is
     * deliberately built without the source's interceptors (it speaks plain local HTTP to the
     * gateway), and a Mihon extension implements image DESCRAMBLING as an OkHttp interceptor on the
     * source's own client. So a gateway SUCCESS stores raw scrambled tiles, while a gateway FAILURE
     * falls back to okhttp, runs the interceptor, and yields a correct page — which is exactly why
     * the corruption presented as RANDOM images rather than all of them (GAP-131, confirmed live on
     * Comix while only Hive Scans needed the fingerprint). Nothing downstream can detect it: the
     * chapter is marked downloaded, the CBZ is a valid zip, and the images are valid images. Gate a
     * source here ONLY when its CDN genuinely blocks the default fingerprint.
     *
     * A source that is not listed behaves EXACTLY as it did before GAP-111: the gateway client is
     * never touched, no request is sent, nothing is logged. ([impersonateGatewayClient] is a `by
     * lazy` on this object, so once ANY gated source has used it the instance exists process-wide —
     * the guarantee for an ungated source is "never touched", not "never built".)
     */
    fun image(
        source: Source,
        pageUrl: String,
        imageUrl: String?,
        cancellation: SourceCallCancellation = SourceCallCancellation(),
    ): Pair<ByteArray, String> =
        cancellation.run {
            val http = source as? HttpSource
                ?: error("Source ${source.name} is not an HttpSource; cannot fetch image bytes")

            // Build the SAME okhttp request the fallback path runs (a cover GET, or the source's own
            // imageRequest for a page — resolving a null page.imageUrl via getImageUrl first). Both
            // the gateway attempt and the okhttp fallback below use this one request + page.
            val page: Page
            val request: Request
            if (pageUrl.isBlank()) {
                val coverUrl = imageUrl ?: error("cover fetch: imageUrl is required when pageUrl is blank")
                page = Page(index = 0, url = "", imageUrl = coverUrl)
                request = GET(coverUrl, http.headers)
            } else {
                page = Page(index = 0, url = pageUrl, imageUrl = imageUrl)
                if (page.imageUrl == null) page.imageUrl = http.getImageUrl(page)
                request = imageRequestFor(http, page)
            }

            // GAP-111/GAP-131: try the Chrome-fingerprint gateway first, but ONLY for a source the
            // owner opted in. A non-null result is the SUCCESS path (okhttp is never touched); null
            // means fall through — an ungated source, the disabled/blank no-op, or ANY throw on the
            // gated path (config read, SOCKS build, or the fetch itself).
            tryImpersonateGateway(source.id, request, cancellation)?.let { return@run it }

            // Gateway success returned above before source-client selection. Only the okhttp fallback
            // chooses between the opted-in pooled source client and the default fresh client.
            val call = imageClientFor(source.id, http.client).newCachelessCallWithProgress(request, page)
            cancellation.withCallSuspend(call) { retained ->
                retained.awaitSuccess().use { response ->
                    val contentType = response.header("Content-Type") ?: "application/octet-stream"
                    response.body.bytes() to contentType
                }
            }
        }

    /**
     * Selects the fallback image client for [sourceId]. Reuse keeps the
     * source's normal pooled client while every call remains cacheless; Fresh
     * derives the existing no-idle-pool client for the GAP-110 behavior.
     */
    internal fun imageClientFor(sourceId: Long, sourceClient: OkHttpClient): OkHttpClient =
        if (ImageTransportConfig.snapshot().reuses(sourceId)) {
            sourceClient
        } else {
            sourceClient.newBuilder()
                .connectionPool(ConnectionPool(0, 1, TimeUnit.NANOSECONDS))
                .build()
        }

    /**
     * The lazily-built OkHttpClient used ONLY to talk to the local impersonate gateway (GAP-111) —
     * separate from any source client so it carries none of the source's interceptors (the gateway is
     * plain local HTTP). A generous call timeout covers a slow CDN behind a proxy; one image is one
     * request.
     */
    private val impersonateGatewayClient: OkHttpClient by lazy {
        OkHttpClient.Builder()
            .callTimeout(120, TimeUnit.SECONDS)
            .build()
    }

    /**
     * Attempts one image fetch through the impersonate gateway FOR [sourceId], returning the
     * bytes+content-type on success or null on ANY failure (so [image] falls back to okhttp). It
     * never throws: the config snapshot read, the SOCKS-string build AND the gateway fetch all sit
     * INSIDE the `runCatching`, so a throwing config/proxy read falls back to okhttp exactly like a
     * transport error would.
     *
     * [sourceId] is the gate (GAP-131): a source the pushed [ImpersonateConfig] does not list is
     * skipped before anything else happens, which is what preserves its OkHttp interceptor chain —
     * and therefore its image descrambling. See [image]'s doc for why the two paths are not
     * interchangeable.
     *
     * The skip is byte-identical to the pre-GAP-111 okhttp path: when the group is disabled, the url
     * is blank, or this source is not gated, the gateway is skipped entirely (no client built, no
     * request sent, nothing logged) and null is returned so [image] runs okhttp.
     *
     * Marked `internal` so the routing tests can drive it (config set via [ConfigPush.applyImpersonate])
     * against a stub / closed-port gateway, proving the catch actually fires.
     */
    internal fun tryImpersonateGateway(
        sourceId: Long,
        upstream: Request,
        cancellation: SourceCallCancellation = SourceCallCancellation(),
    ): Pair<ByteArray, String>? =
        runCatching {
            val snap = ImpersonateConfig.snapshot()
            if (!snap.allows(sourceId)) return@runCatching null
            fetchViaGateway(impersonateGatewayClient, snap.url, socksProxyString(), upstream, cancellation)
        }.getOrElse { e ->
            cancellation.ensureActive()
            logger.warn { "impersonate gateway fetch failed (${e.javaClass.simpleName}: ${e.message}), falling back to okhttp" }
            null
        }

    /**
     * Extracts {url, method, headers, body} from [upstream] and POSTs them to `<gatewayUrl>/fetch`
     * (with the optional [socks] egress), then interprets the gateway response:
     *   - gateway 200 AND `X-Upstream-Status` in 200..299 → the raw bytes + `Content-Type` (SUCCESS).
     *   - anything else (gateway 502 / `X-Gateway-Error`, a non-2xx upstream status, a missing header)
     *     → null, so the caller falls back to okhttp.
     * Marked `internal` so the routing tests can drive it against a stub HTTP server (no real source).
     */
    internal fun fetchViaGateway(
        client: OkHttpClient,
        gatewayUrl: String,
        socks: String?,
        upstream: Request,
        cancellation: SourceCallCancellation = SourceCallCancellation(),
    ): Pair<ByteArray, String>? {
        val bodyBytes: ByteArray? = upstream.body?.let { body ->
            val buffer = Buffer()
            body.writeTo(buffer)
            buffer.readByteArray()
        }
        // Strip hop-by-hop / content-encoding headers: curl_cffi's impersonation owns transport +
        // content-encoding, so forwarding a source-set Accept-Encoding/Host/Connection/... verbatim
        // could mis-decode or misroute the returned bytes. All other headers pass through unchanged.
        val forwardedHeaders = upstream.headers.toMap()
            .filterKeys { it.lowercase() !in strippedGatewayHeaders }
        val payload = GatewayFetchRequest(
            url = upstream.url.toString(),
            method = upstream.method,
            headers = forwardedHeaders,
            bodyB64 = bodyBytes?.let { Base64.getEncoder().encodeToString(it) },
            socks = socks,
        )
        val req = Request.Builder()
            .url(gatewayUrl.trimEnd('/') + "/fetch")
            .post(impersonateMapper.writeValueAsBytes(payload).toRequestBody(jsonMediaType))
            .build()
        val call = client.newCall(req)
        return cancellation.withCall(call) { retained ->
            retained.execute().use { resp ->
                if (resp.code != 200) return@withCall null
                val upstreamStatus = resp.header("X-Upstream-Status")?.toIntOrNull() ?: return@withCall null
                if (upstreamStatus !in 200..299) return@withCall null
                val contentType = resp.header("Content-Type") ?: "application/octet-stream"
                resp.body.bytes() to contentType
            }
        }
    }

    /**
     * The SOCKS proxy string for THIS engine-host instance's `serverConfig.socksProxy*` — the egress
     * passed through to the impersonate gateway so a per-source-VPN-bound instance keeps its egress
     * (per-source SOCKS is per-instance, so reading this process's config is correct). Returns null
     * when SOCKS is disabled or has no host (the gateway then goes direct).
     */
    private fun socksProxyString(): String? =
        buildSocksString(
            enabled = serverConfig.socksProxyEnabled.value,
            version = serverConfig.socksProxyVersion.value,
            host = serverConfig.socksProxyHost.value,
            port = serverConfig.socksProxyPort.value,
            username = serverConfig.socksProxyUsername.value,
            password = serverConfig.socksProxyPassword.value,
        )

    /**
     * Builds a `socks<version>://[user:pass@]host:port` string, or null when [enabled] is false or
     * [host] is blank. Credentials are included only when [username] is non-blank. Marked `internal`
     * so it can be unit-tested without touching `serverConfig`.
     */
    internal fun buildSocksString(
        enabled: Boolean,
        version: Int,
        host: String,
        port: String,
        username: String,
        password: String,
    ): String? {
        if (!enabled || host.isBlank()) return null
        val auth = if (username.isNotBlank()) "$username:$password@" else ""
        return "socks$version://$auth$host:$port"
    }

    /**
     * The source's own image [Request] for [page] — the exact request [HttpSource.getImage] builds,
     * so a per-source `imageRequest` override (a custom Referer, a POST, an alternate url) is honored
     * verbatim. That method is `protected` on [HttpSource], so it is reached reflectively; virtual
     * dispatch still routes to the concrete extension's override. Needed so [image] can run the
     * request on a fresh-connection client (GAP-110) while otherwise staying byte-identical to what
     * getImage would have sent.
     */
    private fun imageRequestFor(
        http: HttpSource,
        page: Page,
    ): Request =
        HttpSource::class.java
            .getDeclaredMethod("imageRequest", Page::class.java)
            .apply { isAccessible = true }
            .invoke(http, page) as Request

    /**
     * Resolves the fully-qualified, browser-clickable url for [manga] via
     * [HttpSource.getMangaUrl] — the "realUrl" the DTOs carry alongside the source-relative
     * addressing [SManga.url]. Only an [HttpSource] exposes this call; any other [Source]
     * (or a source whose request-building throws, e.g. a malformed seed url) yields null,
     * never a thrown exception into the RPC handler.
     */
    private fun realMangaUrl(
        source: Source,
        manga: SManga,
    ): String? = (source as? HttpSource)?.let { http -> runCatching { http.getMangaUrl(manga) }.getOrNull() }

    /**
     * Rebuild a manga object after its serialized address crosses RPC. Most extensions can recreate
     * their request from [address] alone. A source may instead keep request-critical state on the
     * search-result object; when a bare object cannot reproduce the retained address, the source's
     * standard URL-search path is the compatibility boundary that restores the exact extension-owned
     * object. No source identity or URL-path convention is inspected here.
     */
    private suspend fun Source.reconstructManga(
        address: String,
        title: String = "",
    ): SManga {
        require(address.isNotBlank()) { "malformed source candidate: missing source address" }
        val bare = SManga.create().apply { url = address; this.title = title }
        if (this !is HttpSource) return bare
        val absoluteAddress = absoluteAddress(address) ?: return bare
        if (realMangaUrl(this, bare) == absoluteAddress) {
            return bare
        }

        val hydrated =
            getSearchManga(1, absoluteAddress, FilterList()).mangas
                .firstOrNull { realMangaUrl(this, it) == absoluteAddress }
                ?: throw NoSuchElementException("source candidate not found for address: $address")
        hydrated.title = title
        return hydrated
    }

    /** Resolve either an absolute or source-relative [address] without assuming a source path shape. */
    private fun HttpSource.absoluteAddress(address: String): String? =
        address.toHttpUrlOrNull()?.toString()
            ?: runCatching { baseUrl.toHttpUrlOrNull()?.resolve(address)?.toString() }.getOrNull()

    /**
     * Keep a same-origin real URL source-relative on the wire. Cross-origin addresses remain
     * absolute because resolving them against [HttpSource.baseUrl] would change their identity.
     */
    private fun Source.serializedRealAddress(realUrl: String): String {
        val http = this as? HttpSource ?: return realUrl
        val absolute = realUrl.toHttpUrlOrNull() ?: return realUrl
        val base = runCatching { http.baseUrl.toHttpUrlOrNull() }.getOrNull() ?: return realUrl
        if (absolute.scheme != base.scheme || absolute.host != base.host || absolute.port != base.port) return realUrl
        return buildString {
            append(absolute.encodedPath)
            absolute.encodedQuery?.let { append('?').append(it) }
            absolute.encodedFragment?.let { append('#').append(it) }
        }
    }

    /**
     * Select the stable serialized address supplied by the extension. Prefer its normal source url
     * whenever a freshly reconstructed SManga produces the same real URL. If request-critical state
     * exists only on the search object, retain the extension's real URL instead; [reconstructManga]
     * can feed that address through the extension's own URL-search path after RPC serialization.
     */
    private fun SManga.sourceAddress(
        source: Source,
        realUrl: String?,
    ): String {
        val sourceUrl = lateinitOr("") { url }
        if (sourceUrl.isBlank() && realUrl.isNullOrBlank()) {
            throw IllegalArgumentException("malformed source candidate: missing source address")
        }
        if (sourceUrl.isBlank()) return source.serializedRealAddress(realUrl!!)
        if (realUrl.isNullOrBlank()) return sourceUrl

        val bare = SManga.create().apply { url = sourceUrl }
        return if (realMangaUrl(source, bare) == realUrl) sourceUrl else source.serializedRealAddress(realUrl)
    }

    private fun SManga.toEntryDto(source: Source): MangaEntryDto {
        val realUrl = realMangaUrl(source, this)
        return MangaEntryDto(
            url = sourceAddress(source, realUrl),
            title = title,
            thumbnailUrl = thumbnail_url,
            realUrl = realUrl,
        )
    }

    private fun SManga.toDetailsDto(
        requestedUrl: String,
        source: Source,
    ) = MangaDetailsDto(
        url = url.ifBlank { requestedUrl },
        // `title` is also a lateinit — a details parser that omits it would throw identically to the
        // `url` case, so read it defensively and fall back to "" (Suwayomi's own fallback; Tsundoku's
        // canonical series title is set on the Go side and is never sourced from this field).
        title = lateinitOr("") { title },
        author = author,
        artist = artist,
        description = description,
        genres = getGenres().orEmpty(),
        status = statusLabel(status),
        thumbnailUrl = thumbnail_url,
        realUrl = realMangaUrl(source, this),
    )

    /**
     * Maps a raw extension [SChapter] to the wire [ChapterDto], applying the THREE Suwayomi
     * Chapter.kt post-processing steps engine-host must mirror (C1/C2/I2 in the P2 mapper audit):
     *  - [ChapterRecognition.parseChapterNumber] (C1): derives a real chapter number from the
     *    chapter NAME when the extension left `chapter_number` at Mihon's -1 "unset" sentinel (or
     *    Suwayomi's own -2 "hidden" sentinel is passed through unchanged) — this is what keeps a
     *    number-less source keyed by NUMBER instead of NAME downstream in Tsundoku, so it dedups
     *    and sorts correctly against every other source. The result is a Double/float DECIMAL
     *    (e.g. 10.5 for "Chapter 10.5") and is never rounded — fractional chapters must survive.
     *  - [ChapterSanitizer.sanitize] (C2): strips the manga title + surrounding separator/
     *    whitespace chars from the chapter name (Chapter.kt:177, `chapter.name = chapter.name
     *    .sanitize(...)`) — e.g. "One Piece - Chapter 5" -> "Chapter 5" for a title "One Piece",
     *    so Tsundoku's displayed chapter name matches Suwayomi's, not the raw source name.
     *    🔴 ORDER IS LOAD-BEARING: this runs AFTER parseChapterNumber, which needs the RAW,
     *    unsanitized name — sanitize can strip text the recognizer keys off (e.g. the manga
     *    title itself, when it embeds a number) and would change the recognized number if run
     *    first. Mirrors Chapter.kt:171-183 exactly; do not reorder.
     *  - scanlator blank/whitespace normalization (I2): `ifBlank { null }?.trim()`, so a padded or
     *    whitespace-only scanlator never drifts against Tsundoku's EqualFold provider matching.
     * `prepareNewChapter` (I1) runs BEFORE this, in [chapters], since it needs the SManga seed.
     */
    private fun SChapter.toChapterDto(
        mangaTitle: String,
        http: HttpSource?,
    ): ChapterDto {
        val recognizedNumber = ChapterRecognition.parseChapterNumber(mangaTitle, name, chapter_number.toDouble())
        return ChapterDto(
            url = url,
            name = name.sanitize(mangaTitle),
            number = recognizedNumber.toFloat(),
            scanlator = scanlator?.ifBlank { null }?.trim(),
            uploadDate = date_upload,
            realUrl = http?.let { runCatching { it.getChapterUrl(this) }.getOrNull() },
        )
    }
}
