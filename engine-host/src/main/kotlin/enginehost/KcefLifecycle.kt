package enginehost

import com.fasterxml.jackson.annotation.JsonValue
import io.github.oshai.kotlinlogging.KotlinLogging
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.cancel
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withTimeout
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicReference
import java.util.concurrent.locks.ReentrantLock
import kotlin.coroutines.CoroutineContext
import kotlin.concurrent.withLock
import kotlin.time.Duration
import kotlin.time.Duration.Companion.seconds

/** [KCEFInitializationTimeout] is the maximum time the shared producer may initialize Chromium. */
val KCEFInitializationTimeout: Duration = 120.seconds

/** [KCEFCallerTimeout] is the maximum time one WebView caller may wait for the shared producer. */
val KCEFCallerTimeout: Duration = 30.seconds

/** [KCEFShutdownCleanupTimeout] bounds JVM-shutdown waiting for embedded-browser disposal. */
val KCEFShutdownCleanupTimeout: Duration = 4.seconds

private val kcefMonitorInterval: Duration = 1.seconds
private val kcefMonitorProbeTimeout: Duration = 5.seconds
private const val KCEF_INIT_TIMEOUT_ERROR_CODE = "init_timeout"
private const val KCEF_INIT_FAILED_ERROR_CODE = "init_failed"

/** [KcefState] enumerates the finite capability states for one engine-host process generation. */
enum class KcefState(
    @get:JsonValue val wireValue: String,
) {
    DISABLED("disabled"),
    INITIALIZING("initializing"),
    READY("ready"),
    FAILED("failed"),
}

/** [KcefStatus] is the sanitized embedded-browser evidence returned by the status endpoint. */
data class KcefStatus(
    val state: KcefState,
    val errorCode: String?,
)

/** [WebViewUnavailableException] reports bounded readiness failure without initialization details. */
class WebViewUnavailableException : IllegalStateException("embedded browser unavailable")

/** [KcefShutdownCleanup] awaits one shutdown's evolving cleanup chain within its original deadline. */
internal class KcefShutdownCleanup(
    private val awaiter: () -> Boolean,
) {
    /** [awaitCompletion] returns when cleanup is quiescent or the deadline begun at shutdown expires. */
    fun awaitCompletion(): Boolean = awaiter()
}

/**
 * [KcefLifecycle] owns one embedded-browser producer and its terminal process-generation state.
 * Caller timeouts only stop that caller's wait; they never cancel the shared producer or settlement.
 * [cleanup] must be idempotent: terminal settlement schedules it independently, and a
 * non-cooperative physical initializer that exits late schedules it again.
 */
