package enginehost

import com.sun.net.httpserver.HttpServer
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.file.Files
import java.time.Duration
import java.util.concurrent.CompletableFuture
import java.util.concurrent.ExecutorService
import java.util.concurrent.FutureTask
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** Pins the domain thread names consumed by engine process diagnostics and recovery. */
class RpcThreadNamingTest {
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

    /** Replacing the named front-door factory with the JDK default makes this real request fail. */
    @Test
    fun `a served request runs on an engine-http thread`() {
        val before = Thread.getAllStackTraces().keys

        assertEquals(200, get("/health").statusCode())

        val created = Thread.getAllStackTraces().keys - before
        val httpThreads = created.map { it.name }.filter { Regex("""^engine-http-\d+$""").matches(it) }
        assertTrue(
            httpThreads.isNotEmpty(),
            "serving a request created no engine-http-<n> thread; created: ${created.map { it.name }}",
        )
    }

    /** A wrong prefix or process-global counter breaks the exact per-domain diagnostic contract. */
    @Test
    fun `each domain factory uses its exact prefix and numbers from 1`() {
        listOf("engine-http-", "engine-source-", "engine-extension-").forEach { prefix ->
            val first = RpcThreadFactory(prefix)
            val second = RpcThreadFactory(prefix)

            assertEquals("${prefix}1", first.newThread {}.name)
            assertEquals("${prefix}2", first.newThread {}.name)
            assertEquals("${prefix}1", second.newThread {}.name, "the counter must be per factory for $prefix")
        }
    }

    /** Miswiring one default executor to another domain factory makes its observed name fail. */
    @Test
    fun `default executors wire the exact domain factories`() {
        val executors = RpcExecutors()
        try {
            assertEquals("engine-http-1", threadNameFrom(executors.frontDoorExecutor))
            assertEquals("engine-source-1", sourceThreadNameFrom(executors.sourceScheduler))
            assertEquals("engine-extension-1", threadNameFrom(executors.extensionExecutor))
            assertEquals("engine-extension-2", threadNameFrom(executors.extensionNetworkExecutor))
        } finally {
            executors.close()
        }
    }

    /** Deadline supervision must be distinguishable from source execution in thread dumps. */
    @Test
    fun `deadline timer uses its exact domain name and closes cleanly`() {
        val deadline = SourceCallDeadline(Duration.ofHours(1))
        try {
            deadline.supervise(FutureTask<Unit> {}, CompletableFuture(), {})
            assertEquals("engine-deadline-1", awaitThreadName("engine-deadline-"))
        } finally {
            deadline.close()
        }

        awaitNoThread("engine-deadline-")
    }

    /** The owned async transport is bounded, named, and leaves no non-daemon thread after close. */
    @Test
    fun `extension network pool uses its exact domain name and closes cleanly`() {
        val upstream = MockWebServer()
        upstream.enqueue(MockResponse().setBody("[]"))
        upstream.start()
        val downloadClient = BoundedDownloadClient()
        try {
            assertEquals("[]", downloadClient.downloadRepoIndex(upstream.url("/index.json").toString()).decodeToString())
            assertEquals("engine-network-1", awaitThreadName("engine-network-"))
        } finally {
            downloadClient.close()
            upstream.shutdown()
        }

        awaitNoThread("engine-network-")
    }

    /** Named threads retain the safety properties of the JDK fixed-pool factory. */
    @Test
    fun `domain threads are non-daemon and normal priority`() {
        val thread = RpcThreadFactory("engine-source-").newThread {}

        assertFalse(thread.isDaemon)
        assertEquals(Thread.NORM_PRIORITY, thread.priority)
    }

    private fun get(path: String): HttpResponse<String> =
        client.send(
            HttpRequest.newBuilder(URI("$baseUrl$path")).GET().build(),
            HttpResponse.BodyHandlers.ofString(),
        )

    private fun threadNameFrom(executor: ExecutorService): String =
        executor.submit<String> { Thread.currentThread().name }.get(5, TimeUnit.SECONDS)

    private fun sourceThreadNameFrom(scheduler: SourceScheduler): String =
        (scheduler.submit(1L) { Thread.currentThread().name } as Submission.Accepted)
            .future.get(5, TimeUnit.SECONDS)

    private fun awaitThreadName(prefix: String): String {
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        do {
            Thread.getAllStackTraces().keys.firstOrNull { it.isAlive && it.name.startsWith(prefix) }?.let { return it.name }
            Thread.sleep(10)
        } while (System.nanoTime() < deadline)
        error("no live $prefix<n> thread appeared")
    }

    private fun awaitNoThread(prefix: String) {
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        do {
            if (Thread.getAllStackTraces().keys.none { it.isAlive && it.name.startsWith(prefix) }) return
            Thread.sleep(10)
        } while (System.nanoTime() < deadline)
        error("a live $prefix<n> thread remained after close")
    }

    private fun boundPort(rpc: RpcServer): Int {
        val field = RpcServer::class.java.getDeclaredField("server").apply { isAccessible = true }
        return (field.get(rpc) as HttpServer).address.port
    }
}
