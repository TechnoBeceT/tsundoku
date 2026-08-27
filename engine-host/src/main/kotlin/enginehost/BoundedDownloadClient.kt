package enginehost

import okhttp3.Call
import okhttp3.Callback
import okhttp3.Dispatcher
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.io.InterruptedIOException
import java.io.OutputStream
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.ArrayBlockingQueue
import java.util.concurrent.CompletableFuture
import java.util.concurrent.ExecutionException
import java.util.concurrent.ThreadPoolExecutor
import java.util.concurrent.TimeUnit

class DownloadTooLargeException(message: String) : IOException(message)

interface ExtensionDownloadClient : AutoCloseable {
    fun downloadRepoIndex(url: String): ByteArray

    fun downloadApk(
        url: String,
        targetDir: Path,
    ): Path

    override fun close() {}
}

/** Applies fixed transport and response-size bounds to extension repository and APK downloads. */
class BoundedDownloadClient internal constructor(
    client: OkHttpClient? = null,
    private val repoBodyLimitBytes: Long = REPO_BODY_LIMIT_BYTES,
    private val apkBodyLimitBytes: Long = APK_BODY_LIMIT_BYTES,
) : ExtensionDownloadClient {
    private val ownsClient = client == null
    private val client: OkHttpClient = client ?: defaultHttpClient()
    override fun downloadRepoIndex(url: String): ByteArray {
        val output = ByteArrayOutputStream()
        download(url, repoBodyLimitBytes, "repository index", output)
        return output.toByteArray()
    }

    /** Downloads an APK into a caller-owned temporary file under [targetDir]. */
    override fun downloadApk(
        url: String,
        targetDir: Path,
    ): Path {
        Files.createDirectories(targetDir)
        val temporary = Files.createTempFile(targetDir, ".extension-download-", ".apk.tmp")
        try {
            Files.newOutputStream(temporary).use { output ->
                download(url, apkBodyLimitBytes, "APK", output)
            }
            return temporary
        } catch (failure: Throwable) {
            runCatching { Files.deleteIfExists(temporary) }
            throw failure
        }
    }

    private fun download(
        url: String,
        limitBytes: Long,
        label: String,
        output: OutputStream,
    ) {
        require(limitBytes >= 0) { "body limit must not be negative" }
        val request = Request.Builder().url(url).get().build()
        await(client.newCall(request)).use { response ->
            if (!response.isSuccessful) throw IOException("$label download failed with HTTP ${response.code}")
            val body = response.body
            val declaredLength = body.contentLength()
            if (declaredLength > limitBytes) throw tooLarge(label, limitBytes)
            body.byteStream().use { input ->
                val buffer = ByteArray(DEFAULT_BUFFER_SIZE)
                var total = 0L
                while (true) {
                    val read = input.read(buffer)
                    if (read < 0) break
                    if (total > limitBytes - read) throw tooLarge(label, limitBytes)
                    output.write(buffer, 0, read)
                    total += read
                }
            }
        }
    }

    /** Await asynchronously so interruption can cancel an in-flight OkHttp call immediately. */
    private fun await(call: Call): Response {
        val result = CompletableFuture<Response>()
        call.enqueue(
            object : Callback {
                override fun onFailure(
                    call: Call,
                    e: IOException,
                ) {
                    result.completeExceptionally(e)
                }

                override fun onResponse(
                    call: Call,
                    response: Response,
                ) {
                    if (!result.complete(response)) response.close()
                }
            },
        )
        try {
            return result.get()
        } catch (interrupted: InterruptedException) {
            call.cancel()
            if (!result.cancel(false)) {
                runCatching { result.getNow(null) }.getOrNull()?.close()
            }
            Thread.currentThread().interrupt()
            throw InterruptedIOException("extension download cancelled").apply { initCause(interrupted) }
        } catch (failed: ExecutionException) {
            val cause = failed.cause
            if (cause is IOException) throw cause
            throw IOException("extension download failed", cause)
        }
    }

    private fun tooLarge(
        label: String,
        limitBytes: Long,
    ) = DownloadTooLargeException("$label exceeds the $limitBytes bytes response limit")

    override fun close() {
        if (!ownsClient) return
        client.dispatcher.cancelAll()
        client.connectionPool.evictAll()
        client.cache?.close()
        val executor = client.dispatcher.executorService
        executor.shutdownNow()
        try {
            executor.awaitTermination(TERMINATION_SECONDS, TimeUnit.SECONDS)
        } catch (_: InterruptedException) {
            Thread.currentThread().interrupt()
        }
    }

    companion object {
        val CONNECT_TIMEOUT: Duration = Duration.ofSeconds(10)
        val READ_TIMEOUT: Duration = Duration.ofSeconds(60)
        val CALL_TIMEOUT: Duration = Duration.ofSeconds(120)
        const val REPO_BODY_LIMIT_BYTES: Long = 16L * 1024 * 1024
        const val APK_BODY_LIMIT_BYTES: Long = 128L * 1024 * 1024
        private const val NETWORK_THREADS = 2
        private const val NETWORK_QUEUE_CAPACITY = 32
        private const val TERMINATION_SECONDS = 5L
        private const val NETWORK_THREAD_PREFIX = "engine-network-"

        private fun defaultHttpClient(): OkHttpClient {
            val executor =
                ThreadPoolExecutor(
                    NETWORK_THREADS,
                    NETWORK_THREADS,
                    0L,
                    TimeUnit.MILLISECONDS,
                    ArrayBlockingQueue(NETWORK_QUEUE_CAPACITY),
                    RpcThreadFactory(NETWORK_THREAD_PREFIX),
                )
            val dispatcher =
                Dispatcher(executor).apply {
                    maxRequests = NETWORK_THREADS
                    maxRequestsPerHost = NETWORK_THREADS
                }
            return OkHttpClient.Builder()
                .dispatcher(dispatcher)
                .connectTimeout(CONNECT_TIMEOUT)
                .readTimeout(READ_TIMEOUT)
                .callTimeout(CALL_TIMEOUT)
                .build()
        }
    }
}
