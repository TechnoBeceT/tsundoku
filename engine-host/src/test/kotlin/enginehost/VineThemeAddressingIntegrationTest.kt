package enginehost

import eu.kanade.tachiyomi.source.model.FilterList
import eu.kanade.tachiyomi.source.model.MangasPage
import eu.kanade.tachiyomi.source.model.SChapter
import eu.kanade.tachiyomi.source.model.SManga
import eu.kanade.tachiyomi.source.model.SMangaUpdate
import eu.kanade.tachiyomi.source.online.HttpSource
import okhttp3.Headers
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import java.util.Collections
import java.util.IdentityHashMap
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull

/**
 * Models the current extension contract: search emits an opaque url plus state retained on the
 * returned SManga, while URL search can recreate that state after the candidate crosses RPC.
 * Reference identity stands in for the extension-owned memo so this fixture does not duplicate its
 * private metadata schema.
 */
private class RetainedCandidateSource(
    private val server: MockWebServer,
    private val rawUrl: String = "opaque-id",
    private val selectedSlug: String = "selected-slug",
    private val requiresRetainedState: Boolean = true,
) : HttpSource() {
    private val retainedSlugs = Collections.synchronizedMap(IdentityHashMap<SManga, String>())

    override val id: Long = 41L
    override val name: String = "Retained candidate fixture"
    override val lang: String = "en"
    override val supportsLatest: Boolean = false
    override val baseUrl: String = server.url("/").toString().removeSuffix("/")
    override val client: OkHttpClient = OkHttpClient()

    var updateCalls: Int = 0
        private set

    val searchQueries = mutableListOf<String>()

    val retainedAddress: String
        get() = server.url("/$selectedSlug").toString()

    val serializedAddress: String
        get() = "/$selectedSlug"

    override fun headersBuilder(): Headers.Builder = Headers.Builder()

    override fun getMangaUrl(manga: SManga): String =
        if (requiresRetainedState) {
            retainedSlugs[manga]?.let { server.url("/$it").toString() }.orEmpty()
        } else {
            server.url(manga.url).toString()
        }

    override suspend fun getSearchManga(
        page: Int,
        query: String,
        filters: FilterList,
    ): MangasPage {
        searchQueries += query
        val manga =
            SManga.create().apply {
                url = rawUrl
                title = "Selected manga"
            }
        if (selectedSlug.isNotBlank()) retainedSlugs[manga] = selectedSlug
        return MangasPage(listOf(manga), false)
    }

    override suspend fun getMangaUpdate(
        manga: SManga,
        chapters: List<SChapter>,
        fetchDetails: Boolean,
        fetchChapters: Boolean,
    ): SMangaUpdate {
        updateCalls++
        val requestUrl =
            getMangaUrl(manga)
                .ifBlank { server.url("/").toString() }
                .let { it.toHttpUrl().newBuilder().addQueryParameter("sort", "desc").build() }
        client.newCall(Request.Builder().url(requestUrl).build()).execute().use { response ->
            check(response.isSuccessful)
        }
        val returnedChapters =
            if (fetchChapters) {
                listOf(SChapter.create().apply { url = "/chapter/1"; name = "Chapter 1" })
            } else {
                emptyList()
            }
        return SMangaUpdate(manga, returnedChapters)
    }
}

class VineThemeAddressingIntegrationTest {
    @Test
    fun `search serializes the retained address when raw url cannot reconstruct it`() {
        MockWebServer().use { server ->
            server.start()
            val source = RetainedCandidateSource(server)

            val candidate = SourceCalls.search(source, "known", 1).manga.single()

            assertEquals(source.serializedAddress, candidate.url)
        }
    }

    @Test
    fun `details and chapters rehydrate retained address before the selected request`() {
        MockWebServer().use { server ->
            server.enqueue(MockResponse())
            server.enqueue(MockResponse())
            server.start()
            val source = RetainedCandidateSource(server)

            val details = SourceCalls.mangaDetails(source, source.serializedAddress)
            val detailRequest = assertNotNull(server.takeRequest(5, TimeUnit.SECONDS))
            val chapters = SourceCalls.chapters(source, details.url, details.title)
            val chapterRequest = assertNotNull(server.takeRequest(5, TimeUnit.SECONDS))

            assertEquals(source.serializedAddress, details.url)
            assertEquals("/selected-slug?sort=desc", detailRequest.path)
            assertEquals("/selected-slug?sort=desc", chapterRequest.path)
            assertEquals(1, chapters.chapters.size)
            assertEquals(listOf(source.retainedAddress, source.retainedAddress), source.searchQueries)
        }
    }

    @Test
    fun `ordinary relative address remains unchanged and needs no hydration search`() {
        MockWebServer().use { server ->
            server.enqueue(MockResponse())
            server.start()
            val source =
                RetainedCandidateSource(
                    server,
                    rawUrl = "/stable-address",
                    requiresRetainedState = false,
                )

            val candidate = SourceCalls.search(source, "known", 1).manga.single()
            SourceCalls.chapters(source, candidate.url)
            val request = assertNotNull(server.takeRequest(5, TimeUnit.SECONDS))

            assertEquals("/stable-address", candidate.url)
            assertEquals("/stable-address?sort=desc", request.path)
            assertEquals(listOf("known"), source.searchQueries)
        }
    }

    @Test
    fun `all blank extension addresses fail before network`() {
        MockWebServer().use { server ->
            server.start()
            val source = RetainedCandidateSource(server, rawUrl = "", selectedSlug = "")

            val thrown = assertFailsWith<IllegalArgumentException> {
                SourceCalls.search(source, "known", 1)
            }

            assertEquals("malformed source candidate: missing source address", thrown.message)
            assertEquals(0, source.updateCalls)
            assertEquals(0, server.requestCount)
        }
    }
}
