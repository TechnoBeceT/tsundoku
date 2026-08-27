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
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.CountDownLatch
import java.util.concurrent.ExecutorService
import java.util.concurrent.ThreadPoolExecutor
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertTrue

private const val BUSY_BODY = """{"error":"server busy"}"""
private const val SOURCE_QUEUE_FULL_BODY = """{"message":"source queue full"}"""
private const val SOURCE_TIMEOUT_BODY = """{"message":"source call timed out"}"""
private const val SHUTDOWN_BODY = """{"error":"server shutting down"}"""

private class RecordingDetailsSource(
    private val entered: CountDownLatch? = null,
    private val release: CountDownLatch? = null,
) : Source {
    override val id: Long = 1L
    override val name: String = "Recording Source"
    override val lang: String = "en"
    override val supportsLatest: Boolean = false
    val invokedUrls = ConcurrentLinkedQueue<String>()

    override suspend fun getMangaUpdate(
        manga: SManga,
        chapters: List<SChapter>,
        fetchDetails: Boolean,
        fetchChapters: Boolean,
    ): SMangaUpdate {
        invokedUrls += manga.url
        entered?.countDown()
        release?.await()
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

class RpcAdmissionAndShutdownTest {
    private val client = HttpClient.newHttpClient()

    @Test
    fun `production executor queues have explicit finite bounds`() {
        val executors = RpcExecutors()
        try {
            assertEquals(32, remainingCapacity(executors.frontDoorExecutor))
            assertEquals(128, executors.sourceScheduler.limits.queueCapacity)
            assertEquals(32, remainingCapacity(executors.extensionExecutor))
            assertEquals(32, remainingCapacity(executors.extensionNetworkExecutor))
        } finally {
            executors.close()
        }
        assertTrue(executors.frontDoorExecutor.isTerminated)
        assertTrue(executors.sourceScheduler.isTerminated)
        assertTrue(executors.extensionExecutor.isTerminated)
        assertTrue(executors.extensionNetworkExecutor.isTerminated)
    }

    /** An unbounded HTTP queue accepts this request instead of returning the stable capacity error. */
    @Test
    fun `front door capacity rejection completes once with stable 503`() {
        val executors = testExecutors(frontDoorThreads = 1, frontDoorQueueCapacity = 1)
        val runningRelease = CountDownLatch(1)
        val queuedRelease = CountDownLatch(1)
        val rpc = startServer(executors)
        try {
            occupy(executors.frontDoorExecutor, runningRelease)
            executors.frontDoorExecutor.execute { queuedRelease.await() }
            awaitQueueSize(executors.frontDoorExecutor, 1)

            val response = get(rpc.baseUrl, "/health", timeoutMillis = 1_000)

            assertBusy(response)
            assertEquals(1, queueSize(executors.frontDoorExecutor), "front-door queue must stay at its configured bound")
        } finally {
            runningRelease.countDown()
            queuedRelease.countDown()
            rpc.server.stop()
            executors.close()
        }
    }

    /** An unbounded source queue invokes this tenth call later instead of rejecting it at capacity. */
    @Test
    fun `source capacity rejection completes once without invoking rejected work`() {
        val executors = testExecutors(sourceQueueCapacity = 1)
        val entered = CountDownLatch(2)
        val release = CountDownLatch(1)
        val source = RecordingDetailsSource(entered, release)
        val rpc = startServer(executors, source)
        val running = mutableListOf<CompletableFuture<HttpResponse<String>>>()
        try {
            repeat(2) { index -> running += postAsync(rpc.baseUrl, "/running-$index") }
            assertTrue(entered.await(10, TimeUnit.SECONDS), "the source physical allowance must be occupied")

            val queued = postAsync(rpc.baseUrl, "/queued")
            awaitQueueSize(executors.sourceScheduler, 1)
            val rejected = post(rpc.baseUrl, "/rejected", timeoutMillis = 1_000)

            assertEquals(503, rejected.statusCode())
            assertEquals(SOURCE_QUEUE_FULL_BODY, rejected.body())
            assertEquals(1, queueSize(executors.sourceScheduler), "source queue must stay at its configured bound")
            release.countDown()
            assertEquals(200, queued.get(5, TimeUnit.SECONDS).statusCode())
            running.forEach { assertEquals(200, it.get(5, TimeUnit.SECONDS).statusCode()) }
            assertFalse("/rejected" in source.invokedUrls, "capacity-rejected source work must never run")
        } finally {
            release.countDown()
            running.forEach { runCatching { it.get(5, TimeUnit.SECONDS) } }
            rpc.server.stop()
            executors.close()
        }
    }

    @Test
    fun `source execution deadline completes once with stable 504`() {
        val timer = ManualDeadlineTimer()
        val deadline = SourceCallDeadline(Duration.ofSeconds(150), timer)
        val scheduler =
            SourceScheduler(
                limits = SourceSchedulerLimits(workerCount = 1, perSourceLimit = 1, queueCapacity = 1),
                sourceCallDeadline = deadline,
            )
        val executors = RpcExecutors(sourceScheduler = scheduler)
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        val rpc = startServer(executors, RecordingDetailsSource(entered, release))
        try {
            val request = postAsync(rpc.baseUrl, "/deadline")
            assertTrue(entered.await(5, TimeUnit.SECONDS), "source call did not physically start")

            timer.fireAll()

            val response = request.get(5, TimeUnit.SECONDS)
            assertEquals(504, response.statusCode())
            assertEquals(SOURCE_TIMEOUT_BODY, response.body())
        } finally {
            release.countDown()
            rpc.server.stop()
            executors.close()
            deadline.close()
        }
    }

    /** An unbounded extension queue accepts this second route behind the single writer. */
    @Test
    fun `extension capacity rejection completes once with stable 503`() {
        val executors = testExecutors(extensionQueueCapacity = 1)
        val release = CountDownLatch(1)
        val rpc = startServer(executors)
        try {
            occupy(executors.extensionExecutor, release)
            val queued = getAsync(rpc.baseUrl, "/sources")
            awaitQueueSize(executors.extensionExecutor, 1)

            val rejected = get(rpc.baseUrl, "/sources", timeoutMillis = 1_000)

            assertBusy(rejected)
            assertEquals(1, queueSize(executors.extensionExecutor), "extension queue must stay at its configured bound")
            release.countDown()
            assertEquals(200, queued.get(5, TimeUnit.SECONDS).statusCode())
        } finally {
            release.countDown()
            rpc.server.stop()
            executors.close()
        }
    }

    /** Separate extension workers must not multiply the aggregate accepted backlog. */
    @Test
    fun `extension lanes share one aggregate queue bound`() {
        val executors = testExecutors(extensionQueueCapacity = 1)
        val release = CountDownLatch(1)
        val rpc = startServer(executors)
        try {
            occupy(executors.extensionExecutor, release)
            occupy(executors.extensionNetworkExecutor, release)
            val queued = getAsync(rpc.baseUrl, "/sources")
            awaitQueueSize(executors.extensionExecutor, 1)

            val rejected = get(rpc.baseUrl, "/extensions", timeoutMillis = 1_000)

            assertBusy(rejected)
            assertEquals(
                1,
                queueSize(executors.extensionExecutor) + queueSize(executors.extensionNetworkExecutor),
            )
            release.countDown()
            assertEquals(200, queued.get(5, TimeUnit.SECONDS).statusCode())
        } finally {
            release.countDown()
            rpc.server.stop()
            executors.close()
        }
    }

    /** Stopping the server must terminally complete accepted queued work without owning injection. */
    @Test
    fun `server stop completes an accepted queued exchange and remains idempotent`() {
        val executors = testExecutors(sourceQueueCapacity = 1)
        val sourceBlockersRelease = CountDownLatch(1)
        val source = RecordingDetailsSource()
        val rpc = startServer(executors, source)
        try {
            repeat(2) { occupy(executors.sourceScheduler, 1L, sourceBlockersRelease) }
            val queued = postAsync(rpc.baseUrl, "/queued-at-stop")
            awaitQueueSize(executors.sourceScheduler, 1)

            rpc.server.stop()
            rpc.server.stop()

            val response = queued.get(5, TimeUnit.SECONDS)
            assertShutdown(response)
            assertTrue(source.invokedUrls.isEmpty(), "a queued exchange completed by stop must never invoke source work")
            assertSchedulerOpen(executors.sourceScheduler)
        } finally {
            sourceBlockersRelease.countDown()
            executors.close()
        }
    }

    /** Server lifecycle owns accepted front-door exchanges even when the executor is caller-owned. */
    @Test
    fun `server stop drains an accepted queued injected front door exchange`() {
        val executors = testExecutors(frontDoorThreads = 1, frontDoorQueueCapacity = 1)
        val frontDoorRelease = CountDownLatch(1)
        val source = RecordingDetailsSource()
        val rpc = startServer(executors, source)
        try {
            occupy(executors.frontDoorExecutor, frontDoorRelease)
            val queued = postAsync(rpc.baseUrl, "/queued-at-injected-front-stop")
            awaitQueueSize(executors.frontDoorExecutor, 1)

            rpc.server.stop()

            assertShutdown(queued.get(5, TimeUnit.SECONDS))
            assertFalse(executors.frontDoorExecutor.isShutdown, "RpcServer must not close the injected front-door executor")
            assertSchedulerOpen(executors.sourceScheduler)

            frontDoorRelease.countDown()
            awaitQueueSize(executors.frontDoorExecutor, 0)
            assertTrue(source.invokedUrls.isEmpty(), "a front-door task claimed by stop must not invoke its route later")
        } finally {
            frontDoorRelease.countDown()
            executors.close()
        }
    }

    /** Closing executors must drain accepted front-door exchanges rather than discard their tasks. */
    @Test
    fun `executor close completes an accepted queued front door exchange`() {
        val executors = testExecutors(frontDoorThreads = 1, frontDoorQueueCapacity = 1)
        val runningRelease = CountDownLatch(1)
        val rpc = startServer(executors)
        try {
            occupy(executors.frontDoorExecutor, runningRelease)
            val queued = getAsync(rpc.baseUrl, "/health")
            awaitQueueSize(executors.frontDoorExecutor, 1)

            executors.close()

            assertShutdown(queued.get(5, TimeUnit.SECONDS))
            assertTrue(executors.frontDoorExecutor.isTerminated, "cooperative front-door work must terminate before close returns")
        } finally {
            runningRelease.countDown()
            executors.close()
            rpc.server.stop()
        }
    }

    /** Domain tasks returned by shutdownNow retain their exchange and must be terminally drained. */
    @Test
    fun `executor close completes an accepted queued source exchange`() {
        val executors = testExecutors(sourceQueueCapacity = 1)
        val release = CountDownLatch(1)
        val source = RecordingDetailsSource()
        val rpc = startServer(executors, source)
        try {
            repeat(2) { occupy(executors.sourceScheduler, 1L, release) }
            val queued = postAsync(rpc.baseUrl, "/queued-at-executor-close")
            awaitQueueSize(executors.sourceScheduler, 1)

            executors.close()

            assertShutdown(queued.get(5, TimeUnit.SECONDS))
            assertTrue(source.invokedUrls.isEmpty(), "a drained queued task must never invoke source work")
            assertTrue(executors.sourceScheduler.isTerminated, "cooperative source work must terminate before close returns")
        } finally {
            release.countDown()
            executors.close()
            rpc.server.stop()
        }
    }

    /** Shutdown owns the response even when running source work later returns normally. */
    @Test
    fun `server stop completes a running exchange before normal completion races it`() {
        val executors = testExecutors()
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        val source = RecordingDetailsSource(entered, release)
        val rpc = startServer(executors, source)
        try {
            val running = postAsync(rpc.baseUrl, "/running-at-stop")
            assertTrue(entered.await(5, TimeUnit.SECONDS), "source work must be running before stop")

            rpc.server.stop()
            val response = running.get(5, TimeUnit.SECONDS)
            release.countDown()

            assertShutdown(response)
            assertSchedulerOpen(executors.sourceScheduler)
        } finally {
            release.countDown()
            executors.close()
        }
    }

    private fun testExecutors(
        frontDoorThreads: Int = 4,
        frontDoorQueueCapacity: Int = 64,
        sourceQueueCapacity: Int = 128,
        extensionQueueCapacity: Int = 32,
    ): RpcExecutors =
        RpcExecutors(
            frontDoorThreads = frontDoorThreads,
            frontDoorQueueCapacity = frontDoorQueueCapacity,
            sourceQueueCapacity = sourceQueueCapacity,
            extensionQueueCapacity = extensionQueueCapacity,
        )

    private fun startServer(
        executors: RpcExecutors,
        source: Source? = null,
    ): RunningRpc {
        val workDir = Files.createTempDirectory("rpc-admission").toFile()
        val loader = ExtensionLoader(workDir)
        if (source != null) injectSource(loader, source)
        val server = RpcServer(loader, ExtensionManager(loader, workDir), port = 0, executors = executors)
        server.start()
        return RunningRpc(server, "http://localhost:${boundPort(server)}")
    }

    private fun occupy(
        executor: ExecutorService,
        release: CountDownLatch,
    ) {
        val entered = CountDownLatch(1)
        executor.execute {
            entered.countDown()
            try {
                release.await()
            } catch (_: InterruptedException) {
                Thread.currentThread().interrupt()
            }
        }
        assertTrue(entered.await(5, TimeUnit.SECONDS), "executor task did not start")
    }

    private fun occupy(
        scheduler: SourceScheduler,
        sourceId: Long,
        release: CountDownLatch,
    ) {
        val entered = CountDownLatch(1)
        assertIs<Submission.Accepted<Unit>>(
            scheduler.submit(sourceId) {
                entered.countDown()
                try {
                    release.await()
                } catch (_: InterruptedException) {
                    Thread.currentThread().interrupt()
                }
            },
        )
        assertTrue(entered.await(5, TimeUnit.SECONDS), "scheduled source task did not start")
    }

    private fun awaitQueueSize(
        executor: ExecutorService,
        expected: Int,
    ) {
        val pool = executor as ThreadPoolExecutor
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        while (pool.queue.size != expected && System.nanoTime() < deadline) {
            Thread.sleep(5)
        }
        assertEquals(expected, pool.queue.size, "executor queue did not reach expected size")
    }

    private fun queueSize(executor: ExecutorService): Int = (executor as ThreadPoolExecutor).queue.size

    private fun awaitQueueSize(
        scheduler: SourceScheduler,
        expected: Int,
    ) {
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        while (queueSize(scheduler) != expected && System.nanoTime() < deadline) Thread.sleep(5)
        assertEquals(expected, queueSize(scheduler), "source queue did not reach expected size")
    }

    private fun queueSize(scheduler: SourceScheduler): Int = scheduler.snapshot(java.time.Instant.now()).queued

    private fun assertSchedulerOpen(scheduler: SourceScheduler) {
        assertIs<Submission.Accepted<Unit>>(scheduler.submit(Long.MAX_VALUE) {})
    }

    private fun remainingCapacity(executor: ExecutorService): Int =
        (executor as ThreadPoolExecutor).queue.remainingCapacity()

    private fun postAsync(
        baseUrl: String,
        url: String,
    ): CompletableFuture<HttpResponse<String>> =
        client.sendAsync(mangaRequest(baseUrl, url, timeoutMillis = 5_000), HttpResponse.BodyHandlers.ofString())

    private fun post(
        baseUrl: String,
        url: String,
        timeoutMillis: Long,
    ): HttpResponse<String> = client.send(mangaRequest(baseUrl, url, timeoutMillis), HttpResponse.BodyHandlers.ofString())

    private fun mangaRequest(
        baseUrl: String,
        url: String,
        timeoutMillis: Long,
    ): HttpRequest =
        HttpRequest.newBuilder(URI("$baseUrl/manga"))
            .timeout(Duration.ofMillis(timeoutMillis))
            .POST(HttpRequest.BodyPublishers.ofString("""{"sourceId":1,"url":"$url"}"""))
            .build()

    private fun getAsync(
        baseUrl: String,
        path: String,
    ): CompletableFuture<HttpResponse<String>> =
        client.sendAsync(getRequest(baseUrl, path, timeoutMillis = 5_000), HttpResponse.BodyHandlers.ofString())

    private fun get(
        baseUrl: String,
        path: String,
        timeoutMillis: Long,
    ): HttpResponse<String> = client.send(getRequest(baseUrl, path, timeoutMillis), HttpResponse.BodyHandlers.ofString())

    private fun getRequest(
        baseUrl: String,
        path: String,
        timeoutMillis: Long,
    ): HttpRequest =
        HttpRequest.newBuilder(URI("$baseUrl$path"))
            .timeout(Duration.ofMillis(timeoutMillis))
            .GET()
            .build()

    private fun assertBusy(response: HttpResponse<String>) {
        assertEquals(503, response.statusCode())
        assertEquals(BUSY_BODY, response.body())
    }

    private fun assertShutdown(response: HttpResponse<String>) {
        assertEquals(503, response.statusCode())
        assertEquals(SHUTDOWN_BODY, response.body())
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

    private data class RunningRpc(
        val server: RpcServer,
        val baseUrl: String,
    )
}
