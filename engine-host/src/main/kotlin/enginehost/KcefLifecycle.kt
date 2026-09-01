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
import kotlinx.coroutines.withTimeout
import kotlin.coroutines.CoroutineContext
import kotlin.time.Duration
import kotlin.time.Duration.Companion.seconds

/** [KCEFInitializationTimeout] is the maximum time the shared producer may initialize Chromium. */
val KCEFInitializationTimeout: Duration = 120.seconds

/** [KCEFCallerTimeout] is the maximum time one WebView caller may wait for the shared producer. */
val KCEFCallerTimeout: Duration = 30.seconds

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

/**
 * [KcefLifecycle] owns one embedded-browser producer and its terminal process-generation state.
 * Caller timeouts only stop that caller's wait; they never cancel the shared producer or settlement.
 * [cleanup] must be idempotent: terminal abandonment runs it immediately and again after any
 * non-cooperative physical initializer eventually exits.
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
    private val initialSettlement = CompletableDeferred<KcefStatus>()
    private var started = false
    private var status = KcefStatus(KcefState.DISABLED, null)
    private var initializationJob: Deferred<Unit>? = null

    init {
        require(initializationTimeout.isPositive()) { "initializationTimeout must be positive" }
        require(callerTimeout.isPositive()) { "callerTimeout must be positive" }
        require(monitorInterval.isPositive()) { "monitorInterval must be positive" }
        require(monitorProbeTimeout.isPositive()) { "monitorProbeTimeout must be positive" }
    }

    /** Starts at most one producer, or settles disabled synchronously when capability is off. */
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

    /** Waits within one caller's independent bound, failing with a sanitized exception. */
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

    /** Returns the current payload-safe capability state without waiting or probing. */
    fun snapshot(): KcefStatus = synchronized(lock) { status }

    override fun close() {
        val (cleanupTarget, shouldCleanup) =
            synchronized(lock) {
                val active = status.state == KcefState.INITIALIZING || status.state == KcefState.READY
                val initializer = initializationJob
                initializationJob = null
                started = true
                if (status.state == KcefState.INITIALIZING) {
                    settleInitialLocked(KcefStatus(KcefState.FAILED, KCEF_INIT_FAILED_ERROR_CODE))
                } else if (status.state == KcefState.READY) {
                    status = KcefStatus(KcefState.FAILED, KCEF_INIT_FAILED_ERROR_CODE)
                }
                Pair(if (active) initializer else null, active)
            }
        if (cleanupTarget != null) {
            abandonInitializer(cleanupTarget)
        } else if (shouldCleanup) {
            cleanupSafely()
        }
        scope.cancel()
    }

    private suspend fun runProducer() {
        // Register the owned child before it can enter native code, so close cannot miss cleanup.
        val initializer = scope.async(start = CoroutineStart.LAZY) { initialize() }
        val accepted =
            synchronized(lock) {
                if (status.state == KcefState.INITIALIZING) {
                    initializationJob = initializer
                    true
                } else {
                    false
                }
            }
        if (!accepted) {
            initializer.cancel()
            return
        }
        initializer.start()
        val failure =
            try {
                withTimeout(initializationTimeout) { initializer.await() }
                null
            } catch (_: TimeoutCancellationException) {
                abandonInitializer(initializer)
                KCEF_INIT_TIMEOUT_ERROR_CODE
            } catch (e: CancellationException) {
                if (!currentCoroutineContext().isActive) throw e
                logger.error(e) { "Embedded browser initialization was cancelled upstream" }
                KCEF_INIT_FAILED_ERROR_CODE
            } catch (e: Throwable) {
                logger.error(e) { "Embedded browser initialization failed" }
                KCEF_INIT_FAILED_ERROR_CODE
            }

        synchronized(lock) {
            if (initializationJob === initializer) initializationJob = null
        }

        if (failure != null) {
            settleInitial(KcefStatus(KcefState.FAILED, failure))
            if (failure != KCEF_INIT_TIMEOUT_ERROR_CODE) cleanupSafely()
            return
        }
        if (!settleInitial(KcefStatus(KcefState.READY, null))) {
            cleanupSafely()
            return
        }
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
                val transitioned =
                    synchronized(lock) {
                        if (status.state == KcefState.READY) {
                            status = KcefStatus(KcefState.FAILED, KCEF_INIT_FAILED_ERROR_CODE)
                            true
                        } else {
                            false
                        }
                    }
                if (transitioned) cleanupSafely()
                return
            }
        }
    }

    private fun settleInitial(next: KcefStatus): Boolean =
        synchronized(lock) {
            if (status.state != KcefState.INITIALIZING) return@synchronized false
            settleInitialLocked(next)
            true
        }

    private fun settleInitialLocked(next: KcefStatus) {
        status = next
        initialSettlement.complete(next)
    }

    private fun cleanupSafely() {
        runCatching(cleanup).onFailure { logger.error(it) { "Embedded browser cleanup failed" } }
    }

    private fun abandonInitializer(initializer: Deferred<Unit>) {
        initializer.invokeOnCompletion { cleanupSafely() }
        initializer.cancel()
        cleanupSafely()
    }
}
