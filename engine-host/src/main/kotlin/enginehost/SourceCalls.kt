package enginehost

/*
 * SourceCalls bridges the RPC layer to a Mihon source's suspend API. Content is always
 * addressed by a source-relative URL: an SManga/SChapter is reconstructed from just the
 * url (that is all the source needs), so no opaque engine id ever enters the flow.
 *
 * Uses runBlocking to cross the Kotlin suspend boundary — the RPC threads are plain
 * blocking HttpServer threads.
 */

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
import kotlinx.coroutines.runBlocking

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
    ): SearchResponse =
        runBlocking {
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
    ): SearchResponse =
        runBlocking {
            val cat = source as? CatalogueSource ?: error("Source ${source.name} is not a CatalogueSource")
            val result = cat.getPopularManga(page)
            SearchResponse(result.mangas.map { it.toEntryDto(source) }, result.hasNextPage)
        }

    /** Browse the source's latest-updates catalogue; returns url-addressed manga entries. */
    fun latest(
        source: Source,
        page: Int,
    ): SearchResponse =
        runBlocking {
            val cat = source as? CatalogueSource ?: error("Source ${source.name} is not a CatalogueSource")
            val result = cat.getLatestUpdates(page)
            SearchResponse(result.mangas.map { it.toEntryDto(source) }, result.hasNextPage)
        }

    /** Fetch full manga details for a source-relative url. */
    fun mangaDetails(
        source: Source,
        url: String,
    ): MangaDetailsDto =
        runBlocking {
            val seed = SManga.create().apply { this.url = url }
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
    ): ChaptersResponse =
        runBlocking {
            val seed = SManga.create().apply { this.url = url; title = mangaTitle }
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
     */
    fun pages(
        source: Source,
        chapterUrl: String,
    ): PagesResponse =
        runBlocking {
            val seed = SChapter.create().apply { this.url = chapterUrl }
            val pageList = source.getPageList(seed)
            PagesResponse(
                pageList.map { page -> PageDto(index = page.index, url = page.url, imageUrl = page.imageUrl) },
            )
        }

    /**
     * Fetch the raw image bytes + content type for a page or a cover, distinguished by [pageUrl]:
     * blank = COVER, non-blank = reader PAGE.
     *
     * Reader pages reconstruct the source's exact Page(url, imageUrl) and go through
     * [HttpSource.getImage], resolving imageUrl first via getImageUrl (Suwayomi's getTrueImageUrl
     * pattern) when absent — this covers sources whose page.url is an intermediate HTML page.
     *
     * Covers are fetched with a PLAIN GET of [imageUrl] via the source's own client + headers
     * (so the CloudflareInterceptor still supplies cf_clearance), deliberately bypassing
     * [HttpSource.imageRequest] — some extensions override imageRequest to validate a reader-page
     * URL shape (e.g. "The Blank"), and a cover URL never matches that shape.
     */
    fun image(
        source: Source,
        pageUrl: String,
        imageUrl: String?,
    ): Pair<ByteArray, String> =
        runBlocking {
            val http = source as? HttpSource
                ?: error("Source ${source.name} is not an HttpSource; cannot fetch image bytes")
            val response =
                if (pageUrl.isBlank()) {
                    val coverUrl = imageUrl ?: error("cover fetch: imageUrl is required when pageUrl is blank")
                    val request = GET(coverUrl, http.headers)
                    http.client
                        .newCachelessCallWithProgress(request, Page(index = 0, url = "", imageUrl = coverUrl))
                        .awaitSuccess()
                } else {
                    val page = Page(index = 0, url = pageUrl, imageUrl = imageUrl)
                    if (page.imageUrl == null) page.imageUrl = http.getImageUrl(page)
                    http.getImage(page)
                }
            val contentType = response.header("Content-Type") ?: "application/octet-stream"
            val bytes = response.body.bytes()
            bytes to contentType
        }

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

    private fun SManga.toEntryDto(source: Source) =
        MangaEntryDto(url = url, title = title, thumbnailUrl = thumbnail_url, realUrl = realMangaUrl(source, this))

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
