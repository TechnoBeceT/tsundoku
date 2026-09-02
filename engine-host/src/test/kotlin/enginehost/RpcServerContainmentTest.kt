package enginehost

/*
 * Pins the GAP-100 resilience gap: a broken / mistranslated extension throws an `Error` subclass
 * (VerifyError / LinkageError / InstantiationError) out of a source call — NOT an `Exception`. The
 * RPC handlers used to end their catch chain at `catch (e: Exception)`, so an Error escaped the
 * handler, the HttpExchange never got a response, and the request HUNG until the client timed out
 * (the real "can't even install / everything hangs" symptom). Broadening the final catch to
 * `Throwable` contains it as a clean 502 so one bad source can never stall the engine.
 *
 * These tests start a real RpcServer on an ephemeral port and drive real HTTP:
 *  - a source call that throws a VerifyError must return 502 with a JSON body (contained, not hung),
 *  - a malformed body must still return 400 (the specific request-classification catches — BadRequest
 *    / JacksonException / IllegalArgumentException — were not clobbered by the broadening).
 */

import eu.kanade.tachiyomi.source.Source
import eu.kanade.tachiyomi.source.model.FilterList
import eu.kanade.tachiyomi.source.model.MangasPage
import eu.kanade.tachiyomi.source.model.Page
import eu.kanade.tachiyomi.source.model.SChapter
import eu.kanade.tachiyomi.source.model.SManga
import eu.kanade.tachiyomi.source.model.SMangaUpdate
import com.sun.net.httpserver.HttpServer
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
 * A [Source] test double whose details call throws a caller-supplied [Throwable] — used to model a
 * mistranslated extension that fails class verification at call time (VerifyError et al.). Every
 * other source call is unused by the `/manga` route and errors if touched.
 */
private class ThrowingSource(
    private val onDetails: () -> Nothing,
) : Source {
    override val id: Long = 1L
    override val name: String = "Throwing Source"
    override val lang: String = "en"
    override val supportsLatest: Boolean = false

    override suspend fun getMangaUpdate(
        manga: SManga,
        chapters: List<SChapter>,
        fetchDetails: Boolean,
        fetchChapters: Boolean,
    ): SMangaUpdate = onDetails()

    override suspend fun getPopularManga(page: Int): MangasPage = error("unused")

    override suspend fun getLatestUpdates(page: Int): MangasPage = error("unused")

    override suspend fun getSearchManga(
        page: Int,
        query: String,
        filters: FilterList,
    ): MangasPage = error("unused")

    override suspend fun getPageList(chapter: SChapter): List<Page> = error("unused")
}

class RpcServerContainmentTest {
    private lateinit var server: RpcServer
    private lateinit var baseUrl: String
    private val client: HttpClient = HttpClient.newHttpClient()

    /**
     * Stand up a real RpcServer on an ephemeral port whose registry resolves sourceId 1 to a source
     * that throws a [VerifyError]. The loader's source map is private and populated only by the APK
     * install pipeline, so the fake is injected reflectively — this keeps the production loader
     * unchanged (no test-only seam) while still driving the real HTTP path end to end.
     */
    @BeforeTest
    fun setUp() {
        val workDir = Files.createTempDirectory("rpc-containment").toFile()
        val loader = ExtensionLoader(workDir)
        injectSource(loader, ThrowingSource { throw VerifyError("boom") })
        val extensions = ExtensionManager(loader, workDir)

        server = RpcServer(loader, extensions, port = 0)
        server.start()
        baseUrl = "http://localhost:${boundPort(server)}"
    }

    @AfterTest
    fun tearDown() {
        server.stop()
    }

    /**
     * A source call that throws an `Error` (VerifyError) is contained as a clean 502 with a JSON
     * body — the request returns instead of hanging, and the process does not crash.
     */
    @Test
    fun `a source call throwing an Error is contained as a 502, not a hang`() {
        val response = post("/manga", """{"sourceId":1,"url":"/series/1"}""")

        assertEquals(502, response.statusCode())
        assertTrue(response.body().trimStart().startsWith("{"), "expected a JSON body, got: ${response.body()}")
        assertTrue(response.body().contains("VerifyError"), "expected the error class in the body: ${response.body()}")
    }

    /**
     * A malformed request body still yields 400 — the broadened final catch did not swallow the
     * specific request-classification catches (JacksonException -> 400).
     */
    @Test
    fun `a malformed body still yields 400`() {
        val response = post("/manga", "not json at all")

        assertEquals(400, response.statusCode())
        assertTrue(response.body().trimStart().startsWith("{"), "expected a JSON body, got: ${response.body()}")
    }

    private fun post(
        path: String,
        body: String,
    ): HttpResponse<String> {
        val request =
            HttpRequest.newBuilder(URI("$baseUrl$path"))
                .POST(HttpRequest.BodyPublishers.ofString(body))
                .build()
        return client.send(request, HttpResponse.BodyHandlers.ofString())
    }

    /** Register a fake source through the same atomic bootstrap path used before the RPC server starts. */
    private fun injectSource(
        loader: ExtensionLoader,
        source: Source,
    ) = loader.registerSources(listOf(source))

    /** Read the ephemeral port the RpcServer's HttpServer actually bound to. */
    private fun boundPort(rpc: RpcServer): Int {
        val field = RpcServer::class.java.getDeclaredField("server").apply { isAccessible = true }
        return (field.get(rpc) as HttpServer).address.port
    }
}
