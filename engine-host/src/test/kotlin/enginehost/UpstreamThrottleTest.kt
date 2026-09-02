package enginehost

import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.sun.net.httpserver.HttpServer
import eu.kanade.tachiyomi.source.model.FilterList
import eu.kanade.tachiyomi.source.model.MangasPage
import eu.kanade.tachiyomi.source.model.Page
import eu.kanade.tachiyomi.source.model.SChapter
import eu.kanade.tachiyomi.source.model.SManga
import eu.kanade.tachiyomi.source.model.SMangaUpdate
import eu.kanade.tachiyomi.source.online.HttpSource
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.Headers
import okhttp3.OkHttpClient
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.file.Files
import java.time.Instant
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class UpstreamThrottleTest {
    private val now = Instant.parse("2026-09-02T10:00:00Z")

    @Test
    fun `retry after accepts bounded delta seconds and HTTP dates`() {
        assertEquals(90L, parseRetryAfterSeconds("90", now))
        assertEquals(120L, parseRetryAfterSeconds("Wed, 02 Sep 2026 10:02:00 GMT", now))
        assertEquals(86_400L, parseRetryAfterSeconds("86400", now))
    }

    @Test
    fun `retry after rejects absent malformed past negative and excessive values`() {
        val cases = listOf<String?>(
            null,
            "",
            "later",
            "-1",
            "86401",
            "Wed, 02 Sep 2026 09:59:59 GMT",
        )

        cases.forEach { value -> assertEquals(null, parseRetryAfterSeconds(value, now), "value=$value") }
    }

    @Test
    fun `image RPC preserves upstream status and bounded retry delay`() {
        val body = imageRpcError("90")

        assertEquals(429, body["upstreamStatus"].asInt())
        assertEquals(90, body["retryAfterSeconds"].asInt())
    }

    @Test
    fun `image RPC converts retry after HTTP date to a bounded delay`() {
        val retryAt = DateTimeFormatter.RFC_1123_DATE_TIME.format(Instant.now().plusSeconds(120).atZone(ZoneOffset.UTC))
        val seconds = imageRpcError(retryAt)["retryAfterSeconds"].asLong()

        assertTrue(seconds in 118..120, "retryAfterSeconds=$seconds")
    }

    private fun imageRpcError(retryAfter: String): com.fasterxml.jackson.databind.JsonNode {
        MockWebServer().use { upstream ->
            upstream.enqueue(MockResponse(code = 429, headers = okhttp3.Headers.headersOf("Retry-After", retryAfter)))
            upstream.start()
            val workDir = Files.createTempDirectory("rpc-image-throttle").toFile()
            val loader = ExtensionLoader(workDir)
            loader.publishTestSources(listOf(ThrottleImageSource(upstream.url("/").toString())))
            val rpc = RpcServer(loader, ExtensionManager(loader, workDir), port = 0)
            rpc.start()
            try {
                val port = (RpcServer::class.java.getDeclaredField("server").apply { isAccessible = true }.get(rpc) as HttpServer).address.port
                val request = HttpRequest.newBuilder(URI("http://localhost:$port/image"))
                    .POST(HttpRequest.BodyPublishers.ofString("""{"sourceId":91,"pageUrl":"","imageUrl":"${upstream.url("page.jpg")}"}"""))
                    .build()

                val response = HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.ofString())
                val body = jacksonObjectMapper().readTree(response.body())

                assertEquals(502, response.statusCode())
                return body
            } finally {
                rpc.stop()
                workDir.deleteRecursively()
            }
        }
    }
}

private class ThrottleImageSource(override val baseUrl: String) : HttpSource() {
    override val id = 91L
    override val name = "Throttle fixture"
    override val lang = "en"
    override val supportsLatest = false
    override val client = OkHttpClient()
    override fun headersBuilder(): Headers.Builder = Headers.Builder()
    override fun getMangaUrl(manga: SManga): String = baseUrl
    override suspend fun getPopularManga(page: Int): MangasPage = error("unused")
    override suspend fun getLatestUpdates(page: Int): MangasPage = error("unused")
    override suspend fun getSearchManga(page: Int, query: String, filters: FilterList): MangasPage = error("unused")
    override suspend fun getMangaUpdate(manga: SManga, chapters: List<SChapter>, fetchDetails: Boolean, fetchChapters: Boolean): SMangaUpdate = error("unused")
    override suspend fun getPageList(chapter: SChapter): List<Page> = error("unused")
}
