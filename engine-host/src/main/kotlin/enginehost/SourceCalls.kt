package enginehost

/*
 * SourceCalls bridges the RPC layer to a Mihon source's suspend API. Content is always
 * addressed by a source-relative URL: an SManga/SChapter is reconstructed from just the
 * url (that is all the source needs), so no opaque engine id ever enters the flow.
 *
 * Uses runBlocking to cross the Kotlin suspend boundary — the RPC threads are plain
 * blocking HttpServer threads.
 */

import eu.kanade.tachiyomi.source.CatalogueSource
import eu.kanade.tachiyomi.source.Source
import eu.kanade.tachiyomi.source.model.FilterList
import eu.kanade.tachiyomi.source.model.Page
import eu.kanade.tachiyomi.source.model.SChapter
import eu.kanade.tachiyomi.source.model.SManga
import eu.kanade.tachiyomi.source.online.HttpSource
import kotlinx.coroutines.runBlocking

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
                manga = result.mangas.map { it.toEntryDto() },
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
            SearchResponse(result.mangas.map { it.toEntryDto() }, result.hasNextPage)
        }

    /** Browse the source's latest-updates catalogue; returns url-addressed manga entries. */
    fun latest(
        source: Source,
        page: Int,
    ): SearchResponse =
        runBlocking {
            val cat = source as? CatalogueSource ?: error("Source ${source.name} is not a CatalogueSource")
            val result = cat.getLatestUpdates(page)
            SearchResponse(result.mangas.map { it.toEntryDto() }, result.hasNextPage)
        }

    /** Fetch full manga details for a source-relative url. */
    fun mangaDetails(
        source: Source,
        url: String,
    ): MangaDetailsDto =
        runBlocking {
            val seed = SManga.create().apply { this.url = url }
            val update = source.getMangaUpdate(seed, emptyList(), fetchDetails = true, fetchChapters = false)
            update.manga.toDetailsDto(url)
        }

    /** Fetch the chapter list for a source-relative manga url. */
    fun chapters(
        source: Source,
        url: String,
    ): ChaptersResponse =
        runBlocking {
            val seed = SManga.create().apply { this.url = url }
            val update = source.getMangaUpdate(seed, emptyList(), fetchDetails = false, fetchChapters = true)
            ChaptersResponse(update.chapters.map { it.toChapterDto() })
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
     * Fetch the raw image bytes + content type for a page, reconstructing the source's exact
     * Page(url, imageUrl). If imageUrl is absent, resolve it via getImageUrl (Suwayomi's
     * getTrueImageUrl pattern) — this covers sources whose page.url is an intermediate HTML page.
     */
    fun image(
        source: Source,
        pageUrl: String,
        imageUrl: String?,
    ): Pair<ByteArray, String> =
        runBlocking {
            val http = source as? HttpSource
                ?: error("Source ${source.name} is not an HttpSource; cannot fetch image bytes")
            val page = Page(index = 0, url = pageUrl, imageUrl = imageUrl)
            if (page.imageUrl == null) page.imageUrl = http.getImageUrl(page)
            val response = http.getImage(page)
            val contentType = response.header("Content-Type") ?: "application/octet-stream"
            val bytes = response.body.bytes()
            bytes to contentType
        }

    private fun SManga.toEntryDto() = MangaEntryDto(url = url, title = title, thumbnailUrl = thumbnail_url)

    private fun SManga.toDetailsDto(requestedUrl: String) =
        MangaDetailsDto(
            url = url.ifBlank { requestedUrl },
            title = title,
            author = author,
            artist = artist,
            description = description,
            genres = getGenres().orEmpty(),
            status = statusLabel(status),
            thumbnailUrl = thumbnail_url,
        )

    private fun SChapter.toChapterDto() =
        ChapterDto(
            url = url,
            name = name,
            number = chapter_number,
            scanlator = scanlator,
            uploadDate = date_upload,
        )
}
