package enginehost

import com.sun.net.httpserver.HttpServer
import eu.kanade.tachiyomi.source.Source
import eu.kanade.tachiyomi.source.model.FilterList
import eu.kanade.tachiyomi.source.model.MangasPage
import eu.kanade.tachiyomi.source.model.Page
import eu.kanade.tachiyomi.source.model.SChapter
import eu.kanade.tachiyomi.source.model.SManga
import eu.kanade.tachiyomi.source.model.SMangaUpdate
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.file.Files
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * Source fixture for the stale-offer RPC boundary: the bare chapter emits the narrow refresh
 * sentinel and the authoritative refreshed list omits the retained chapter URL.
 */
private class StaleOfferPagesSource : Source {
    override val id: Long = 1L
    override val name: String = "Stale Offer Source"
    override val lang: String = "en"
    override val supportsLatest: Boolean = false

    override suspend fun getMangaUpdate(
        manga: SManga,
        chapters: List<SChapter>,
        fetchDetails: Boolean,
        fetchChapters: Boolean,
    ): SMangaUpdate = SMangaUpdate(manga, listOf(SChapter.create().apply { url = "/chapter/current" }))

    override suspend fun getPageList(chapter: SChapter): List<Page> = throw IllegalStateException("Refresh Chapter List")

    override suspend fun getPopularManga(page: Int): MangasPage = error("unused")

    override suspend fun getLatestUpdates(page: Int): MangasPage = error("unused")

    override suspend fun getSearchManga(
        page: Int,
        query: String,
        filters: FilterList,
    ): MangasPage = error("unused")
}

class RpcServerPagesTest {
    private lateinit var server: RpcServer
    private lateinit var baseUrl: String

    @BeforeTest
    fun setUp() {
        val workDir = Files.createTempDirectory("rpc-pages").toFile()
        val loader = ExtensionLoader(workDir)
        injectSource(loader, StaleOfferPagesSource())
        server = RpcServer(loader, ExtensionManager(loader, workDir), port = 0)
        server.start()
        baseUrl = "http://localhost:${boundPort(server)}"
    }

    @AfterTest
    fun tearDown() {
        server.stop()
    }

    /**
     * Changing the stale-offer message or bypassing the RPC error serializer would make the Go
     * download boundary lose its chapter-specific classification.
     */
    @Test
    fun `pages serializes the refreshed-list stale offer message`() {
        val request =
            HttpRequest.newBuilder(URI("$baseUrl/pages"))
                .POST(
                    HttpRequest.BodyPublishers.ofString(
                        """{"sourceId":1,"chapterUrl":"/chapter/stale","mangaUrl":"/series/1"}""",
                    ),
                ).build()

        val response = HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.ofString())

        assertEquals(502, response.statusCode())
        assertTrue(
            response.body().contains("chapter not found in refreshed chapter list: /chapter/stale"),
            "expected stale-offer wire message, got: ${response.body()}",
        )
    }

    private fun injectSource(
        loader: ExtensionLoader,
        source: Source,
    ) {
        val field = ExtensionLoader::class.java.getDeclaredField("sources").apply { isAccessible = true }
        @Suppress("UNCHECKED_CAST")
        val registry = field.get(loader) as MutableMap<Long, Source>
        registry[source.id] = source
    }

    private fun boundPort(rpc: RpcServer): Int {
        val field = RpcServer::class.java.getDeclaredField("server").apply { isAccessible = true }
        return (field.get(rpc) as HttpServer).address.port
    }
}
