package enginehost

/*
 * Pins the RPC pool's THREAD NAMES, because the name is the whole point of the factory (GAP-137).
 *
 * The container watchdog sends SIGQUIT and identifies the RPC pool in the resulting thread dump by
 * name — it restarts the engine only when every RPC pool thread it can see is BLOCKED, which is what
 * separates a real deadlock from a saturated pool. The bare `Executors.newFixedThreadPool(8)` this
 * replaced left naming to the JDK's DefaultThreadFactory, whose `pool-<N>-thread-<M>` prefix is
 * numbered from a PROCESS-GLOBAL counter any other library can shift; when it shifts, the watchdog
 * counts zero pool threads and silently declines to restart a permanently deadlocked engine
 * (reproduced end to end). So the name is a production safety anchor, and a rename is a silent
 * regression no other test in this suite would notice — hence these assertions.
 *
 * `watchdog.sh` matches the dump line with the anchored pattern `^"engine-rpc-[0-9]+" `, so the tests
 * below assert the EXACT `engine-rpc-<n>` shape rather than merely "starts with engine-rpc".
 *
 * NOTE on ServerConfigTestSetup: deliberately NOT referenced. Nothing on this path reads Suwayomi's
 * process-global `serverConfig` — RpcServer.start() only registers contexts, and `/health` reads
 * ExtensionLoader.loaded(), a plain in-memory map. Adding the registration would be harmless but
 * misleading about what this test depends on. (RpcServerContainmentTest omits it for the same reason.)
 */

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
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class RpcThreadNamingTest {
    /** The exact shape watchdog.sh anchors on, minus the surrounding dump quoting. */
    private val rpcThreadName = Regex("""^engine-rpc-\d+$""")

    private lateinit var server: RpcServer
    private lateinit var baseUrl: String
    private val client: HttpClient = HttpClient.newHttpClient()

    @BeforeTest
    fun setUp() {
        val workDir = Files.createTempDirectory("rpc-thread-naming").toFile()
        val loader = ExtensionLoader(workDir)
        server = RpcServer(loader, ExtensionManager(loader, workDir), port = 0)
        server.start()
        baseUrl = "http://localhost:${boundPort(server)}"
    }

    @AfterTest
    fun tearDown() {
        server.stop()
    }

    /**
     * The end-to-end pin, and the one that kills the "drop the factory argument" mutation: a request
     * served by a REAL RpcServer creates a thread named `engine-rpc-<n>` — the exact string the
     * watchdog looks for in the SIGQUIT dump.
     *
     * The pool grows lazily, so a fresh server owns NO pool threads until an exchange is dispatched
     * to one; diffing the live-thread set across the request therefore attributes the new thread to
     * THIS server (Gradle shares one JVM across test classes, and another class's RpcServer would
     * otherwise satisfy a bare "some engine-rpc thread exists" assertion). The request completing is
     * also the synchronisation point — no polling, no sleep.
     */
    @Test
    fun `a served request runs on a thread named engine-rpc-n`() {
        val before = Thread.getAllStackTraces().keys

        assertEquals(200, get("/health").statusCode())

        val created = Thread.getAllStackTraces().keys - before
        val rpcThreads = created.map { it.name }.filter { rpcThreadName.matches(it) }
        assertTrue(
            rpcThreads.isNotEmpty(),
            "serving a request created no engine-rpc-<n> thread — watchdog.sh would count ZERO RPC " +
                "pool threads and never restart a deadlocked engine. Threads created: " +
                created.map { it.name },
        )
    }

    /**
     * Numbering is PER POOL and starts at 1. DefaultThreadFactory's pool ordinal comes from a static
     * shared with the whole JVM — the coupling that made the watchdog's anchor unownable — so two
     * independent factories must both hand out `engine-rpc-1` first.
     */
    @Test
    fun `each factory numbers its own threads from 1`() {
        val first = RpcThreadFactory()
        val second = RpcThreadFactory()

        assertEquals("engine-rpc-1", first.newThread {}.name)
        assertEquals("engine-rpc-2", first.newThread {}.name)
        assertEquals("engine-rpc-1", second.newThread {}.name, "the counter must not be shared between pools")
    }

    /**
     * Daemon status and priority stay exactly what `Executors.newFixedThreadPool` produced. These
     * threads serve live HTTP requests; this change is naming only, and a daemon RPC pool would let
     * the JVM exit out from under in-flight requests.
     */
    @Test
    fun `threads keep the Executors default daemon status and priority`() {
        val thread = RpcThreadFactory().newThread {}

        assertFalse(thread.isDaemon, "the JDK's DefaultThreadFactory produces non-daemon threads")
        assertEquals(Thread.NORM_PRIORITY, thread.priority)
    }

    private fun get(path: String): HttpResponse<String> =
        client.send(
            HttpRequest.newBuilder(URI("$baseUrl$path")).GET().build(),
            HttpResponse.BodyHandlers.ofString(),
        )

    /** Read the ephemeral port the RpcServer's HttpServer actually bound to. */
    private fun boundPort(rpc: RpcServer): Int {
        val field = RpcServer::class.java.getDeclaredField("server").apply { isAccessible = true }
        return (field.get(rpc) as HttpServer).address.port
    }
}
