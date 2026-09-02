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
import java.util.concurrent.TimeoutException
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** A source whose details call occupies its serving thread until the test releases it. */
private class BlockingDetailsSource(
    private val entered: CountDownLatch,
    private val release: CountDownLatch,
) : Source {
    private val running = AtomicInteger()
    val thirdEntered = CountDownLatch(1)
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
        val nowRunning = running.incrementAndGet()
        if (nowRunning >= 3) thirdEntered.countDown()
        entered.countDown()
        try {
            release.await()
            return SMangaUpdate(manga, emptyList())
        } finally {
            running.decrementAndGet()
        }
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
        val entered = CountDownLatch(2)
        val workDir = Files.createTempDirectory("rpc-front-door").toFile()
        val loader = ExtensionLoader(workDir)
        val source = BlockingDetailsSource(entered, release)
        injectSource(loader, source)
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
        assertTrue(entered.await(10, TimeUnit.SECONDS), "two source calls must occupy their physical allowance")
        assertFalse(source.thirdEntered.await(250, TimeUnit.MILLISECONDS), "one source occupied a third physical worker")

        val healthRequest =
            HttpRequest.newBuilder(URI("$baseUrl/health"))
                .timeout(Duration.ofMillis(250))
                .GET()
                .build()
        val health = client.send(healthRequest, HttpResponse.BodyHandlers.ofString())

        assertEquals(200, health.statusCode())
    }

    /** `/image` must share the same keyed allowance as the JSON source routes. */
    @Test
    fun `image work queues behind two physical calls from the same source`() {
        val entered = CountDownLatch(2)
        val workDir = Files.createTempDirectory("rpc-image-bulkhead").toFile()
        val loader = ExtensionLoader(workDir)
        injectSource(loader, BlockingDetailsSource(entered, release))
        val runningServer = RpcServer(loader, ExtensionManager(loader, workDir), port = 0)
        server = runningServer
        runningServer.start()
        val baseUrl = "http://localhost:${boundPort(runningServer)}"

        repeat(2) { index -> requests += postManga(baseUrl, index) }
        assertTrue(entered.await(5, TimeUnit.SECONDS), "two source calls did not start")

        val imageRequest =
            HttpRequest.newBuilder(URI("$baseUrl/image"))
                .POST(HttpRequest.BodyPublishers.ofString("""{"sourceId":1,"pageUrl":"/page/1"}"""))
                .build()
        val image = client.sendAsync(imageRequest, HttpResponse.BodyHandlers.ofString())
        requests += image

        assertFailsWith<TimeoutException> { image.get(250, TimeUnit.MILLISECONDS) }

        release.countDown()
        assertEquals(502, image.get(5, TimeUnit.SECONDS).statusCode())
    }

    private fun postManga(
        baseUrl: String,
        index: Int,
    ): CompletableFuture<HttpResponse<String>> =
        client.sendAsync(
            HttpRequest.newBuilder(URI("$baseUrl/manga"))
                .POST(HttpRequest.BodyPublishers.ofString("""{"sourceId":1,"url":"/series/$index"}"""))
                .build(),
            HttpResponse.BodyHandlers.ofString(),
        )

    private fun injectSource(
        loader: ExtensionLoader,
        source: Source,
    ) = loader.publishTestSources(listOf(source))

    private fun boundPort(rpc: RpcServer): Int {
        val field = RpcServer::class.java.getDeclaredField("server").apply { isAccessible = true }
        return (field.get(rpc) as HttpServer).address.port
    }
}
