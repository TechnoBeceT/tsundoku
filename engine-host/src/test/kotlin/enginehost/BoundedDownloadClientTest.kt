package enginehost

import okhttp3.Call
import okhttp3.Callback
import okhttp3.EventListener
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.ResponseBody
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import okio.Buffer
import okio.BufferedSource
import okio.ForwardingSource
import okio.Timeout
import okio.buffer
import java.io.InterruptedIOException
import java.lang.reflect.InvocationTargetException
import java.net.SocketTimeoutException
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference
import kotlin.io.path.listDirectoryEntries
import kotlin.io.path.readBytes
import kotlin.io.path.writeBytes
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertIs
import kotlin.test.assertSame
import kotlin.test.assertTrue
import kotlin.reflect.KClass

class BoundedDownloadClientTest {
    @Test
    fun `interrupt closes a response delivered synchronously by call cancellation`() {
        val tracked = trackedResponse("raced body")
        val call = ControlledCall(onCancel = { it.onResponse(call = it.call, response = tracked.response) })
        val client = client()
        val failure = AtomicReference<Throwable>()
        val waiter =
            Thread {
                try {
                    invokeAwait(client, call)
                } catch (thrown: Throwable) {
                    failure.set(thrown)
                }
            }
        waiter.start()
        assertTrue(call.enqueued.await(5, TimeUnit.SECONDS), "await did not enqueue the controlled call")

        waiter.interrupt()
        waiter.join(TimeUnit.SECONDS.toMillis(5))

        assertTrue(!waiter.isAlive, "interrupted waiter did not return")
        assertIs<InterruptedIOException>(failure.get())
        assertEquals(1, tracked.closeCount.get(), "the response that won completion lost its only closer")
    }

    @Test
    fun `normal response ownership transfers open to the caller`() {
        val tracked = trackedResponse("normal body")
        val call = ControlledCall(onEnqueue = { it.onResponse(call = it.call, response = tracked.response) })

        val returned = invokeAwait(client(), call)

        assertSame(tracked.response, returned)
        assertEquals(0, tracked.closeCount.get(), "await closed a response before its caller could consume it")
        assertEquals("normal body", returned.body.string())
        assertEquals(1, tracked.closeCount.get(), "caller consumption did not close the response body exactly once")
    }

    @Test
    fun `response arriving after interrupted waiter cancellation is closed by the callback`() {
        val tracked = trackedResponse("late body")
        val call = ControlledCall()
        val failure = AtomicReference<Throwable>()
        val waiter =
            Thread {
                try {
                    invokeAwait(client(), call)
                } catch (thrown: Throwable) {
                    failure.set(thrown)
                }
            }
        waiter.start()
        assertTrue(call.enqueued.await(5, TimeUnit.SECONDS), "await did not enqueue the controlled call")
        waiter.interrupt()
        waiter.join(TimeUnit.SECONDS.toMillis(5))

        call.respond(tracked.response)

        assertIs<InterruptedIOException>(failure.get())
        assertEquals(1, tracked.closeCount.get(), "late callback did not close its rejected response")
    }

    @Test
    fun `repository indexes reject a streamed body above the configured limit`() {
        MockWebServer().use { server ->
            server.enqueue(MockResponse().setChunkedBody("123456", 1))
            server.start()
            val client = client(repoBodyLimit = 5)

            val failure = assertFailsWith<DownloadTooLargeException> {
                client.downloadRepoIndex(server.url("/index.json").toString())
            }

            assertTrue(failure.message!!.contains("repository index"))
            assertTrue(failure.message!!.contains("5 bytes"))
        }
    }

    @Test
    fun `APK downloads reject a streamed body above the configured limit and remove the temp file`() {
        MockWebServer().use { server ->
            server.enqueue(MockResponse().setChunkedBody("123456", 1))
            server.start()
            val targetDir = Files.createTempDirectory("bounded-apk-oversize")
            val installed = targetDir.resolve("installed.apk").also { it.writeBytes("working apk".toByteArray()) }
            val client = client(apkBodyLimit = 5)

            assertFailsWith<DownloadTooLargeException> {
                client.downloadApk(server.url("/extension.apk").toString(), targetDir)
            }

            assertEquals(listOf(installed), targetDir.listDirectoryEntries(), "failed transfer touched the installed APK or leaked a temp")
            assertContentEquals("working apk".toByteArray(), installed.readBytes())
        }
    }

