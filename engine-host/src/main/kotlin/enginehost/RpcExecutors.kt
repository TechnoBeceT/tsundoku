package enginehost

import java.util.concurrent.ArrayBlockingQueue
import java.util.concurrent.ExecutorService
import java.util.concurrent.RejectedExecutionHandler
import java.util.concurrent.ThreadFactory
import java.util.concurrent.ThreadPoolExecutor
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger

internal enum class RpcRejection {
    CAPACITY,
    SHUTDOWN,
}

/** A queued exchange task that can still terminally complete when its executor is drained. */
internal interface ShutdownAwareTask : Runnable {
    fun shutdown()
}

/**
 * Owns the independent, bounded execution domains used by the RPC server. Source work passes
 * through a keyed scheduler, while extension work stays single-writer and the HTTP front door
 * remains free to dispatch control requests.
 */
class RpcExecutors(
    frontDoorThreads: Int = FRONT_DOOR_THREADS,
    sourceScheduler: SourceScheduler? = null,
    extensionExecutor: ExecutorService? = null,
    frontDoorQueueCapacity: Int = FRONT_DOOR_QUEUE_CAPACITY,
    sourceQueueCapacity: Int = SOURCE_QUEUE_CAPACITY,
    extensionQueueCapacity: Int = EXTENSION_QUEUE_CAPACITY,
) : AutoCloseable {
    private val closed = AtomicBoolean(false)
    private val frontDoorRejection = ThreadLocal<RpcRejection?>()

    init {
        require(frontDoorThreads > 0) { "frontDoorThreads must be positive" }
        require(frontDoorQueueCapacity > 0) { "frontDoorQueueCapacity must be positive" }
        require(sourceQueueCapacity > 0) { "sourceQueueCapacity must be positive" }
        require(extensionQueueCapacity > 0) { "extensionQueueCapacity must be positive" }
    }

    val sourceScheduler: SourceScheduler =
        sourceScheduler ?: SourceScheduler(SourceSchedulerLimits(queueCapacity = sourceQueueCapacity))
    val extensionExecutor: ExecutorService =
        extensionExecutor ?: boundedFixedPool(EXTENSION_THREADS, extensionQueueCapacity, EXTENSION_THREAD_PREFIX)

    val frontDoorExecutor: ExecutorService =
        boundedFixedPool(
            frontDoorThreads,
            frontDoorQueueCapacity,
            HTTP_THREAD_PREFIX,
            RejectedExecutionHandler { task, executor ->
                runFrontDoorRejection(
                    if (executor.isShutdown) RpcRejection.SHUTDOWN else RpcRejection.CAPACITY,
                    task,
                )
            },
        )

    internal fun currentFrontDoorRejection(): RpcRejection? = frontDoorRejection.get()

    override fun close() {
        if (!closed.compareAndSet(false, true)) return

        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(TERMINATION_SECONDS)
        val frontDoorQueued = frontDoorExecutor.shutdownNow()
        val extensionQueued = extensionExecutor.shutdownNow()

        frontDoorQueued.forEach { task -> runFrontDoorRejection(RpcRejection.SHUTDOWN, task) }
        sourceScheduler.close()
        extensionQueued.forEach { task -> (task as? ShutdownAwareTask)?.shutdown() }

        listOf(frontDoorExecutor, extensionExecutor)
            .distinct()
            .forEach { executor -> awaitTermination(executor, deadline) }
    }

    internal fun runFrontDoorRejection(
        rejection: RpcRejection,
        task: Runnable,
    ) {
        val previous = frontDoorRejection.get()
        frontDoorRejection.set(rejection)
        try {
            task.run()
        } finally {
            if (previous == null) frontDoorRejection.remove() else frontDoorRejection.set(previous)
        }
    }

    private fun awaitTermination(
        executor: ExecutorService,
        deadline: Long,
    ) {
        val remaining = deadline - System.nanoTime()
        if (remaining <= 0) return
        try {
            executor.awaitTermination(remaining, TimeUnit.NANOSECONDS)
        } catch (_: InterruptedException) {
            Thread.currentThread().interrupt()
        }
    }

    companion object {
        private const val FRONT_DOOR_THREADS = 4
        private const val FRONT_DOOR_QUEUE_CAPACITY = 32
        private const val SOURCE_QUEUE_CAPACITY = 128
        private const val EXTENSION_THREADS = 1
        private const val EXTENSION_QUEUE_CAPACITY = 32
        private const val TERMINATION_SECONDS = 5L

        internal const val HTTP_THREAD_PREFIX = "engine-http-"
        internal const val EXTENSION_THREAD_PREFIX = "engine-extension-"

        private fun boundedFixedPool(
            threads: Int,
            queueCapacity: Int,
            prefix: String,
            rejectedExecutionHandler: RejectedExecutionHandler = ThreadPoolExecutor.AbortPolicy(),
        ): ExecutorService =
            ThreadPoolExecutor(
                threads,
                threads,
                0L,
                TimeUnit.MILLISECONDS,
                ArrayBlockingQueue(queueCapacity),
                RpcThreadFactory(prefix),
                rejectedExecutionHandler,
            )
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
