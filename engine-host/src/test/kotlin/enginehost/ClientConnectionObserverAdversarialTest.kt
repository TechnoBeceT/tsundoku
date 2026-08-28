package enginehost

import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import eu.kanade.tachiyomi.source.Source
import eu.kanade.tachiyomi.source.model.FilterList
import eu.kanade.tachiyomi.source.model.MangasPage
import eu.kanade.tachiyomi.source.model.Page
import eu.kanade.tachiyomi.source.model.SChapter
import eu.kanade.tachiyomi.source.model.SManga
import eu.kanade.tachiyomi.source.model.SMangaUpdate
import kotlinx.coroutines.delay
import org.junit.jupiter.api.RepeatedTest
import java.io.ByteArrayOutputStream
import java.io.InputStream
import java.net.InetSocketAddress
import java.net.Socket
import java.net.SocketTimeoutException
import java.nio.charset.StandardCharsets
import java.nio.file.Files
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private data class RawHttpResponse(
    val statusLine: String,
    val headers: Map<String, String>,
    val body: ByteArray,
)

private class SocketDetailsSource(
    override val id: Long,
    private val delayMillis: Long,
    private val entered: CountDownLatch,
    private val exited: CountDownLatch,
    private val completed: AtomicBoolean,
) : Source {
    override val name: String = "Socket lifecycle source"
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
            delay(delayMillis)
            completed.set(true)
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

class ClientConnectionObserverAdversarialTest {
    /** A request write-half FIN must not cancel the still-readable response path. */
    @RepeatedTest(20)
    fun `half closed request can still receive its normal response`() {
        assertSuccessfulRequest(halfClose = true, connectionClose = true)
    }

    /** TCP FIN remains ambiguous even when the request omits a connection token. */
    @RepeatedTest(20)
    fun `headerless half closed request can still receive its normal response`() {
        assertSuccessfulRequest(halfClose = true, connectionClose = false)
    }

    /** The connection observer must leave an ordinary response-capable request untouched. */
    @RepeatedTest(20)
    fun `normal request remains live until its successful response`() {
        assertSuccessfulRequest(halfClose = false, connectionClose = true)
    }

    /** An abortive close proves the response path is gone even if the request announced close. */
    @RepeatedTest(20)
    fun `reset after close header promptly cancels cooperative work`() {
        assertResetCancels(connectionClose = true)
    }

    /** An abortive close remains the same transport proof without a connection token. */
    @RepeatedTest(20)
    fun `headerless reset promptly cancels cooperative work`() {
        assertResetCancels(connectionClose = false)
    }

    private fun assertSuccessfulRequest(
        halfClose: Boolean,
        connectionClose: Boolean,
    ) {
        val entered = CountDownLatch(1)
        val exited = CountDownLatch(1)
        val completed = AtomicBoolean(false)
        val root = Files.createTempDirectory("half-close-integration").toFile()
        val loader = ExtensionLoader(root)
        injectSource(loader, SocketDetailsSource(505L, 500, entered, exited, completed))
        val manager = ExtensionManager(loader, root)
        val server = RpcServer(loader, manager, port = 0)
        server.start()
        try {
            Socket().use { socket ->
                socket.soTimeout = 5_000
                socket.connect(InetSocketAddress("127.0.0.1", boundPort(server)))
                writeRequest(socket, sourceId = 505L, connectionClose = connectionClose)
                assertTrue(entered.await(5, TimeUnit.SECONDS), "source call did not start")

                if (halfClose) socket.shutdownOutput()

                // Consume through the server's EOF instead of closing after the first header line.
                // That proves the half-closed response path completed and keeps HttpServer.stop()
                // out of the JDK connection-dispatcher's legal close bookkeeping window.
                val responseBytes = readThroughEof(socket.getInputStream())
                val response = parseResponse(responseBytes)
                assertEquals("HTTP/1.1 200 OK", response.statusLine, response.diagnostic())
                assertEquals("application/json", response.headers["content-type"], response.diagnostic())
                assertEquals(
                    response.body.size.toString(),
                    response.headers["content-length"],
                    "response body was not framed completely; ${response.diagnostic()}",
                )
                val json = JSON.readTree(response.body)
                assertEquals("/socket-lifecycle", json.path("url").asText(), response.diagnostic())
                assertTrue(completed.get(), "live half-close was misclassified as cancellation")
                assertTrue(exited.await(0, TimeUnit.MILLISECONDS), "response arrived before source work exited")
            }
        } finally {
            server.stop()
            manager.close()
        }
    }

    private fun assertResetCancels(connectionClose: Boolean) {
        val entered = CountDownLatch(1)
        val exited = CountDownLatch(1)
        val completed = AtomicBoolean(false)
        val root = Files.createTempDirectory("reset-integration").toFile()
        val loader = ExtensionLoader(root)
        injectSource(loader, SocketDetailsSource(506L, 5_000, entered, exited, completed))
        val manager = ExtensionManager(loader, root)
        val server = RpcServer(loader, manager, port = 0)
        server.start()
        val socket = Socket()
        try {
            socket.connect(InetSocketAddress("127.0.0.1", boundPort(server)))
            writeRequest(socket, sourceId = 506L, connectionClose = connectionClose)
            assertTrue(entered.await(5, TimeUnit.SECONDS), "source call did not start")
            assertTrue(awaitEstablishedTuple(socket), "reset test socket was never visible in procfs")
            // Leave more than three monitor intervals for the observer's own positive snapshot.
            Thread.sleep(200)

            socket.setSoLinger(true, 0)
            socket.close()

            assertTrue(exited.await(500, TimeUnit.MILLISECONDS), "source work outlived the reset client")
            assertFalse(completed.get(), "reset client was allowed to complete source work")
        } finally {
            runCatching(socket::close)
            server.stop()
            manager.close()
        }
    }

    private fun writeRequest(
        socket: Socket,
        sourceId: Long,
        connectionClose: Boolean,
    ) {
        val body = """{"sourceId":$sourceId,"url":"/socket-lifecycle"}"""
        val request =
            buildString {
                append("POST /manga HTTP/1.1\r\n")
                append("Host: 127.0.0.1\r\n")
                append("Content-Type: application/json\r\n")
                append("Content-Length: ${body.toByteArray(StandardCharsets.UTF_8).size}\r\n")
                if (connectionClose) append("Connection: close\r\n")
                append("\r\n")
                append(body)
            }
        socket.getOutputStream().apply {
            write(request.toByteArray(StandardCharsets.UTF_8))
            flush()
        }
    }

    private fun awaitEstablishedTuple(socket: Socket): Boolean {
        val connection =
            ClientConnection(
                localAddress = "7f000001",
                localPort = socket.port,
                remoteAddress = "7f000001",
                remotePort = socket.localPort,
            )
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        do {
            val states = ProcConnectionStateReader().connectionStates()?.get(connection)
            if (states == setOf(TcpConnectionState.ESTABLISHED)) return true
            Thread.sleep(5)
        } while (System.nanoTime() < deadline)
        return false
    }

    private fun parseResponse(bytes: ByteArray): RawHttpResponse {
        val headerEnd = bytes.indexOf(HTTP_HEADER_END)
        require(headerEnd >= 0) {
            "response ended before HTTP headers completed: ${bytes.toString(StandardCharsets.US_ASCII)}"
        }
        val headerLines =
            bytes.copyOfRange(0, headerEnd)
                .toString(StandardCharsets.US_ASCII)
                .split("\r\n")
        val statusLine = headerLines.firstOrNull().orEmpty()
        require(statusLine.isNotEmpty()) { "response contained no HTTP status line" }
        val headers =
            headerLines.drop(1).associate { line ->
                val separator = line.indexOf(':')
                require(separator > 0) { "malformed HTTP response header: $line" }
                line.substring(0, separator).lowercase() to line.substring(separator + 1).trim()
            }
        val body = bytes.copyOfRange(headerEnd + HTTP_HEADER_END.size, bytes.size)
        return RawHttpResponse(statusLine, headers, body)
    }

    private fun readThroughEof(input: InputStream): ByteArray {
        val received = ByteArrayOutputStream()
        val buffer = ByteArray(1024)
        while (true) {
            val count =
                try {
                    input.read(buffer)
                } catch (failure: SocketTimeoutException) {
                    throw AssertionError(
                        "timed out waiting for response EOF after ${received.size()} bytes: " +
                            received.toByteArray().toString(StandardCharsets.US_ASCII),
                        failure,
                    )
                }
            if (count < 0) return received.toByteArray()
            received.write(buffer, 0, count)
        }
    }

    private fun ByteArray.indexOf(needle: ByteArray): Int {
        if (needle.isEmpty()) return 0
        for (start in 0..size - needle.size) {
            if (needle.indices.all { offset -> this[start + offset] == needle[offset] }) return start
        }
        return -1
    }

    private fun RawHttpResponse.diagnostic(): String =
        "status=$statusLine headers=$headers body=${body.toString(StandardCharsets.UTF_8)}"

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

    private companion object {
        val JSON = jacksonObjectMapper()
        val HTTP_HEADER_END = "\r\n\r\n".toByteArray(StandardCharsets.US_ASCII)
    }
}