    @Test
    fun `APK read timeout cancels the transfer and removes the partial temp file`() {
        MockWebServer().use { server ->
            server.enqueue(
                MockResponse()
                    .setBody("partial transfer")
                    .setBodyDelay(1, TimeUnit.DAYS),
            )
            server.start()
            val targetDir = Files.createTempDirectory("bounded-apk-read-timeout")
            val installed = targetDir.resolve("installed.apk").also { it.writeBytes("working apk".toByteArray()) }
            val client =
                client(
                    readTimeout = Duration.ofMillis(100),
                    callTimeout = Duration.ofSeconds(2),
                )

            assertFailsWith<SocketTimeoutException> {
                client.downloadApk(server.url("/slow.apk").toString(), targetDir)
            }

            assertEquals(listOf(installed), targetDir.listDirectoryEntries(), "timed-out transfer touched the installed APK or leaked a temp")
            assertContentEquals("working apk".toByteArray(), installed.readBytes())
        }
    }

    @Test
    fun `whole call timeout cancels a repository request that never responds`() {
        MockWebServer().use { server ->
            server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.NO_RESPONSE))
            server.start()
            val client =
                client(
                    readTimeout = Duration.ofSeconds(2),
                    callTimeout = Duration.ofMillis(100),
                )

            val failure = assertFailsWith<InterruptedIOException> {
                client.downloadRepoIndex(server.url("/index.json").toString())
            }

            assertTrue(failure.message!!.contains("timeout", ignoreCase = true))
        }
    }

    @Test
    fun `successful APK download returns a temporary file in the requested directory`() {
        MockWebServer().use { server ->
            server.enqueue(MockResponse().setBody("valid apk bytes"))
            server.start()
            val targetDir = Files.createTempDirectory("bounded-apk-success")

            val downloaded = client().downloadApk(server.url("/extension.apk").toString(), targetDir)

            assertEquals(targetDir, downloaded.parent)
            assertTrue(downloaded.fileName.toString().endsWith(".apk.tmp"))
            assertContentEquals("valid apk bytes".toByteArray(), downloaded.readBytes())
            Files.delete(downloaded)
        }
    }

    @Test
    fun `interrupting an APK download cancels the call and removes the temp file`() {
        MockWebServer().use { server ->
            server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.NO_RESPONSE))
            server.start()
            val targetDir = Files.createTempDirectory("bounded-apk-cancel")
            val installed = targetDir.resolve("installed.apk").also { it.writeBytes("working apk".toByteArray()) }
            val executor = Executors.newSingleThreadExecutor()
            try {
                val download = executor.submit<Path> {
                    client(callTimeout = Duration.ofSeconds(30))
                        .downloadApk(server.url("/cancel.apk").toString(), targetDir)
                }
                assertTrue(server.takeRequest(5, TimeUnit.SECONDS) != null, "APK request did not reach the server")

                assertTrue(download.cancel(true), "running APK download was not interrupted")
                eventually { targetDir.listDirectoryEntries() == listOf(installed) }
                assertContentEquals("working apk".toByteArray(), installed.readBytes())
            } finally {
                executor.shutdownNow()
            }
        }
    }

    @Test
    fun `default limits match the extension networking contract`() {
        assertEquals(16L * 1024 * 1024, BoundedDownloadClient.REPO_BODY_LIMIT_BYTES)
        assertEquals(128L * 1024 * 1024, BoundedDownloadClient.APK_BODY_LIMIT_BYTES)
        assertEquals(Duration.ofSeconds(10), BoundedDownloadClient.CONNECT_TIMEOUT)
        assertEquals(Duration.ofSeconds(60), BoundedDownloadClient.READ_TIMEOUT)
        assertEquals(Duration.ofSeconds(120), BoundedDownloadClient.CALL_TIMEOUT)
        val field = BoundedDownloadClient::class.java.getDeclaredField("client").apply { isAccessible = true }
        val configured = field.get(BoundedDownloadClient()) as OkHttpClient
        assertEquals(10_000, configured.connectTimeoutMillis)
        assertEquals(60_000, configured.readTimeoutMillis)
        assertEquals(120_000, configured.callTimeoutMillis)
    }

    private fun client(
        repoBodyLimit: Long = BoundedDownloadClient.REPO_BODY_LIMIT_BYTES,
        apkBodyLimit: Long = BoundedDownloadClient.APK_BODY_LIMIT_BYTES,
        connectTimeout: Duration = BoundedDownloadClient.CONNECT_TIMEOUT,
        readTimeout: Duration = BoundedDownloadClient.READ_TIMEOUT,
        callTimeout: Duration = BoundedDownloadClient.CALL_TIMEOUT,
    ): BoundedDownloadClient =
        BoundedDownloadClient(
            client =
                OkHttpClient.Builder()
                    .connectTimeout(connectTimeout)
                    .readTimeout(readTimeout)
                    .callTimeout(callTimeout)
                    .build(),
            repoBodyLimitBytes = repoBodyLimit,
            apkBodyLimitBytes = apkBodyLimit,
        )

    private fun eventually(condition: () -> Boolean) {
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        while (!condition() && System.nanoTime() < deadline) Thread.sleep(5)
        assertTrue(condition(), "condition did not become true")
    }

    private fun invokeAwait(
        client: BoundedDownloadClient,
        call: Call,
    ): Response {
        val method = BoundedDownloadClient::class.java.getDeclaredMethod("await", Call::class.java).apply { isAccessible = true }
        try {
            return method.invoke(client, call) as Response
        } catch (failure: InvocationTargetException) {
            throw requireNotNull(failure.cause)
        }
    }

    private fun trackedResponse(body: String): TrackedResponse {
        val closeCount = AtomicInteger()
        val source =
            object : ForwardingSource(Buffer().writeUtf8(body)) {
                override fun close() {
                    closeCount.incrementAndGet()
                    super.close()
                }
            }.buffer()
        val responseBody =
            object : ResponseBody() {
                override fun contentType() = null

                override fun contentLength(): Long = body.toByteArray().size.toLong()

                override fun source(): BufferedSource = source
            }
        val request = Request.Builder().url("https://example.test/extension").build()
        val response =
            Response.Builder()
                .request(request)
                .protocol(Protocol.HTTP_1_1)
                .code(200)
                .message("OK")
                .body(responseBody)
                .build()
        return TrackedResponse(response, closeCount)
    }
}

