package enginehost

import com.fasterxml.jackson.core.JsonParseException
import eu.kanade.tachiyomi.network.HttpException
import eu.kanade.tachiyomi.source.model.FilterList
import eu.kanade.tachiyomi.source.model.MangasPage
import eu.kanade.tachiyomi.source.model.Page
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
import org.junit.jupiter.api.DynamicTest
import org.junit.jupiter.api.TestFactory
import kotlinx.coroutines.CancellationException
import java.io.IOException
import java.util.Collections
import java.util.IdentityHashMap
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull
import kotlin.test.assertSame

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
    private val bareSlug: String? = null,
    private val throwOnBareMangaUrl: Boolean = false,
) : HttpSource() {
    private val retainedSlugs = Collections.synchronizedMap(IdentityHashMap<SManga, String>())
    private val retainedChapters = Collections.newSetFromMap(IdentityHashMap<SChapter, Boolean>())

    override val id: Long = 41L
    override val name: String = "Retained candidate fixture"
    override val lang: String = "en"
    override val supportsLatest: Boolean = false
    override val baseUrl: String = server.url("/").toString().removeSuffix("/")
    override val client: OkHttpClient = OkHttpClient()

    var updateCalls: Int = 0
        private set

    var bareUpdateAttempts: Int = 0
        private set

    val searchQueries = mutableListOf<String>()

    val retainedAddress: String
        get() = server.url("/$selectedSlug").toString()

    val serializedAddress: String
        get() = "/$selectedSlug"

    override fun headersBuilder(): Headers.Builder = Headers.Builder()

    override fun getMangaUrl(manga: SManga): String =
        if (requiresRetainedState) {
            retainedSlugs[manga]?.let { server.url("/$it").toString() }
                ?: if (throwOnBareMangaUrl) {
                    throw IllegalStateException("bare manga URL is unavailable")
                } else {
                    bareSlug?.let { server.url("/$it").toString() }.orEmpty()
                }
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
        if (requiresRetainedState && manga !in retainedSlugs) {
            bareUpdateAttempts++
            throw IllegalStateException("retained search state required")
        }
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
                SChapter.create().apply { url = "/chapter/1"; name = "Chapter 1" }.also(retainedChapters::add).let(::listOf)
            } else {
                emptyList()
            }
        return SMangaUpdate(manga, returnedChapters)
    }

    override suspend fun getPageList(chapter: SChapter): List<Page> {
        if (chapter !in retainedChapters) throw Exception("Refresh Chapter List")
        return listOf(Page(0, chapter.url, "https://pages.fixture/1.jpg"))
    }
}

/**
 * Models an extension whose source address is an opaque key while its browser link is a separate
 * presentation URL. The update entry point consumes the opaque key directly; URL-search is not a
 * valid operation for the synthesized browser-host address.
 */
