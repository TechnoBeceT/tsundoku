package enginehost

import java.util.concurrent.ArrayBlockingQueue
import java.util.concurrent.ExecutorService
import java.util.concurrent.RejectedExecutionHandler
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.ThreadFactory
import java.util.concurrent.ThreadPoolExecutor
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock

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
 * through a keyed scheduler. Extension preparation/networking has a bounded lane distinct from
 * local registry and preference mutation, while the HTTP front door remains free to dispatch
 * control requests.
 */
class RpcExecutors(
    frontDoorThreads: Int = FRONT_DOOR_THREADS,
    sourceScheduler: SourceScheduler? = null,
    extensionExecutor: ExecutorService? = null,
    extensionNetworkExecutor: ExecutorService? = null,
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
    internal val clientConnectionObserver = ClientConnectionObserver()
    private val extensionThreadFactory = RpcThreadFactory(EXTENSION_THREAD_PREFIX)
    private val defaultExtensionState = ExtensionExecutorState(extensionQueueCapacity)
    val extensionExecutor: ExecutorService =
        extensionExecutor ?:
            observedExtensionPool(extensionQueueCapacity, extensionThreadFactory, defaultExtensionState)
    val extensionNetworkExecutor: ExecutorService =
        extensionNetworkExecutor ?:
            observedExtensionPool(extensionQueueCapacity, extensionThreadFactory, defaultExtensionState)

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

    fun extensionSnapshot(): ExtensionExecutorSnapshot {
        val snapshots = mutableListOf<ExtensionExecutorSnapshot>()
        val observedStates = mutableSetOf<ExtensionExecutorState>()
        listOf(extensionExecutor, extensionNetworkExecutor).distinct().forEach { executor ->
            if (executor is ObservedExtensionExecutor) {
                if (observedStates.add(executor.state)) snapshots.add(executor.snapshot())
            } else {
                snapshots.add(executorSnapshot(executor))
            }
        }
        return ExtensionExecutorSnapshot(
            running = snapshots.any { it.running },
            queued = snapshots.sumOf { it.queued },
        )
    }

    private fun executorSnapshot(executor: ExecutorService): ExtensionExecutorSnapshot =
        when (executor) {
            is ThreadPoolExecutor ->
                ExtensionExecutorSnapshot(
                    running = executor.activeCount > 0,
                    queued = executor.queue.size,
                )
            else -> ExtensionExecutorSnapshot(running = false, queued = 0)
        }

    override fun close() {
        if (!closed.compareAndSet(false, true)) return

        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(TERMINATION_SECONDS)
        val frontDoorQueued = frontDoorExecutor.shutdownNow()
        val extensionQueued = extensionExecutor.shutdownNow()
        val extensionNetworkQueued = extensionNetworkExecutor.shutdownNow()

        frontDoorQueued.forEach { task -> runFrontDoorRejection(RpcRejection.SHUTDOWN, task) }
        clientConnectionObserver.close()
        sourceScheduler.close()
        (extensionQueued + extensionNetworkQueued).forEach { task -> (task as? ShutdownAwareTask)?.shutdown() }

        listOf(frontDoorExecutor, extensionExecutor, extensionNetworkExecutor)
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

        private fun observedExtensionPool(
            queueCapacity: Int,
            threadFactory: ThreadFactory,
            state: ExtensionExecutorState,
        ): ExecutorService =
            ObservedExtensionExecutor(
                queueCapacity = queueCapacity,
                threadFactory = threadFactory,
                state = state,
            )
    }
}

/** Applies one aggregate queue bound and records occupancy across the extension execution lanes. */
private class ExtensionExecutorState(
    private val queueCapacity: Int,
) {
    private val lock = ReentrantLock()
    private var queuedCount = 0
    private var runningCount = 0

    fun reserve() {
        lock.withLock {
            if (queuedCount >= queueCapacity) throw RejectedExecutionException("extension queue is full")
            queuedCount++
        }
    }

    fun start() {
        lock.withLock {
            queuedCount--
            runningCount++
        }
    }

    fun finish() {
        lock.withLock { runningCount-- }
    }

    fun reject() {
        lock.withLock { queuedCount-- }
    }

    fun snapshot(): ExtensionExecutorSnapshot =
        lock.withLock {
            ExtensionExecutorSnapshot(running = runningCount > 0, queued = queuedCount)
        }
}

/** Tracks extension-domain occupancy without consulting extension state or waiting on its lock. */
private class ObservedExtensionExecutor(
    queueCapacity: Int,
    threadFactory: ThreadFactory,
    val state: ExtensionExecutorState,
) : ThreadPoolExecutor(
        1,
        1,
        0L,
        TimeUnit.MILLISECONDS,
        ArrayBlockingQueue(queueCapacity),
    threadFactory,
    AbortPolicy(),
) {
    override fun execute(command: Runnable) {
        val observed = ObservedTask(command)
        state.reserve()
        try {
            super.execute(observed)
        } catch (failure: RejectedExecutionException) {
            observed.reject()
            throw failure
        }
    }

    override fun shutdownNow(): MutableList<Runnable> =
        super.shutdownNow().mapTo(mutableListOf()) { queued ->
            (queued as? ObservedTask)?.drain() ?: queued
        }

    fun snapshot(): ExtensionExecutorSnapshot =
        state.snapshot()

    private inner class ObservedTask(
        val delegate: Runnable,
    ) : ShutdownAwareTask {
        private val claimed = AtomicBoolean(false)

        override fun run() {
            if (!claimed.compareAndSet(false, true)) return
            state.start()
            try {
                delegate.run()
            } finally {
                state.finish()
            }
        }

        override fun shutdown() {
            (delegate as? ShutdownAwareTask)?.shutdown()
        }

        fun reject() {
            if (claimed.compareAndSet(false, true)) state.reject()
        }

        fun drain(): Runnable {
            reject()
            return delegate
        }
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