private data class TrackedResponse(
    val response: Response,
    val closeCount: AtomicInteger,
)

private class ControlledCall(
    private val onEnqueue: (Delivery) -> Unit = {},
    private val onCancel: (Delivery) -> Unit = {},
) : Call {
    val enqueued = CountDownLatch(1)
    private val request = Request.Builder().url("https://example.test/extension").build()
    private lateinit var callback: Callback
    private var executed = false
    private var cancelled = false

    override fun request(): Request = request

    override fun execute(): Response = error("controlled call is asynchronous")

    override fun enqueue(responseCallback: Callback) {
        check(!executed) { "Already Executed" }
        executed = true
        callback = responseCallback
        enqueued.countDown()
        onEnqueue(Delivery(this, callback))
    }

    override fun cancel() {
        cancelled = true
        onCancel(Delivery(this, callback))
    }

    override fun isExecuted(): Boolean = executed

    override fun isCanceled(): Boolean = cancelled

    override fun timeout(): Timeout = Timeout.NONE

    override fun addEventListener(eventListener: EventListener) = Unit

    override fun <T : Any> tag(type: KClass<T>): T? = null

    override fun <T> tag(type: Class<out T>): T? = null

    override fun <T : Any> tag(
        type: KClass<T>,
        computeIfAbsent: () -> T,
    ): T = computeIfAbsent()

    override fun <T : Any> tag(
        type: Class<T>,
        computeIfAbsent: () -> T,
    ): T = computeIfAbsent()

    override fun clone(): Call = ControlledCall(onEnqueue, onCancel)

    fun respond(response: Response) {
        callback.onResponse(this, response)
    }

    data class Delivery(
        val call: Call,
        val callback: Callback,
    ) {
        fun onResponse(
            call: Call,
            response: Response,
        ) = callback.onResponse(call, response)
    }
}
