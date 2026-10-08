package enginehost

import com.sun.net.httpserver.HttpServer
import eu.kanade.tachiyomi.source.model.Page
import eu.kanade.tachiyomi.source.online.HttpSource
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.file.Files
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import okhttp3.Headers
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer

private class ReaderContextSource : HttpSource() {
    override val id = 91L
    override val name = "Reader context fixture"
    override val lang = "en"
    override val supportsLatest = false
    override val baseUrl = "https://example.test"
    override val client = OkHttpClient()
    var receivedPage: Pair<String, String?>? = null
    override fun headersBuilder() = Headers.Builder().add("Referer", baseUrl).add("Origin", baseUrl)
    override fun imageRequest(page: Page): Request {
        receivedPage = page.url to page.imageUrl
        return super.imageRequest(page).newBuilder().removeHeader("Referer").removeHeader("Origin").build()
    }
}

class ReaderImageContextTest {
    @Test
    fun `empty reader addresses fail before request override`() {
        val source = ReaderContextSource()
        assertFailsWith<IllegalArgumentException> {
            SourceCalls.image(source, pageUrl = "", imageUrl = null, reader = true)
        }
        assertFailsWith<IllegalArgumentException> {
            SourceCalls.image(source, pageUrl = "", imageUrl = "", reader = true)
        }
        assertEquals(null, source.receivedPage)
    }

    @Test
    fun `empty page URL reader honors override while cover retains source headers`() {
        MockWebServer().use { upstream ->
            upstream.start()
            upstream.enqueue(MockResponse().setHeader("Content-Type", "image/webp").setBody("reader bytes"))
            upstream.enqueue(MockResponse().setHeader("Content-Type", "image/webp").setBody("cover bytes"))
            val workDir = Files.createTempDirectory("reader-context-rpc").toFile()
            val source = ReaderContextSource()
            val loader = ExtensionLoader(workDir)
            loader.publishTestSources(listOf(source))
            val rpc = RpcServer(loader, ExtensionManager(loader, workDir), port = 0)
            rpc.start()
            try {
                val port = (RpcServer::class.java.getDeclaredField("server").apply { isAccessible = true }.get(rpc) as HttpServer).address.port
                val url = upstream.url("/image.webp").toString()
                fun fetch(reader: Boolean): HttpResponse<String> {
                    val readerField = if (reader) ",\"reader\":true" else ""
                    val body = """{"sourceId":91,"pageUrl":"","imageUrl":"$url"$readerField}"""
                    val request = HttpRequest.newBuilder(URI("http://localhost:$port/image"))
                        .POST(HttpRequest.BodyPublishers.ofString(body)).build()
                    return HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.ofString())
                }
                val reader = fetch(true)
                assertEquals(200, reader.statusCode())
                assertEquals("reader bytes", reader.body())
                assertEquals("" to url, source.receivedPage)
                val readerRequest = upstream.takeRequest(5, TimeUnit.SECONDS)!!
                assertEquals(null, readerRequest.getHeader("Referer"))
                assertEquals(null, readerRequest.getHeader("Origin"))
                source.receivedPage = null
                val cover = fetch(false)
                assertEquals(200, cover.statusCode())
                assertEquals("cover bytes", cover.body())
                assertEquals(null, source.receivedPage)
                val coverRequest = upstream.takeRequest(5, TimeUnit.SECONDS)!!
                assertEquals("GET", coverRequest.method)
                assertEquals(source.baseUrl, coverRequest.getHeader("Referer"))
                assertEquals(source.baseUrl, coverRequest.getHeader("Origin"))
            } finally {
                rpc.stop()
                workDir.deleteRecursively()
            }
        }
    }
}
