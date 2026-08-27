package enginehost

import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import java.io.InterruptedIOException
import java.net.SocketTimeoutException
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.io.path.listDirectoryEntries
import kotlin.io.path.readBytes
import kotlin.io.path.writeBytes
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class BoundedDownloadClientTest {
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
}
