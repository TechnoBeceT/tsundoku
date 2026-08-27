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
import java.time.Duration
import java.util.concurrent.CompletableFuture
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** A source whose details call occupies its serving thread until the test releases it. */
private class BlockingDetailsSource(
    private val entered: CountDownLatch,
    private val release: CountDownLatch,
) : Source {
    override val id: Long = 1L
    override val name: String = "Blocking Source"
    override val lang: String = "en"
    override val supportsLatest: Boolean = false

    override suspend fun getMangaUpdate(
        manga: SManga,
        chapters: List<SChapter>,
        fetchDetails: Boolean,
        fetchChapters: Boolean,
    ): SMangaUpdate {
        entered.countDown()
        release.await()
        return SMangaUpdate(manga, emptyList())
    }

    override suspend fun getPopularManga(page: Int): MangasPage = error("unused")

    override suspend fun getLatestUpdates(page: Int): MangasPage = error("unused")

    override suspend fun getSearchManga(
        page: Int,
        query: String,
        filters: FilterList,
    ): MangasPage = error("unused")

    override suspend fun getPageList(chapter: SChapter): List<Page> = error("unused")
}

class RpcFrontDoorTest {
    private val client = HttpClient.newHttpClient()
    private val release = CountDownLatch(1)
    private val requests = mutableListOf<CompletableFuture<HttpResponse<String>>>()
    private var server: RpcServer? = null

    @AfterTest
    fun tearDown() {
        release.countDown()
        requests.forEach { request -> runCatching { request.get(5, TimeUnit.SECONDS) } }
        server?.stop()
    }

    /**
     * Removing source/front-door isolation makes all eight blocked source calls occupy the HTTP
     * executor, so this bounded health request times out instead of reaching the direct handler.
     */
    @Test
    fun `health remains responsive while all source workers are blocked`() {
        val entered = CountDownLatch(8)
        val workDir = Files.createTempDirectory("rpc-front-door").toFile()
        val loader = ExtensionLoader(workDir)
        injectSource(loader, BlockingDetailsSource(entered, release))
        val runningServer = RpcServer(loader, ExtensionManager(loader, workDir), port = 0)
        server = runningServer
        runningServer.start()
        val baseUrl = "http://localhost:${boundPort(runningServer)}"

        repeat(8) { index ->
            val request =
                HttpRequest.newBuilder(URI("$baseUrl/manga"))
                    .POST(HttpRequest.BodyPublishers.ofString("""{"sourceId":1,"url":"/series/$index"}"""))
                    .build()
            requests += client.sendAsync(request, HttpResponse.BodyHandlers.ofString())
        }
        assertTrue(entered.await(10, TimeUnit.SECONDS), "all eight source calls must occupy source workers")

        val healthRequest =
            HttpRequest.newBuilder(URI("$baseUrl/health"))
                .timeout(Duration.ofMillis(250))
                .GET()
                .build()
        val health = client.send(healthRequest, HttpResponse.BodyHandlers.ofString())

        assertEquals(200, health.statusCode())
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
