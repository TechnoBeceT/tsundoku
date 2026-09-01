package enginehost

import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.fasterxml.jackson.module.kotlin.readValue
import com.sun.net.httpserver.HttpServer
import eu.kanade.tachiyomi.source.model.FilterList
import eu.kanade.tachiyomi.source.model.MangasPage
import eu.kanade.tachiyomi.source.model.Page
import eu.kanade.tachiyomi.source.model.SChapter
import eu.kanade.tachiyomi.source.model.SManga
import eu.kanade.tachiyomi.source.model.SMangaUpdate
import eu.kanade.tachiyomi.source.online.HttpSource
import okhttp3.Headers
import okhttp3.OkHttpClient
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.file.Files
import java.util.Collections
import java.util.IdentityHashMap
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertSame

class AddressModeDtoCompatibilityTest {
    private val mapper = jacksonObjectMapper()

    @Test
    fun `legacy request and response bodies default added address fields`() {
        val manga = mapper.readValue<MangaRequest>("""{"sourceId":11,"url":"opaque"}""")
        val chapters =
            mapper.readValue<ChaptersRequest>(
                """{"sourceId":12,"url":"opaque","mangaTitle":"Title"}""",
            )
        val pages =
            mapper.readValue<PagesRequest>(
                """{"sourceId":13,"chapterUrl":"chapter","mangaUrl":"opaque"}""",
            )
        val entry =
            mapper.readValue<MangaEntryDto>(
                """{"url":"opaque","title":"Title","thumbnailUrl":null,"realUrl":null}""",
            )
        val details =
            mapper.readValue<MangaDetailsDto>(
                """{"url":"opaque","title":"Title","author":null,"artist":null,"description":null,"genres":[],"status":"UNKNOWN","thumbnailUrl":null,"realUrl":null}""",
            )
        val chapterResult = mapper.readValue<ChaptersResponse>("""{"chapters":[]}""")
        val pageResult = mapper.readValue<PagesResponse>("""{"pages":[]}""")

        assertEquals(AddressMode.UNKNOWN, manga.addressMode)
        assertEquals(null, manga.webUrl)
        assertEquals(AddressMode.UNKNOWN, chapters.addressMode)
        assertEquals(null, chapters.webUrl)
        assertEquals(AddressMode.UNKNOWN, pages.addressMode)
        assertEquals(null, pages.webUrl)
        assertEquals(AddressMode.UNKNOWN, entry.addressMode)
        assertEquals(AddressMode.UNKNOWN, details.addressMode)
        assertEquals(AddressMode.UNKNOWN, chapterResult.addressMode)
        assertEquals(AddressMode.UNKNOWN, pageResult.addressMode)
    }

    @Test
    fun `address modes use all three stable wire values and tolerate a future value`() {
        val wireValues =
            listOf(
                AddressMode.UNKNOWN to "unknown",
                AddressMode.DIRECT to "direct",
                AddressMode.URL_SEARCH to "url_search",
            )

        wireValues.forEach { (mode, wire) ->
            assertEquals("\"$wire\"", mapper.writeValueAsString(mode))
            assertSame(mode, mapper.readValue<AddressMode>("\"$wire\""))
        }
        assertSame(AddressMode.UNKNOWN, mapper.readValue<AddressMode>("\"future_mode\""))
    }
}

/**
 * Retains the browser witness on the URL-search result itself. The three RPC routes can reach an
 * update only if they deserialize and forward both `addressMode=url_search` and `webUrl`.
 */
private class RpcAddressModeSource : HttpSource() {
    private val retainedWebUrls = Collections.synchronizedMap(IdentityHashMap<SManga, String>())
    private val retainedChapters = Collections.newSetFromMap(IdentityHashMap<SChapter, Boolean>())

    override val id: Long = 77L
    override val name: String = "RPC address-mode fixture"
    override val lang: String = "en"
    override val supportsLatest: Boolean = false
    override val baseUrl: String = "https://source.fixture"
    override val client: OkHttpClient = OkHttpClient()

    val searchQueries = mutableListOf<String>()
    val updateAddresses = mutableListOf<String>()

    override fun headersBuilder(): Headers.Builder = Headers.Builder()

    override fun getMangaUrl(manga: SManga): String =
        retainedWebUrls[manga] ?: "$baseUrl/bare/${manga.url}"

    override suspend fun getSearchManga(
        page: Int,
        query: String,
        filters: FilterList,
    ): MangasPage {
        searchQueries += query
        val manga =
            SManga.create().apply {
                url = "/extension-owned-key"
                title = "Hydrated manga"
            }
        retainedWebUrls[manga] = query
        return MangasPage(listOf(manga), false)
    }

