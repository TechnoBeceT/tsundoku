package enginehost

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.runBlocking
import okhttp3.Call
import java.time.Duration
import java.util.concurrent.CompletableFuture
import java.util.concurrent.Executors
import java.util.concurrent.Future
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException
import java.util.concurrent.atomic.AtomicBoolean

internal fun interface DeadlineTimerHandle {
    fun cancel()
}

internal fun interface DeadlineTimer {
    fun schedule(
        delay: Duration,
        task: () -> Unit,
    ): DeadlineTimerHandle
}

/**
 * Applies the host execution deadline independently of the physical source worker. The public
 * result can therefore time out while non-cooperative source code continues to occupy its worker.
 */
class SourceCallDeadline internal constructor(
    val timeout: Duration,
    private val timer: DeadlineTimer,
    private val closeTimer: () -> Unit = {},
) : AutoCloseable {
    constructor(timeout: Duration = DEFAULT_TIMEOUT) : this(timeout, ScheduledDeadlineTimer().let { owned ->
        OwnedDeadlineTimer(owned, owned::close)
    })

    private constructor(
        timeout: Duration,
        owned: OwnedDeadlineTimer,
    ) : this(timeout, owned.timer, owned.close)

    init {
        require(!timeout.isNegative && !timeout.isZero) { "timeout must be positive" }
    }

    /** Starts only when the scheduler's physical callable invokes this method. */
    fun <T> supervise(
        physical: Future<T>,
        publicResult: CompletableFuture<T>,
        cancelUnderlying: () -> Unit,
    ) {
        val cancellationStarted = AtomicBoolean(false)
        fun cancelPhysicalAndUnderlying() {
            if (!cancellationStarted.compareAndSet(false, true)) return
            runCatching(cancelUnderlying)
            physical.cancel(true)
        }

        val timeoutTask =
            timer.schedule(timeout) {
                if (publicResult.completeExceptionally(TimeoutException(TIMEOUT_MESSAGE))) {
                    cancelPhysicalAndUnderlying()
                }
            }
        publicResult.whenComplete { _, _ ->
            timeoutTask.cancel()
            if (publicResult.isCancelled) cancelPhysicalAndUnderlying()
        }
    }

    override fun close() = closeTimer()

    private class ScheduledDeadlineTimer : DeadlineTimer {
        private val executor: ScheduledExecutorService =
            Executors.newSingleThreadScheduledExecutor(RpcThreadFactory(DEADLINE_THREAD_PREFIX))

        override fun schedule(
            delay: Duration,
            task: () -> Unit,
        ): DeadlineTimerHandle {
            val future = executor.schedule(task, delay.toNanos(), TimeUnit.NANOSECONDS)
            return DeadlineTimerHandle { future.cancel(false) }
        }

        fun close() {
            executor.shutdownNow()
        }
    }

    private data class OwnedDeadlineTimer(
        val timer: DeadlineTimer,
        val close: () -> Unit,
    )

    private companion object {
        val DEFAULT_TIMEOUT: Duration = Duration.ofSeconds(150)
        const val TIMEOUT_MESSAGE = "source call timed out"
        const val DEADLINE_THREAD_PREFIX = "engine-deadline-"
    }
}

/** A source call's cancellable coroutine job plus any direct blocking OkHttp calls it owns. */
class SourceCallCancellation {
    private val job: Job = SupervisorJob()
    private val lock = Any()
    private val calls = mutableSetOf<Call>()
    private var cancelled = false

    fun <T> run(block: suspend CoroutineScope.() -> T): T = runBlocking(job) { block() }

    fun <T> withCall(
        call: Call,
        block: (Call) -> T,
    ): T {
        synchronized(lock) {
            if (cancelled) call.cancel() else calls += call
        }
        try {
            return block(call)
        } finally {
            synchronized(lock) { calls.remove(call) }
        }
    }

    suspend fun <T> withCallSuspend(
        call: Call,
        block: suspend (Call) -> T,
    ): T {
        synchronized(lock) {
            if (cancelled) call.cancel() else calls += call
        }
        try {
            return block(call)
        } finally {
            synchronized(lock) { calls.remove(call) }
        }
    }

    fun ensureActive() = job.ensureActive()

    fun cancel() {
        val retained =
            synchronized(lock) {
                if (cancelled) return
                cancelled = true
                calls.toList().also { calls.clear() }
            }
        job.cancel()
        retained.forEach(Call::cancel)
    }
}
