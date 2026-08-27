package enginehost

import eu.kanade.tachiyomi.source.Source
import eu.kanade.tachiyomi.source.model.FilterList
import eu.kanade.tachiyomi.source.model.MangasPage
import eu.kanade.tachiyomi.source.model.Page
import eu.kanade.tachiyomi.source.model.SChapter
import eu.kanade.tachiyomi.source.model.SManga
import eu.kanade.tachiyomi.source.model.SMangaUpdate
import kotlinx.coroutines.delay
import org.junit.jupiter.api.RepeatedTest
import java.net.InetSocketAddress
import java.net.Socket
import java.nio.charset.StandardCharsets
import java.nio.file.Files
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private class HalfCloseDetailsSource(
    private val entered: CountDownLatch,
    private val completed: AtomicBoolean,
) : Source {
    override val id: Long = 505L
    override val name: String = "Half-close source"
    override val lang: String = "en"
    override val supportsLatest: Boolean = false

    override suspend fun getMangaUpdate(
        manga: SManga,
        chapters: List<SChapter>,
        fetchDetails: Boolean,
        fetchChapters: Boolean,
    ): SMangaUpdate {
        entered.countDown()
        delay(500)
        completed.set(true)
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

class ClientConnectionObserverAdversarialTest {
    /** A request write-half FIN must not cancel the still-readable response path. */
    @RepeatedTest(20)
    fun `half closed request can still receive its normal response`() {
        assertSuccessfulRequest(halfClose = true)
    }

    /** The connection observer must leave an ordinary response-capable request untouched. */
    @RepeatedTest(20)
    fun `normal request remains live until its successful response`() {
        assertSuccessfulRequest(halfClose = false)
    }

    private fun assertSuccessfulRequest(halfClose: Boolean) {
        val entered = CountDownLatch(1)
        val completed = AtomicBoolean(false)
        val root = Files.createTempDirectory("half-close-integration").toFile()
        val loader = ExtensionLoader(root)
        injectSource(loader, HalfCloseDetailsSource(entered, completed))
        val manager = ExtensionManager(loader, root)
        val server = RpcServer(loader, manager, port = 0)
        server.start()
        try {
            Socket().use { socket ->
                socket.soTimeout = 5_000
                socket.connect(InetSocketAddress("127.0.0.1", boundPort(server)))
                writeRequest(socket)
                assertTrue(entered.await(5, TimeUnit.SECONDS), "source call did not start")

                if (halfClose) socket.shutdownOutput()

                val statusLine = socket.getInputStream().bufferedReader(StandardCharsets.US_ASCII).readLine()
                assertEquals("HTTP/1.1 200 OK", statusLine)
                assertTrue(completed.get(), "live half-close was misclassified as cancellation")
            }
        } finally {
            server.stop()
            manager.close()
        }
    }

    private fun writeRequest(socket: Socket) {
        val body = """{"sourceId":505,"url":"/half-close"}"""
        val request =
            buildString {
                append("POST /manga HTTP/1.1\r\n")
                append("Host: 127.0.0.1\r\n")
                append("Content-Type: application/json\r\n")
                append("Content-Length: ${body.toByteArray(StandardCharsets.UTF_8).size}\r\n")
                append("Connection: close\r\n")
                append("\r\n")
                append(body)
            }
        socket.getOutputStream().apply {
            write(request.toByteArray(StandardCharsets.UTF_8))
            flush()
        }
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
        val http = field.get(rpc) as com.sun.net.httpserver.HttpServer
        return http.address.port
    }
}