    override suspend fun getMangaUpdate(
        manga: SManga,
        chapters: List<SChapter>,
        fetchDetails: Boolean,
        fetchChapters: Boolean,
    ): SMangaUpdate {
        check(manga in retainedWebUrls) { "RPC did not forward the URL-search witness" }
        updateAddresses += manga.url
        manga.title = "Hydrated manga"
        val returnedChapters =
            if (fetchChapters) {
                SChapter.create().apply {
                    url = "/chapter/1"
                    name = "Chapter 1"
                    chapter_number = 1F
                }.also(retainedChapters::add).let(::listOf)
            } else {
                emptyList()
            }
        return SMangaUpdate(manga, returnedChapters)
    }

    override suspend fun getPageList(chapter: SChapter): List<Page> {
        if (chapter !in retainedChapters) throw Exception("Refresh Chapter List")
        return listOf(Page(0, chapter.url, "https://images.fixture/1.jpg"))
    }
}

class AddressModeRpcCompatibilityTest {
    private val mapper = jacksonObjectMapper()
    private val client = HttpClient.newHttpClient()
    private lateinit var source: RpcAddressModeSource
    private lateinit var server: RpcServer
    private lateinit var baseUrl: String

    @BeforeTest
    fun setUp() {
        val workDir = Files.createTempDirectory("rpc-address-mode").toFile()
        val loader = ExtensionLoader(workDir)
        source = RpcAddressModeSource()
        injectSource(loader, source)
        server = RpcServer(loader, ExtensionManager(loader, workDir), port = 0)
        server.start()
        baseUrl = "http://localhost:${boundPort(server)}"
    }

    @AfterTest
    fun tearDown() {
        server.stop()
    }

    @Test
    fun `RPC forwards additive address mode and web URL fields on every content route`() {
        val webUrl = "https://browser.fixture/series/77"
        val (manga, chapters, pages) = postAllAddressModeRoutes(webUrl)

        assertEquals(listOf(200, 200, 200), listOf(manga, chapters, pages).map { it.statusCode() })
        assertEquals(listOf(webUrl, webUrl, webUrl), source.searchQueries)
        assertEquals(listOf("/extension-owned-key", "/extension-owned-key", "/extension-owned-key"), source.updateAddresses)
    }

    @Test
    fun `RPC serializes resolved address mode on chapters responses`() {
        val chapters =
            post(
                "/chapters",
                """{"sourceId":77,"url":"opaque-wire-key","mangaTitle":"Hydrated manga","addressMode":"url_search","webUrl":"https://browser.fixture/series/77"}""",
            )

        assertEquals(200, chapters.statusCode())
        assertEquals("url_search", mapper.readTree(chapters.body())["addressMode"].asText())
        assertEquals("/chapter/1", mapper.readTree(chapters.body())["chapters"][0]["url"].asText())
    }

    @Test
    fun `RPC serializes resolved address mode on pages responses`() {
        val pages =
            post(
                "/pages",
                """{"sourceId":77,"chapterUrl":"/chapter/1","mangaUrl":"opaque-wire-key","addressMode":"url_search","webUrl":"https://browser.fixture/series/77"}""",
            )

        assertEquals(200, pages.statusCode())
        assertEquals("url_search", mapper.readTree(pages.body())["addressMode"].asText())
        assertEquals("https://images.fixture/1.jpg", mapper.readTree(pages.body())["pages"][0]["imageUrl"].asText())
    }

    private fun postAllAddressModeRoutes(webUrl: String): List<HttpResponse<String>> =
        listOf(
            post(
                "/manga",
                """{"sourceId":77,"url":"opaque-wire-key","addressMode":"url_search","webUrl":"$webUrl","futureField":"ignored"}""",
            ),
            post(
                "/chapters",
                """{"sourceId":77,"url":"opaque-wire-key","mangaTitle":"Hydrated manga","addressMode":"url_search","webUrl":"$webUrl"}""",
            ),
            post(
                "/pages",
                """{"sourceId":77,"chapterUrl":"/chapter/1","mangaUrl":"opaque-wire-key","addressMode":"url_search","webUrl":"$webUrl"}""",
            ),
        )

    private fun post(
        path: String,
        body: String,
    ): HttpResponse<String> =
        client.send(
            HttpRequest.newBuilder(URI("$baseUrl$path"))
                .POST(HttpRequest.BodyPublishers.ofString(body))
                .build(),
            HttpResponse.BodyHandlers.ofString(),
        )

    private fun injectSource(
        loader: ExtensionLoader,
        source: RpcAddressModeSource,
    ) {
        val field = ExtensionLoader::class.java.getDeclaredField("sources").apply { isAccessible = true }
        @Suppress("UNCHECKED_CAST")
        val registry = field.get(loader) as MutableMap<Long, eu.kanade.tachiyomi.source.Source>
        registry[source.id] = source
    }

    private fun boundPort(rpc: RpcServer): Int {
        val field = RpcServer::class.java.getDeclaredField("server").apply { isAccessible = true }
        return (field.get(rpc) as HttpServer).address.port
    }
}