class KcefLifecycle(
    private val initialize: suspend () -> Unit,
    private val cleanup: () -> Unit = {},
    private val capabilityProbe: (suspend () -> Boolean)? = null,
    private val initializationTimeout: Duration = KCEFInitializationTimeout,
    private val callerTimeout: Duration = KCEFCallerTimeout,
    private val monitorInterval: Duration = kcefMonitorInterval,
    private val monitorProbeTimeout: Duration = kcefMonitorProbeTimeout,
    coroutineContext: CoroutineContext = Dispatchers.IO,
) : AutoCloseable {
    private val logger = KotlinLogging.logger {}
    private val lock = Any()
    private val scope = CoroutineScope(SupervisorJob() + coroutineContext)
    private val cleanupScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val cleanupLock = Mutex()
    private val cleanupTracker = CleanupTracker()
    private val initialSettlement = CompletableDeferred<KcefStatus>()
    private var started = false
    private var status = KcefStatus(KcefState.DISABLED, null)
    private var initializationJob: OwnedInitializer? = null

    init {
        require(initializationTimeout.isPositive()) { "initializationTimeout must be positive" }
        require(callerTimeout.isPositive()) { "callerTimeout must be positive" }
        require(monitorInterval.isPositive()) { "monitorInterval must be positive" }
        require(monitorProbeTimeout.isPositive()) { "monitorProbeTimeout must be positive" }
    }

    /** [start] starts at most one producer, or settles disabled synchronously when capability is off. */
    fun start(enabled: Boolean) {
        val launchProducer =
            synchronized(lock) {
                if (started) return
                started = true
                if (!enabled) {
                    initialSettlement.complete(status)
                    false
                } else {
                    status = KcefStatus(KcefState.INITIALIZING, null)
                    true
                }
            }
        if (launchProducer) scope.launch { runProducer() }
    }

    /** [awaitReady] waits within one caller's independent bound and fails with a sanitized exception. */
    suspend fun awaitReady(timeout: Duration = callerTimeout) {
        require(timeout.isPositive()) { "timeout must be positive" }
        val current = snapshot()
        if (current.state == KcefState.READY) return
        if (current.state != KcefState.INITIALIZING) throw WebViewUnavailableException()

        val settled =
            try {
                withTimeout(timeout) { initialSettlement.await() }
            } catch (_: TimeoutCancellationException) {
                throw WebViewUnavailableException()
            }
        if (settled.state != KcefState.READY || snapshot().state != KcefState.READY) {
            throw WebViewUnavailableException()
        }
    }

    /** [snapshot] returns the current payload-safe capability state without waiting or probing. */
    fun snapshot(): KcefStatus = synchronized(lock) { status }

    override fun close() {
        requestClose()
    }

    /** [beginShutdownCleanup] settles disabled and starts cleanup under one bound beginning now. */
    internal fun beginShutdownCleanup(timeout: Duration = KCEFShutdownCleanupTimeout): KcefShutdownCleanup {
        require(timeout.isPositive()) { "timeout must be positive" }
        val startedAtNanos = System.nanoTime()
        val timeoutNanos = timeout.inWholeNanoseconds.coerceAtLeast(1)
        requestClose()
        return KcefShutdownCleanup {
            val remainingNanos = timeoutNanos - (System.nanoTime() - startedAtNanos)
            awaitCleanupQuiescence(remainingNanos)
        }
    }

    /** [shutdownAndAwaitCleanup] settles disabled and waits within [timeout] for owned cleanup. */
    fun shutdownAndAwaitCleanup(timeout: Duration = KCEFShutdownCleanupTimeout): Boolean =
        beginShutdownCleanup(timeout).awaitCompletion()

    private fun awaitCleanupQuiescence(remainingNanos: Long): Boolean =
        try {
            cleanupTracker.awaitQuiescence(remainingNanos)
        } catch (_: InterruptedException) {
            Thread.currentThread().interrupt()
            false
        }

    private fun requestClose() {
        val plan =
            synchronized(lock) {
                val active = status.state == KcefState.INITIALIZING || status.state == KcefState.READY
                val initializer = initializationJob
                initializationJob = null
                started = true
                if (status.state == KcefState.INITIALIZING) {
                    settleInitialLocked(KcefStatus(KcefState.DISABLED, null))
                } else if (status.state == KcefState.READY) {
                    status = KcefStatus(KcefState.DISABLED, null)
                }
                val cleanupRequest = if (active) reserveCleanupLocked() else null
                ClosePlan(
                    initializer = if (active) initializer else null,
                    cleanupRequest = cleanupRequest,
                )
            }
        if (plan.cleanupRequest != null) {
            if (plan.initializer != null) {
                abandonInitializer(plan.initializer, plan.cleanupRequest)
            } else {
                launchCleanup(plan.cleanupRequest)
            }
        }
        scope.cancel()
    }

    private suspend fun runProducer() {
        // Register the owned child before it can enter native code, so close cannot miss cleanup.
        val cleanupAfterExit = AtomicReference<CleanupRequest?>()
        val initializer =
            OwnedInitializer(
                job = scope.async(start = CoroutineStart.LAZY) { initialize() },
                cleanupAfterExit = cleanupAfterExit,
            )
        initializer.job.invokeOnCompletion {
            if (initializer.tracked.get()) {
                val followUp =
                    initializer.cleanupAfterExit.get()?.let { terminalCleanup ->
                        if (terminalCleanup.initializerExitedAfterCleanupStarted()) reserveCleanup() else null
                    }
                cleanupTracker.completeInitializer()
                if (followUp != null) launchCleanup(followUp)
            }
        }
        val accepted =
            synchronized(lock) {
                if (status.state == KcefState.INITIALIZING) {
                    initializationJob = initializer
                    cleanupTracker.registerInitializer()
                    initializer.tracked.set(true)
                    initializer.job.start()
                    true
                } else {
                    false
                }
            }
        if (!accepted) {
            initializer.job.cancel()
            return
        }
        val failure =
            try {
                withTimeout(initializationTimeout) { initializer.job.await() }
                null
            } catch (_: TimeoutCancellationException) {
                KCEF_INIT_TIMEOUT_ERROR_CODE
            } catch (e: CancellationException) {
                if (!currentCoroutineContext().isActive) throw e
                logger.error(e) { "Embedded browser initialization was cancelled upstream" }
                KCEF_INIT_FAILED_ERROR_CODE
            } catch (e: Throwable) {
                logger.error(e) { "Embedded browser initialization failed" }
                KCEF_INIT_FAILED_ERROR_CODE
            }

        if (failure != null) {
            settleInitialFailure(initializer, KcefStatus(KcefState.FAILED, failure))?.let { cleanupRequest ->
                if (failure == KCEF_INIT_TIMEOUT_ERROR_CODE) {
                    abandonInitializer(initializer, cleanupRequest)
                } else {
                    launchCleanup(cleanupRequest)
                }
            }
            return
        }
        if (!settleInitial(initializer, KcefStatus(KcefState.READY, null))) return
        monitorCapability()
    }

    private suspend fun monitorCapability() {
        val probe = capabilityProbe ?: return
        while (currentCoroutineContext().isActive && snapshot().state == KcefState.READY) {
            delay(monitorInterval)
            val healthy =
                try {
                    withTimeout(monitorProbeTimeout) { probe() }
                } catch (_: TimeoutCancellationException) {
                    false
                } catch (e: CancellationException) {
                    if (!currentCoroutineContext().isActive) throw e
                    logger.error(e) { "Embedded browser capability probe was cancelled upstream" }
                    false
                } catch (e: Throwable) {
                    logger.error(e) { "Embedded browser capability probe failed" }
                    false
                }
            if (!healthy) {
                val cleanupRequest =
                    synchronized(lock) {
                        if (status.state == KcefState.READY) {
                            status = KcefStatus(KcefState.FAILED, KCEF_INIT_FAILED_ERROR_CODE)
                            reserveCleanupLocked()
                        } else {
                            null
                        }
                    }
                if (cleanupRequest != null) launchCleanup(cleanupRequest)
                return
            }
        }
    }

    private fun settleInitialFailure(
        initializer: OwnedInitializer,
        next: KcefStatus,
    ): CleanupRequest? =
        synchronized(lock) {
            if (status.state != KcefState.INITIALIZING || initializationJob !== initializer) {
                return@synchronized null
            }
            initializationJob = null
            settleInitialLocked(next)
            reserveCleanupLocked()
        }

    private fun settleInitial(
        initializer: OwnedInitializer,
        next: KcefStatus,
    ): Boolean =
        synchronized(lock) {
            if (status.state != KcefState.INITIALIZING || initializationJob !== initializer) {
                return@synchronized false
            }
            initializationJob = null
            settleInitialLocked(next)
            true
        }

    private fun settleInitialLocked(next: KcefStatus) {
        status = next
        initialSettlement.complete(next)
    }

    private fun reserveCleanupLocked(): CleanupRequest =
        reserveCleanup()

    private fun reserveCleanup(): CleanupRequest =
        CleanupRequest().also { cleanupTracker.registerCleanup() }

    private fun launchCleanup(request: CleanupRequest) {
        cleanupScope.launch {
            try {
                cleanupLock.withLock {
                    request.markCleanupStarted()
                    runCatching(cleanup).onFailure { logger.error(it) { "Embedded browser cleanup failed" } }
                }
            } finally {
                cleanupTracker.completeCleanup()
            }
        }
    }

    private fun abandonInitializer(
        initializer: OwnedInitializer,
        terminalCleanup: CleanupRequest,
    ) {
        initializer.cleanupAfterExit.set(terminalCleanup)
        initializer.job.cancel()
        launchCleanup(terminalCleanup)
    }

    private data class OwnedInitializer(
        val job: Deferred<Unit>,
        val cleanupAfterExit: AtomicReference<CleanupRequest?>,
        val tracked: AtomicBoolean = AtomicBoolean(),
    )

    private data class ClosePlan(
        val initializer: OwnedInitializer?,
        val cleanupRequest: CleanupRequest?,
    )

    private class CleanupRequest {
        private val initializerOrder = AtomicReference(InitializerOrder.PENDING)

        fun markCleanupStarted() {
            initializerOrder.compareAndSet(InitializerOrder.PENDING, InitializerOrder.CLEANUP_STARTED)
        }

        fun initializerExitedAfterCleanupStarted(): Boolean {
            while (true) {
                when (initializerOrder.get()) {
                    InitializerOrder.CLEANUP_STARTED -> return true
                    InitializerOrder.INITIALIZER_EXITED -> return false
                    InitializerOrder.PENDING -> {
                        if (
                            initializerOrder.compareAndSet(
                                InitializerOrder.PENDING,
                                InitializerOrder.INITIALIZER_EXITED,
                            )
                        ) {
                            return false
                        }
                    }
                }
            }
        }
    }

    private class CleanupTracker {
        private val lock = ReentrantLock()
        private val changed = lock.newCondition()
        private var initializers = 0
        private var cleanups = 0

        fun registerInitializer() = lock.withLock { initializers += 1 }

        fun completeInitializer() =
            lock.withLock {
                check(initializers > 0) { "initializer completion was not registered" }
                initializers -= 1
                changed.signalAll()
            }

        fun registerCleanup() = lock.withLock { cleanups += 1 }

        fun completeCleanup() =
            lock.withLock {
                check(cleanups > 0) { "cleanup completion was not registered" }
                cleanups -= 1
                changed.signalAll()
            }

        @Throws(InterruptedException::class)
        fun awaitQuiescence(timeoutNanos: Long): Boolean =
            lock.withLock {
                var remainingNanos = timeoutNanos
                while (initializers > 0 || cleanups > 0) {
                    if (remainingNanos <= 0) return false
                    remainingNanos = changed.awaitNanos(remainingNanos)
                }
                true
            }
    }

    private enum class InitializerOrder {
        PENDING,
        CLEANUP_STARTED,
        INITIALIZER_EXITED,
    }
}