private class OpaqueExtensionAddressSource(
    private val opaqueAddress: String,
    private val updateFailure: Exception? = null,
    private val hydrationFailure: Exception? = null,
) : HttpSource() {
    private val retainedChapters = Collections.newSetFromMap(IdentityHashMap<SChapter, Boolean>())
    override val id: Long = 42L
    override val name: String = "Opaque extension address fixture"
    override val lang: String = "en"
    override val supportsLatest: Boolean = false
    override val baseUrl: String = "https://fixture.invalid"
    override val client: OkHttpClient = OkHttpClient()

    var hydrationSearchCalls: Int = 0
        private set

    var updateAttempts: Int = 0
        private set

    val updateAddresses = mutableListOf<String>()

    override fun headersBuilder(): Headers.Builder = Headers.Builder()

    override fun getMangaUrl(manga: SManga): String = "$baseUrl/browser/series"

    override suspend fun getSearchManga(
        page: Int,
        query: String,
        filters: FilterList,
    ): MangasPage {
        if (query == "known") {
            return MangasPage(
                listOf(
                    SManga.create().apply {
                        url = opaqueAddress
                        title = "Opaque manga"
                    },
                ),
                false,
            )
        }
        hydrationSearchCalls++
        hydrationFailure?.let { throw it }
        throw Exception("Unsupported URL")
    }

    override suspend fun getMangaUpdate(
        manga: SManga,
        chapters: List<SChapter>,
        fetchDetails: Boolean,
        fetchChapters: Boolean,
    ): SMangaUpdate {
        updateAttempts++
        updateFailure?.let { throw it }
        check(manga.url == opaqueAddress) { "unexpected source address: ${manga.url}" }
        updateAddresses += manga.url
        val returnedChapters =
            if (fetchChapters) {
                SChapter.create().apply { url = "/chapter/1"; name = "Chapter 1" }.also(retainedChapters::add).let(::listOf)
            } else {
                emptyList()
            }
        return SMangaUpdate(manga, returnedChapters)
    }

    override suspend fun getPageList(chapter: SChapter): List<Page> {
        if (chapter !in retainedChapters) throw Exception("Refresh Chapter List")
        return listOf(Page(0, chapter.url, "https://pages.fixture/1.jpg"))
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
            assertEquals(AddressMode.URL_SEARCH, candidate.addressMode)
        }
    }

    @Test
    fun `details and chapters rehydrate retained address before the selected request`() {
        MockWebServer().use { server ->
            server.enqueue(MockResponse())
            server.enqueue(MockResponse())
            server.start()
            val source = RetainedCandidateSource(server)

            val details = SourceCalls.mangaDetails(source, source.serializedAddress, AddressMode.URL_SEARCH)
            val detailRequest = assertNotNull(server.takeRequest(5, TimeUnit.SECONDS))
            val chapters = SourceCalls.chapters(source, details.url, details.title, AddressMode.URL_SEARCH)
            val chapterRequest = assertNotNull(server.takeRequest(5, TimeUnit.SECONDS))

            assertEquals(source.serializedAddress, details.url)
            assertEquals("/selected-slug?sort=desc", detailRequest.path)
            assertEquals("/selected-slug?sort=desc", chapterRequest.path)
            assertEquals(1, chapters.chapters.size)
            assertEquals(AddressMode.URL_SEARCH, details.addressMode)
            assertEquals(AddressMode.URL_SEARCH, chapters.addressMode)
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
            SourceCalls.chapters(source, candidate.url, addressMode = AddressMode.DIRECT)
            val request = assertNotNull(server.takeRequest(5, TimeUnit.SECONDS))

            assertEquals("/stable-address", candidate.url)
            assertEquals(AddressMode.DIRECT, candidate.addressMode)
            assertEquals("/stable-address?sort=desc", request.path)
            assertEquals(listOf("known"), source.searchQueries)
        }
    }

    @Test
    fun `details and chapters hydrate retained state before updating when bare browser url is nonblank`() {
        MockWebServer().use { server ->
            server.enqueue(MockResponse())
            server.enqueue(MockResponse())
            server.start()
            val source = RetainedCandidateSource(server, bareSlug = "generic-browser-path")

            val details = SourceCalls.mangaDetails(source, source.serializedAddress)
            val detailRequest = assertNotNull(server.takeRequest(5, TimeUnit.SECONDS))
            val chapters = SourceCalls.chapters(source, details.url, details.title)
            val chapterRequest = assertNotNull(server.takeRequest(5, TimeUnit.SECONDS))

            assertEquals("/selected-slug?sort=desc", detailRequest.path)
            assertEquals("/selected-slug?sort=desc", chapterRequest.path)
            assertEquals(1, chapters.chapters.size)
            assertEquals(0, source.bareUpdateAttempts)
            assertEquals(2, source.updateCalls)
            assertEquals(AddressMode.URL_SEARCH, details.addressMode)
            assertEquals(AddressMode.URL_SEARCH, chapters.addressMode)
            assertEquals(listOf(source.retainedAddress, source.retainedAddress), source.searchQueries)
        }
    }

    @Test
    fun `details and chapters hydrate when bare browser url throws`() {
        MockWebServer().use { server ->
            server.enqueue(MockResponse())
            server.enqueue(MockResponse())
            server.start()
            val source =
                RetainedCandidateSource(
                    server,
                    rawUrl = "opaque-id",
                    selectedSlug = "opaque-id",
                    throwOnBareMangaUrl = true,
                )

            val details = SourceCalls.mangaDetails(source, "opaque-id")
            val detailRequest = assertNotNull(server.takeRequest(5, TimeUnit.SECONDS))
            val chapters = SourceCalls.chapters(source, details.url, details.title)
            val chapterRequest = assertNotNull(server.takeRequest(5, TimeUnit.SECONDS))

            assertEquals("/opaque-id?sort=desc", detailRequest.path)
            assertEquals("/opaque-id?sort=desc", chapterRequest.path)
            assertEquals(1, chapters.chapters.size)
            assertEquals(2, source.updateCalls)
            assertEquals(AddressMode.URL_SEARCH, details.addressMode)
            assertEquals(AddressMode.URL_SEARCH, chapters.addressMode)
            assertEquals(listOf(source.retainedAddress, source.retainedAddress), source.searchQueries)
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

    @Test
    fun `unknown uncached pages resolve to url-search after rehydrating retained chapter state`() {
        MockWebServer().use { server ->
            server.enqueue(MockResponse())
            server.start()
            val source = RetainedCandidateSource(server)

            val pages = SourceCalls.pages(source, "/chapter/1", source.serializedAddress)
            val updateRequest = assertNotNull(server.takeRequest(5, TimeUnit.SECONDS))

            assertEquals(1, pages.pages.size)
            assertEquals("/selected-slug?sort=desc", updateRequest.path)
            assertEquals(AddressMode.URL_SEARCH, pages.addressMode)
            assertEquals(1, source.updateCalls)
            assertEquals(listOf(source.retainedAddress), source.searchQueries)
        }
    }

    @Test
    fun `known url-search uncached pages rehydrate retained chapter state`() {
        MockWebServer().use { server ->
            server.enqueue(MockResponse())
            server.start()
            val source = RetainedCandidateSource(server)

            val pages =
                SourceCalls.pages(
                    source,
                    "/chapter/1",
                    source.serializedAddress,
                    AddressMode.URL_SEARCH,
                )
            val updateRequest = assertNotNull(server.takeRequest(5, TimeUnit.SECONDS))

            assertEquals(1, pages.pages.size)
            assertEquals("/selected-slug?sort=desc", updateRequest.path)
            assertEquals(AddressMode.URL_SEARCH, pages.addressMode)
            assertEquals(1, source.updateCalls)
            assertEquals(listOf(source.retainedAddress), source.searchQueries)
        }
    }

    @TestFactory
    fun `unknown opaque operations fall back once to the exact direct key and resolve direct`(): List<DynamicTest> =
        listOf(
            Triple(
                "details",
                "survival-supremacy#354",
                { source: OpaqueExtensionAddressSource, address: String ->
                    SourceCalls.mangaDetails(source, address).addressMode
                },
            ),
            Triple(
                "chapters",
                "survival-supremacy#645",
                { source: OpaqueExtensionAddressSource, address: String ->
                    SourceCalls.chapters(source, address).addressMode
                },
            ),
            Triple(
                "uncached pages",
                "survival-supremacy#354",
                { source: OpaqueExtensionAddressSource, address: String ->
                    SourceCalls.pages(source, "/chapter/1", address).addressMode
                },
            ),
        ).map { (operation, address, call) ->
            DynamicTest.dynamicTest(operation) {
                val source = OpaqueExtensionAddressSource(address)

                val resolvedMode = call(source, address)

                assertEquals(AddressMode.DIRECT, resolvedMode)
                assertEquals(1, source.hydrationSearchCalls)
                assertEquals(1, source.updateAttempts)
                assertEquals(listOf(address), source.updateAddresses)
            }
        }

    @Test
    fun `details falls back after an unsupported-url hydration rejection and preserves the direct failure`() {
        val failure = IllegalStateException("source-wide update failure")
        val source = OpaqueExtensionAddressSource("survival-supremacy#354", failure)

        val thrown = assertFailsWith<IllegalStateException> {
            SourceCalls.mangaDetails(source, "survival-supremacy#354")
        }

        assertSame(failure, thrown)
        assertEquals(1, source.hydrationSearchCalls)
        assertEquals(1, source.updateAttempts)
    }

    @Test
    fun `chapters falls back after an unsupported-url hydration rejection and preserves the direct failure`() {
        val failure = IllegalStateException("source-wide update failure")
        val source = OpaqueExtensionAddressSource("survival-supremacy#645", failure)

        val thrown = assertFailsWith<IllegalStateException> {
            SourceCalls.chapters(source, "survival-supremacy#645")
        }

        assertSame(failure, thrown)
        assertEquals(1, source.hydrationSearchCalls)
        assertEquals(1, source.updateAttempts)
    }

    @TestFactory
    fun `unknown resolver propagates every non-compatibility hydration failure without updating`(): List<DynamicTest> =
        listOf(
            "transport" to IOException("hydration transport failure"),
            "http" to HttpException(503),
            "parser" to JsonParseException(null, "hydration parser failure"),
            "cancellation" to CancellationException("hydration cancelled"),
            "exact-message subclass" to object : Exception("Unsupported URL") {},
            "wrapped exact sentinel" to Exception("hydration wrapper", Exception("Unsupported URL")),
        ).map { (failureKind, failure) ->
            DynamicTest.dynamicTest(failureKind) {
                val address = "survival-supremacy#354"
                val source = OpaqueExtensionAddressSource(address, hydrationFailure = failure)

                val thrown = assertFailsWith<Exception> {
                    SourceCalls.mangaDetails(source, address)
                }

                assertSame(failure, thrown)
                assertEquals(1, source.hydrationSearchCalls)
                assertEquals(0, source.updateAttempts)
            }
        }

    @TestFactory
    fun `known url-search resolver propagates provider failures without updating`(): List<DynamicTest> =
        listOf(
            "transport" to IOException("known url-search transport failure"),
            "http" to HttpException(429),
            "parser" to JsonParseException(null, "known url-search parser failure"),
            "cancellation" to CancellationException("known url-search cancelled"),
        ).map { (failureKind, failure) ->
            DynamicTest.dynamicTest(failureKind) {
                val address = "survival-supremacy#645"
                val source = OpaqueExtensionAddressSource(address, hydrationFailure = failure)

                val thrown = assertFailsWith<Exception> {
                    SourceCalls.mangaDetails(source, address, AddressMode.URL_SEARCH)
                }

                assertSame(failure, thrown)
                assertEquals(1, source.hydrationSearchCalls)
                assertEquals(0, source.updateAttempts)
            }
        }

    @Test
    fun `known direct update failure propagates unchanged without hydration`() {
        val failure = IllegalStateException("known direct update failure")
        val address = "survival-supremacy#645"
        val source = OpaqueExtensionAddressSource(address, updateFailure = failure)

        val thrown = assertFailsWith<IllegalStateException> {
            SourceCalls.mangaDetails(source, address, AddressMode.DIRECT)
        }

        assertSame(failure, thrown)
        assertEquals(0, source.hydrationSearchCalls)
        assertEquals(1, source.updateAttempts)
    }

    @TestFactory
    fun `opaque extension addresses use their direct keys for uncached page recovery`(): List<DynamicTest> =
        listOf("survival-supremacy#354", "survival-supremacy#645").map { opaqueAddress ->
            DynamicTest.dynamicTest(opaqueAddress) {
                val source = OpaqueExtensionAddressSource(opaqueAddress)

                val details = SourceCalls.mangaDetails(source, opaqueAddress, AddressMode.DIRECT)
                val chapters = SourceCalls.chapters(source, opaqueAddress, addressMode = AddressMode.DIRECT)
                val pages = SourceCalls.pages(source, "/chapter/1", opaqueAddress, AddressMode.DIRECT)

                assertEquals(1, pages.pages.size)
                assertEquals(0, source.hydrationSearchCalls)
                assertEquals(3, source.updateAttempts)
                assertEquals(listOf(opaqueAddress, opaqueAddress, opaqueAddress), source.updateAddresses)
                assertEquals(AddressMode.DIRECT, details.addressMode)
                assertEquals(AddressMode.DIRECT, chapters.addressMode)
                assertEquals(AddressMode.DIRECT, pages.addressMode)
            }
        }
}
