package enginehost

import androidx.preference.PreferenceScreen
import androidx.preference.SwitchPreferenceCompat
import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.sun.net.httpserver.HttpServer
import eu.kanade.tachiyomi.App
import eu.kanade.tachiyomi.createAppModule
import eu.kanade.tachiyomi.source.ConfigurableSource
import eu.kanade.tachiyomi.source.Source
import eu.kanade.tachiyomi.source.model.FilterList
import eu.kanade.tachiyomi.source.model.MangasPage
import eu.kanade.tachiyomi.source.model.Page
import eu.kanade.tachiyomi.source.model.SChapter
import eu.kanade.tachiyomi.source.model.SManga
import eu.kanade.tachiyomi.source.model.SMangaUpdate
import kotlinx.coroutines.delay
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import org.koin.core.context.startKoin
import org.koin.dsl.module
import org.junit.jupiter.api.RepeatedTest
import suwayomi.tachidesk.server.ApplicationDirs
import xyz.nulldev.androidcompat.AndroidCompat
import xyz.nulldev.androidcompat.AndroidCompatInitializer
import xyz.nulldev.androidcompat.androidCompatModule
import xyz.nulldev.androidcompat.webkit.KcefWebViewProvider
import xyz.nulldev.ts.config.CONFIG_PREFIX
import xyz.nulldev.ts.config.configManagerModule
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.file.Files
import java.time.Duration
import java.util.concurrent.CompletableFuture
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.io.path.listDirectoryEntries
import kotlin.test.AfterTest
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private class NonCooperativeDetailsSource(
    private val entered: CountDownLatch,
    private val released: AtomicBoolean,
) : Source {
    override val id: Long = 101L
    override val name: String = "Blocked A"
    override val lang: String = "en"
    override val supportsLatest: Boolean = false

    override suspend fun getMangaUpdate(
        manga: SManga,
        chapters: List<SChapter>,
        fetchDetails: Boolean,
        fetchChapters: Boolean,
    ): SMangaUpdate {
        entered.countDown()
        while (!released.get()) {
            try {
                Thread.sleep(25)
            } catch (_: InterruptedException) {
                // Model extension code that deliberately ignores cancellation and interruption.
            }
        }
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

private class HealthyDetailsSource : Source {
    override val id: Long = 202L
    override val name: String = "Healthy B"
    override val lang: String = "en"
    override val supportsLatest: Boolean = false

    override suspend fun getMangaUpdate(
        manga: SManga,
        chapters: List<SChapter>,
        fetchDetails: Boolean,
        fetchChapters: Boolean,
    ): SMangaUpdate =
        SMangaUpdate(
            SManga.create().apply {
                title = "Healthy response"
                author = "Source B"
                description = "response fidelity fixture"
                genre = "Action, Adventure"
            },
            emptyList(),
        )

    override suspend fun getPopularManga(page: Int): MangasPage = error("unused")

    override suspend fun getLatestUpdates(page: Int): MangasPage = error("unused")

    override suspend fun getSearchManga(
        page: Int,
        query: String,
        filters: FilterList,
    ): MangasPage = error("unused")

    override suspend fun getPageList(chapter: SChapter): List<Page> = error("unused")
}

private class CooperativeDetailsSource(
    private val entered: CountDownLatch,
    private val exited: CountDownLatch,
) : Source {
    override val id: Long = 404L
    override val name: String = "Cooperative source"
    override val lang: String = "en"
    override val supportsLatest: Boolean = false

    override suspend fun getMangaUpdate(
        manga: SManga,
        chapters: List<SChapter>,
        fetchDetails: Boolean,
        fetchChapters: Boolean,
    ): SMangaUpdate {
        entered.countDown()
        try {
            delay(5_000)
            return SMangaUpdate(manga, emptyList())
        } finally {
            exited.countDown()
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

private class MutablePreferenceSource : Source, ConfigurableSource {
    override val id: Long = 303L
    override val name: String = "Preference source"
    override val lang: String = "en"
    override val supportsLatest: Boolean = false

    override fun setupPreferenceScreen(screen: PreferenceScreen) {
        screen.addPreference(
            SwitchPreferenceCompat(screen.context).apply {
                key = "enabled"
                title = "Isolation switch"
                defaultValue = false
            },
        )
    }

    override suspend fun getMangaUpdate(
        manga: SManga,
        chapters: List<SChapter>,
        fetchDetails: Boolean,
        fetchChapters: Boolean,
    ): SMangaUpdate = error("unused")

    override suspend fun getPopularManga(page: Int): MangasPage = error("unused")

    override suspend fun getLatestUpdates(page: Int): MangasPage = error("unused")

    override suspend fun getSearchManga(
        page: Int,
        query: String,
        filters: FilterList,
    ): MangasPage = error("unused")

    override suspend fun getPageList(chapter: SChapter): List<Page> = error("unused")
}

private object PreferenceIntegrationTestSetup {
    init {
        val dataRoot = Files.createTempDirectory("preference-integration-runtime")
        System.setProperty("$CONFIG_PREFIX.server.rootDir", dataRoot.toString())
        ServerConfigTestSetup.ensureRegistered()
        AndroidCompatInitializer().init()
        val app = App()
        startKoin {
            modules(
                createAppModule(app),
                androidCompatModule(),
                configManagerModule(),
                module {
                    single { ApplicationDirs(dataRoot = dataRoot.toString()) }
                    single<KcefWebViewProvider.InitBrowserHandler> {
                        object : KcefWebViewProvider.InitBrowserHandler {
                            override fun init(provider: KcefWebViewProvider) = Unit
                        }
                    }
                },
            )
        }
        AndroidCompat().startApp(app)
    }

    fun ensureReady() = Unit
}

class EngineIsolationIntegrationTest {
    private val client = HttpClient.newHttpClient()
    private val mapper = jacksonObjectMapper()
    private val released = AtomicBoolean(false)
    private val blockedRequests = mutableListOf<CompletableFuture<HttpResponse<String>>>()
    private var server: RpcServer? = null

    @AfterTest
    fun tearDown() {
        released.set(true)
        blockedRequests.forEach { request -> runCatching { request.get(5, TimeUnit.SECONDS) } }
        server?.stop()
    }

    /**
     * A realistic non-cooperative source retains both of its physical slots while unrelated
     * source and control-plane requests cross the same public RPC port unchanged.
     */
    @RepeatedTest(20)
    fun `non cooperative source cannot delay healthy source or control responses`() {
        val entered = CountDownLatch(2)
        val root = Files.createTempDirectory("engine-isolation-integration").toFile()
        val loader = ExtensionLoader(root)
        injectSource(loader, NonCooperativeDetailsSource(entered, released))
        injectSource(loader, HealthyDetailsSource())
        val runningServer = RpcServer(loader, ExtensionManager(loader, root), port = 0)
        server = runningServer
        runningServer.start()
        val baseUrl = "http://127.0.0.1:${boundPort(runningServer)}"

        repeat(2) { index ->
            blockedRequests += postAsync(baseUrl, "/manga", """{"sourceId":101,"url":"/blocked/$index"}""")
        }
        assertTrue(entered.await(5, TimeUnit.SECONDS), "both non-cooperative source-A calls must start")

        val started = System.nanoTime()
        val healthy = postAsync(baseUrl, "/manga", """{"sourceId":202,"url":"/healthy/7"}""")
        val health = getAsync(baseUrl, "/health")
        val status = getAsync(baseUrl, "/status")
        val control = getAsync(baseUrl, "/sources")

        val healthyResponse = healthy.get(1, TimeUnit.SECONDS)
        val healthResponse = health.get(1, TimeUnit.SECONDS)
        val statusResponse = status.get(1, TimeUnit.SECONDS)
        val controlResponse = control.get(1, TimeUnit.SECONDS)
        val elapsedMillis = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - started)

        assertTrue(elapsedMillis < 1_000, "isolated responses took ${elapsedMillis}ms")
        assertEquals(200, healthyResponse.statusCode())
        assertEquals(
            mapper.readTree(
                """{"url":"/healthy/7","title":"Healthy response","author":"Source B","artist":null,"description":"response fidelity fixture","genres":["Action","Adventure"],"status":"UNKNOWN","thumbnailUrl":null,"realUrl":null}""",
            ),
            mapper.readTree(healthyResponse.body()),
        )
        assertEquals(200, healthResponse.statusCode())
        assertEquals(
            mapper.readTree("""{"status":"ok","sources":2}"""),
            mapper.readTree(healthResponse.body()),
        )
        assertEquals(200, statusResponse.statusCode())
        val statusJson = mapper.readTree(statusResponse.body())
        assertTrue(
            statusJson["running"].intValue() in 2..3,
            "status must include the two blocked A calls and may observe B before its physical finally",
        )
        assertEquals(8, statusJson["source_workers"].intValue())
        assertEquals(2, statusJson["per_source_limit"].intValue())
        val sourceAStatus = statusJson["busiest_sources"].first { it["source_id"].longValue() == 101L }
        assertEquals(2, sourceAStatus["running"].intValue())
        assertEquals(200, controlResponse.statusCode())
        assertEquals(
            mapper.readTree(
                """[{"id":101,"name":"Blocked A","lang":"en"},{"id":202,"name":"Healthy B","lang":"en"}]""",
            ),
            mapper.readTree(controlResponse.body()),
        )
        assertFalse(blockedRequests.any(CompletableFuture<HttpResponse<String>>::isDone))
    }

    /** A real bounded HTTP transfer stays outside the public preference write path. */
    @RepeatedTest(20)
    fun `slow repository transfer does not delay preference mutation`() {
        PreferenceIntegrationTestSetup.ensureReady()
        MockWebServer().use { upstream ->
            upstream.enqueue(
                MockResponse.Builder()
                    .body("[]")
                    .bodyDelay(1, TimeUnit.SECONDS)
                    .build(),
            )
            upstream.start()
            val root = Files.createTempDirectory("extension-transfer-integration")
            val loader = ExtensionLoader(root.toFile())
            injectSource(loader, MutablePreferenceSource())
            val downloader = BoundedDownloadClient()
            val manager = ExtensionManager(loader, root.toFile(), downloader)
            manager.setRepos(listOf(upstream.url("/index.json").toString()))
            val runningServer = RpcServer(loader, manager, port = 0)
            server = runningServer
            runningServer.start()
            val baseUrl = "http://127.0.0.1:${boundPort(runningServer)}"
            assertEquals(200, putPreference(baseUrl, false).statusCode())
            try {
                val listing = getAsync(baseUrl, "/extensions", Duration.ofSeconds(3))
                assertTrue(upstream.takeRequest(5, TimeUnit.SECONDS) != null, "repository request did not start")
                assertFalse(listing.isDone, "slow repository transfer completed before the mutation")

                val preferenceMutation = putPreferenceAsync(baseUrl, true)

                val mutationResponse = preferenceMutation.get(500, TimeUnit.MILLISECONDS)
                assertEquals(200, mutationResponse.statusCode())
                assertEquals(true, preferenceValue(mutationResponse.body()))
                assertFalse(listing.isDone, "preference mutation waited for repository transfer")

                val listingResponse = listing.get(3, TimeUnit.SECONDS)
                assertEquals(200, listingResponse.statusCode())
                assertEquals(mapper.readTree("[]"), mapper.readTree(listingResponse.body()))
                val persisted = getAsync(baseUrl, "/sources/303/preferences").get(500, TimeUnit.MILLISECONDS)
                assertEquals(200, persisted.statusCode())
                assertEquals(true, preferenceValue(persisted.body()))
            } finally {
                manager.close()
                assertTrue(root.listDirectoryEntries().none { it.fileName.toString().endsWith(".tmp") })
            }
        }
    }

    /** Closing the real HTTP client request promptly cancels cooperative source work. */
    @RepeatedTest(20)
    fun `client disconnect cancels cooperative source work before its normal response`() {
        val entered = CountDownLatch(1)
        val exited = CountDownLatch(1)
        val root = Files.createTempDirectory("client-disconnect-integration").toFile()
        val loader = ExtensionLoader(root)
        injectSource(loader, CooperativeDetailsSource(entered, exited))
        val runningServer = RpcServer(loader, ExtensionManager(loader, root), port = 0)
        server = runningServer
        runningServer.start()
        val baseUrl = "http://127.0.0.1:${boundPort(runningServer)}"
        val request = postAsync(baseUrl, "/manga", """{"sourceId":404,"url":"/cooperative"}""")
        blockedRequests += request
        assertTrue(entered.await(5, TimeUnit.SECONDS), "cooperative source call did not start")

        assertTrue(request.cancel(true), "HTTP request was already terminal before cancellation")

        assertTrue(exited.await(500, TimeUnit.MILLISECONDS), "source work outlived the disconnected client")
        assertTrue(awaitRunning(baseUrl, expected = 0, timeoutMillis = 500), "physical source slot did not release")
    }

    private fun getAsync(
        baseUrl: String,
        path: String,
        timeout: Duration = Duration.ofSeconds(1),
    ): CompletableFuture<HttpResponse<String>> =
        client.sendAsync(
            HttpRequest.newBuilder(URI("$baseUrl$path"))
                .timeout(timeout)
                .GET()
                .build(),
            HttpResponse.BodyHandlers.ofString(),
        )

    private fun postAsync(
        baseUrl: String,
        path: String,
        body: String,
    ): CompletableFuture<HttpResponse<String>> =
        client.sendAsync(
            HttpRequest.newBuilder(URI("$baseUrl$path"))
                .POST(HttpRequest.BodyPublishers.ofString(body))
                .build(),
            HttpResponse.BodyHandlers.ofString(),
        )

    private fun putPreference(
        baseUrl: String,
        enabled: Boolean,
    ): HttpResponse<String> = putPreferenceAsync(baseUrl, enabled).get(1, TimeUnit.SECONDS)

    private fun putPreferenceAsync(
        baseUrl: String,
        enabled: Boolean,
    ): CompletableFuture<HttpResponse<String>> =
        client.sendAsync(
            HttpRequest.newBuilder(URI("$baseUrl/sources/303/preferences"))
                .header("Content-Type", "application/json")
                .PUT(HttpRequest.BodyPublishers.ofString("""{"enabled":$enabled}"""))
                .build(),
            HttpResponse.BodyHandlers.ofString(),
        )

    private fun preferenceValue(body: String): Boolean {
        val preferences = mapper.readTree(body)["preferences"]
        val enabled = preferences.first { it["key"].textValue() == "enabled" }
        return enabled["currentValue"].booleanValue()
    }

    private fun awaitRunning(
        baseUrl: String,
        expected: Int,
        timeoutMillis: Long,
    ): Boolean {
        val deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(timeoutMillis)
        do {
            val status = getAsync(baseUrl, "/status").get(500, TimeUnit.MILLISECONDS)
            if (status.statusCode() == 200 && mapper.readTree(status.body())["running"].intValue() == expected) {
                return true
            }
            Thread.sleep(5)
        } while (System.nanoTime() < deadline)
        return false
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
