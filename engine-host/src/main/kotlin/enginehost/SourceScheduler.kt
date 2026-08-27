package enginehost

import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.concurrent.CompletableFuture
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import java.util.concurrent.Future
import java.util.concurrent.FutureTask
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock

data class SourceWork<T>(
    val sourceId: Long,
    val run: () -> T,
)

sealed interface Submission<out T> {
    data class Accepted<T>(val future: CompletableFuture<T>) : Submission<T>

    data object Rejected : Submission<Nothing>
}

data class SourceSchedulerLimits(
    val workerCount: Int = 8,
    val perSourceLimit: Int = 2,
    val queueCapacity: Int = 128,
) {
    init {
        require(workerCount > 0) { "workerCount must be positive" }
        require(perSourceLimit > 0) { "perSourceLimit must be positive" }
        require(queueCapacity > 0) { "queueCapacity must be positive" }
    }
}

data class SourceSchedulerSourceSnapshot(
    val sourceId: Long,
    val queued: Int,
    val running: Int,
)

data class SourceSchedulerSnapshot(
    val sourceWorkers: Int,
    val perSourceLimit: Int,
    val queued: Int,
    val running: Int,
    val completionSequence: Long,
    val oldestRunningMillis: Long,
    val completed: Long,
    val cancelled: Long,
    val timedOut: Long,
    val rejected: Long,
    val sources: List<SourceSchedulerSourceSnapshot>,
)

/**
 * A bounded, keyed scheduler for extension source calls. Waiting work is FIFO within one source;
 * runnable sources rotate after every admission, while physical occupancy remains capped even if a
 * caller terminally completes the public result before non-cooperative source code returns.
 */
