package enginehost

import com.sun.net.httpserver.HttpServer
import java.net.InetSocketAddress
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.channels.SeekableByteChannel
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardOpenOption
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.io.path.writeBytes
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class InstalledApkExportTest {
    @Test
    fun `manager streams the exact installed APK and its manifest identity`() {
        val fixture = InstalledApkFixture()
        val expected = "exact installed bytes".toByteArray()
        fixture.apk.writeBytes(expected)

        val exported =
            fixture.manager.withInstalledApk(PACKAGE) { apk ->
                assertEquals(PACKAGE, apk.pkgName)
                assertEquals(57, apk.versionCode)
                assertEquals("1.4.57", apk.versionName)
                assertEquals(expected.size.toLong(), apk.contentLength)
                apk.input.readBytes()
            }

        assertContentEquals(expected, exported)
    }

    @Test
    fun `manager rejects missing non-regular empty and oversized installed APKs`() {
        val missing = InstalledApkFixture()
        assertFailsWith<IllegalArgumentException> { missing.manager.withInstalledApk(PACKAGE) { } }

        val directory = InstalledApkFixture()
        Files.createDirectory(directory.apk)
        assertFailsWith<IllegalArgumentException> { directory.manager.withInstalledApk(PACKAGE) { } }

        val empty = InstalledApkFixture()
        empty.apk.writeBytes(ByteArray(0))
        assertFailsWith<IllegalArgumentException> { empty.manager.withInstalledApk(PACKAGE) { } }

        val oversized = InstalledApkFixture()
        Files.newByteChannel(oversized.apk, StandardOpenOption.CREATE, StandardOpenOption.WRITE).use { channel ->
            (channel as SeekableByteChannel).truncate(MAX_APK_BYTES + 1)
            channel.position(MAX_APK_BYTES)
            channel.write(java.nio.ByteBuffer.wrap(byteArrayOf(0)))
        }
        assertFailsWith<IllegalArgumentException> { oversized.manager.withInstalledApk(PACKAGE) { } }
    }

    @Test
    fun `manager rejects a symlink even when its target is a regular file`() {
        val fixture = InstalledApkFixture()
        val outside = Files.createTempFile("installed-apk-outside", ".apk")
        outside.writeBytes("outside".toByteArray())
        Files.createSymbolicLink(fixture.apk, outside)

        assertTrue(Files.isRegularFile(fixture.apk))
        assertTrue(Files.isSymbolicLink(fixture.apk))
        assertFailsWith<IllegalArgumentException> { fixture.manager.withInstalledApk(PACKAGE) { } }
    }

    @Test
    fun `manager holds the mutation lock until APK streaming completes`() {
        val fixture = InstalledApkFixture()
        fixture.apk.writeBytes("apk".toByteArray())
        val streamEntered = CountDownLatch(1)
        val releaseStream = CountDownLatch(1)
        val executor = Executors.newFixedThreadPool(2)
        try {
            val export =
                executor.submit {
                    fixture.manager.withInstalledApk(PACKAGE) {
                        streamEntered.countDown()
                        assertTrue(releaseStream.await(5, TimeUnit.SECONDS))
                    }
                }
            assertTrue(streamEntered.await(5, TimeUnit.SECONDS))
            val mutation = executor.submit(java.util.concurrent.Callable { fixture.manager.underLock { "mutated" } })

            Thread.sleep(100)
            assertTrue(!mutation.isDone, "mutation entered while the installed APK stream was active")
            releaseStream.countDown()
            export.get(5, TimeUnit.SECONDS)
            assertEquals("mutated", mutation.get(5, TimeUnit.SECONDS))
        } finally {
            releaseStream.countDown()
            executor.shutdownNow()
        }
    }

    @Test
    fun `RPC requires control authentication and streams fixed-length APK metadata`() {
        val fixture = InstalledApkFixture()
        val expected = "rpc exact installed bytes".toByteArray()
        fixture.apk.writeBytes(expected)
        val server = RpcServer(fixture.loader, fixture.manager, port = 0, controlToken = CONTROL_TOKEN)
        server.start()
        try {
            val port = boundAddress(server).port
            assertTrue(boundAddress(server).address.isLoopbackAddress)
            assertEquals(401, request(port).statusCode())
            assertEquals(401, request(port, "wrong").statusCode())

            val response = request(port, CONTROL_TOKEN)
            assertEquals(200, response.statusCode())
            assertContentEquals(expected, response.body())
            assertEquals("application/vnd.android.package-archive", response.headers().firstValue("Content-Type").orElse(null))
            assertEquals(expected.size.toString(), response.headers().firstValue("Content-Length").orElse(null))
            assertEquals(PACKAGE, response.headers().firstValue("X-Tsundoku-Extension-Package").orElse(null))
            assertEquals("57", response.headers().firstValue("X-Tsundoku-Extension-Version-Code").orElse(null))
            assertEquals("1.4.57", response.headers().firstValue("X-Tsundoku-Extension-Version-Name").orElse(null))
        } finally {
            server.stop()
            fixture.manager.close()
        }
    }

    @Test
    fun `RPC installed APK route allows GET only`() {
        val fixture = InstalledApkFixture()
        fixture.apk.writeBytes("apk".toByteArray())
        val server = RpcServer(fixture.loader, fixture.manager, port = 0, controlToken = CONTROL_TOKEN)
        server.start()
        try {
            val port = boundAddress(server).port
            val request =
                HttpRequest.newBuilder(URI("http://127.0.0.1:$port/extensions/$PACKAGE/installed-apk"))
                    .header("Authorization", "Bearer $CONTROL_TOKEN")
                    .POST(HttpRequest.BodyPublishers.noBody())
                    .build()
            assertEquals(405, HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.ofByteArray()).statusCode())
        } finally {
            server.stop()
            fixture.manager.close()
        }
    }

    private class InstalledApkFixture {
        val root: Path = Files.createTempDirectory("installed-apk-export")
        val apk: Path = root.resolve("installed.apk")
        val loader = ExtensionLoader(root.toFile())
        val manager: ExtensionManager

        init {
            val record =
                InstalledExtension(
                    pkgName = PACKAGE,
                    name = "Example",
                    versionName = "1.4.57",
                    versionCode = 57,
                    lang = "en",
                    apkFileName = apk.fileName.toString(),
                    mainClass = "example.Extension",
                    isNsfw = false,
                    iconUrl = null,
                    repoUrl = null,
                    sourceIds = emptyList(),
                    sources = emptyList(),
                )
            loader.publishRegistry(loader.prepareRegistry(emptyMap(), mapOf(PACKAGE to record)))
            manager = ExtensionManager(loader, root.toFile())
        }
    }

    private fun request(
        port: Int,
        token: String? = null,
    ): HttpResponse<ByteArray> {
        val builder = HttpRequest.newBuilder(URI("http://127.0.0.1:$port/extensions/$PACKAGE/installed-apk")).GET()
        token?.let { builder.header("Authorization", "Bearer $it") }
        return HttpClient.newHttpClient().send(builder.build(), HttpResponse.BodyHandlers.ofByteArray())
    }

    private fun boundAddress(rpc: RpcServer): InetSocketAddress {
        val field = RpcServer::class.java.getDeclaredField("server").apply { isAccessible = true }
        return (field.get(rpc) as HttpServer).address
    }

    private companion object {
        const val PACKAGE = "eu.kanade.tachiyomi.extension.all.webtoons"
        const val CONTROL_TOKEN = "installed-apk-control-token"
        const val MAX_APK_BYTES = 256L * 1024 * 1024
    }
}
