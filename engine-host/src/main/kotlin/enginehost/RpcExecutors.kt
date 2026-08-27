package enginehost

import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import java.util.concurrent.ThreadFactory
import java.util.concurrent.atomic.AtomicInteger

/**
 * Owns the independent execution domains used by the RPC server. Source work is temporarily backed
 * by a fixed pool, while extension work stays single-writer and the HTTP front door remains free to
 * dispatch control requests.
 */
class RpcExecutors(
    frontDoorThreads: Int = FRONT_DOOR_THREADS,
    val sourceExecutor: ExecutorService = namedFixedPool(SOURCE_THREADS, SOURCE_THREAD_PREFIX),
    val extensionExecutor: ExecutorService = namedFixedPool(EXTENSION_THREADS, EXTENSION_THREAD_PREFIX),
) : AutoCloseable {
    init {
        require(frontDoorThreads > 0) { "frontDoorThreads must be positive" }
    }

    val frontDoorExecutor: ExecutorService = namedFixedPool(frontDoorThreads, HTTP_THREAD_PREFIX)

    override fun close() {
        listOf(frontDoorExecutor, sourceExecutor, extensionExecutor)
            .distinct()
            .forEach(ExecutorService::shutdownNow)
    }

    companion object {
        private const val FRONT_DOOR_THREADS = 4
        private const val SOURCE_THREADS = 8
        private const val EXTENSION_THREADS = 1

        internal const val HTTP_THREAD_PREFIX = "engine-http-"
        internal const val SOURCE_THREAD_PREFIX = "engine-source-"
        internal const val EXTENSION_THREAD_PREFIX = "engine-extension-"

        private fun namedFixedPool(
            threads: Int,
            prefix: String,
        ): ExecutorService = Executors.newFixedThreadPool(threads, RpcThreadFactory(prefix))
    }
}

/** A non-daemon, normal-priority thread factory with a counter scoped to this factory instance. */
internal class RpcThreadFactory(
    private val prefix: String,
) : ThreadFactory {
    private val group: ThreadGroup? = Thread.currentThread().threadGroup
    private val nextThreadNumber = AtomicInteger(1)

    init {
        require(prefix.isNotBlank()) { "thread prefix must not be blank" }
    }

    override fun newThread(runnable: Runnable): Thread =
        Thread(group, runnable, "$prefix${nextThreadNumber.getAndIncrement()}", 0).apply {
            if (isDaemon) isDaemon = false
            if (priority != Thread.NORM_PRIORITY) priority = Thread.NORM_PRIORITY
        }
}