class SourceScheduler(
    val limits: SourceSchedulerLimits = SourceSchedulerLimits(),
    private val clock: Clock = Clock.systemUTC(),
    workerExecutor: ExecutorService? = null,
    sourceCallDeadline: SourceCallDeadline? = null,
) : AutoCloseable {
    private val lock = ReentrantLock()
    private val queues = linkedMapOf<Long, ArrayDeque<ScheduledWork<*>>>()
    private val runnableSources = ArrayDeque<Long>()
    private val runnableSet = mutableSetOf<Long>()
    private val runningBySource = mutableMapOf<Long, Int>()
    private val runningWork = mutableSetOf<ScheduledWork<*>>()
    private val workers =
        workerExecutor ?: Executors.newFixedThreadPool(limits.workerCount, RpcThreadFactory(SOURCE_THREAD_PREFIX))
    private val ownsDeadline = sourceCallDeadline == null
    private val deadline = sourceCallDeadline ?: SourceCallDeadline()

    private var closed = false
    private var queuedCount = 0
    private var physicalRunning = 0
    private var completionSequence = 0L
    private var completedCount = 0L
    private var cancelledCount = 0L
    private var timedOutCount = 0L
    private var rejectedCount = 0L

    internal val isTerminated: Boolean
        get() = workers.isTerminated

    fun <T> submit(
        sourceId: Long,
        work: () -> T,
    ): Submission<T> = submit(sourceId, {}, work)

    fun <T> submit(
        sourceId: Long,
        cancelUnderlying: () -> Unit,
        work: () -> T,
    ): Submission<T> = submit(SourceWork(sourceId, work), cancelUnderlying)

    private fun <T> submit(
        work: SourceWork<T>,
        cancelUnderlying: () -> Unit,
    ): Submission<T> {
        val scheduled = ScheduledWork(work, cancelUnderlying = cancelUnderlying)
        val accepted =
            lock.withLock {
                val canUseFreeWorker =
                    physicalRunning < limits.workerCount &&
                        runningBySource.getOrDefault(work.sourceId, 0) < limits.perSourceLimit
                if (closed || (queuedCount >= limits.queueCapacity && !canUseFreeWorker)) {
                    rejectedCount++
                    false
                } else {
                    queues.getOrPut(work.sourceId) { ArrayDeque() }.addLast(scheduled)
                    queuedCount++
                    ensureRunnableLocked(work.sourceId)
                    admitLocked()
                    true
                }
            }
        if (!accepted) return Submission.Rejected

        scheduled.result.whenComplete { _, failure -> publicResultCompleted(scheduled, failure) }
        return Submission.Accepted(scheduled.result)
    }

    fun snapshot(now: Instant): SourceSchedulerSnapshot =
        lock.withLock {
            val sourceIds = (queues.keys + runningBySource.keys).toSortedSet()
            val oldest =
                runningWork.filter { it.startedAt != Instant.EPOCH }.maxOfOrNull { running ->
                    Duration.between(running.startedAt, now).toMillis().coerceAtLeast(0)
                } ?: 0L
            SourceSchedulerSnapshot(
                sourceWorkers = limits.workerCount,
                perSourceLimit = limits.perSourceLimit,
                queued = queuedCount,
                running = physicalRunning,
                completionSequence = completionSequence,
                oldestRunningMillis = oldest,
                completed = completedCount,
                cancelled = cancelledCount,
                timedOut = timedOutCount,
                rejected = rejectedCount,
                sources =
                    sourceIds.map { sourceId ->
                        SourceSchedulerSourceSnapshot(
                            sourceId = sourceId,
                            queued = queues[sourceId]?.size ?: 0,
                            running = runningBySource[sourceId] ?: 0,
                        )
                    },
            )
        }

    override fun close() {
        val publicResults =
            lock.withLock {
                if (closed) return
                closed = true
                val allWork = queues.values.flatten() + runningWork
                queues.clear()
                runnableSources.clear()
                runnableSet.clear()
                queuedCount = 0
                allWork.forEach { scheduled ->
                    if (scheduled.publicState == PublicState.PENDING) {
                        scheduled.publicState = PublicState.CANCELLED
                        cancelledCount++
                    }
                    if (scheduled.physicalState == PhysicalState.QUEUED) {
                        scheduled.physicalState = PhysicalState.RETURNED
                    }
                }
                allWork.map { it.result }
            }
        publicResults.forEach { it.cancel(false) }

        workers.shutdownNow()
        try {
            workers.awaitTermination(TERMINATION_SECONDS, TimeUnit.SECONDS)
        } catch (_: InterruptedException) {
            Thread.currentThread().interrupt()
        }
        if (ownsDeadline) deadline.close()
    }

    private fun ensureRunnableLocked(sourceId: Long) {
        if (queues[sourceId].isNullOrEmpty()) return
        if (runningBySource.getOrDefault(sourceId, 0) >= limits.perSourceLimit) return
        if (runnableSet.add(sourceId)) runnableSources.addLast(sourceId)
    }

    private fun admitLocked() {
        while (physicalRunning < limits.workerCount && runnableSources.isNotEmpty()) {
            val sourceId = runnableSources.removeFirst()
            runnableSet.remove(sourceId)
            val queue = queues[sourceId]
            if (queue.isNullOrEmpty() || runningBySource.getOrDefault(sourceId, 0) >= limits.perSourceLimit) {
                if (queue.isNullOrEmpty()) queues.remove(sourceId)
                continue
            }

            val scheduled = queue.removeFirst()
            queuedCount--
            if (queue.isEmpty()) queues.remove(sourceId)
            scheduled.physicalState = PhysicalState.RUNNING
            physicalRunning++
            runningBySource[sourceId] = runningBySource.getOrDefault(sourceId, 0) + 1
            runningWork += scheduled
            ensureRunnableLocked(sourceId)
            try {
                val physical = FutureTask<Any?> { runPhysical(scheduled) }
                scheduled.physical = physical
                workers.execute(physical)
            } catch (_: RejectedExecutionException) {
                physicalReturnedLocked(scheduled)
                if (scheduled.publicState == PublicState.PENDING) {
                    scheduled.publicState = PublicState.CANCELLED
                    cancelledCount++
                    scheduled.result.cancel(false)
                }
            }
        }
    }

    private fun <T> runPhysical(scheduled: ScheduledWork<T>): T {
        val startedAt = clock.instant()
        @Suppress("UNCHECKED_CAST")
        val physical = scheduled.physical as Future<T>
        deadline.supervise(physical, scheduled.result, scheduled.cancelUnderlying)
        lock.withLock { scheduled.startedAt = startedAt }
        try {
            val value = scheduled.work.run()
            scheduled.result.complete(value)
            return value
        } catch (caught: Throwable) {
            scheduled.result.completeExceptionally(caught)
            throw caught
        } finally {
            lock.withLock { physicalReturnedLocked(scheduled) }
        }
    }

    private fun physicalReturnedLocked(scheduled: ScheduledWork<*>) {
        if (scheduled.physicalState != PhysicalState.RUNNING) return
        scheduled.physicalState = PhysicalState.RETURNED
        physicalRunning--
        runningWork.remove(scheduled)
        val sourceId = scheduled.work.sourceId
        val remaining = runningBySource.getValue(sourceId) - 1
        if (remaining == 0) runningBySource.remove(sourceId) else runningBySource[sourceId] = remaining
        completionSequence++
        ensureRunnableLocked(sourceId)
        admitLocked()
    }

    private fun publicResultCompleted(
        scheduled: ScheduledWork<*>,
        failure: Throwable?,
    ) {
        lock.withLock {
            if (scheduled.publicState != PublicState.PENDING) return
            when {
                scheduled.result.isCancelled -> {
                    scheduled.publicState = PublicState.CANCELLED
                    cancelledCount++
                    if (scheduled.physicalState == PhysicalState.QUEUED) removeQueuedLocked(scheduled)
                }
                failure is TimeoutException && scheduled.physicalState != PhysicalState.RETURNED -> {
                    scheduled.publicState = PublicState.TIMED_OUT
                    timedOutCount++
                }
                else -> {
                    scheduled.publicState = PublicState.COMPLETED
                    completedCount++
                }
            }
        }
    }

    private fun removeQueuedLocked(scheduled: ScheduledWork<*>) {
        val sourceId = scheduled.work.sourceId
        val queue = queues[sourceId] ?: return
        if (!queue.remove(scheduled)) return
        scheduled.physicalState = PhysicalState.RETURNED
        queuedCount--
        if (queue.isEmpty()) {
            queues.remove(sourceId)
            if (runnableSet.remove(sourceId)) runnableSources.remove(sourceId)
        }
    }

    private class ScheduledWork<T>(
        val work: SourceWork<T>,
        val result: CompletableFuture<T> = CompletableFuture(),
        val cancelUnderlying: () -> Unit,
        var physical: Future<Any?>? = null,
        var physicalState: PhysicalState = PhysicalState.QUEUED,
        var publicState: PublicState = PublicState.PENDING,
        var startedAt: Instant = Instant.EPOCH,
    )

    private enum class PhysicalState {
        QUEUED,
        RUNNING,
        RETURNED,
    }

    private enum class PublicState {
        PENDING,
        COMPLETED,
        CANCELLED,
        TIMED_OUT,
    }

    private companion object {
        const val SOURCE_THREAD_PREFIX = "engine-source-"
        const val TERMINATION_SECONDS = 5L
    }
}
