package enginehost

import com.fasterxml.jackson.annotation.JsonValue
import io.github.oshai.kotlinlogging.KotlinLogging
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.cancel
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeout
import kotlin.coroutines.CoroutineContext
import kotlin.time.Duration
import kotlin.time.Duration.Companion.seconds

/** Maximum time the shared producer may spend initializing embedded Chromium. */
val KCEFInitializationTimeout: Duration = 120.seconds

/** Maximum time one WebView caller may wait for the shared producer. */
val KCEFCallerTimeout: Duration = 30.seconds

private val kcefMonitorInterval: Duration = 1.seconds
private val kcefMonitorProbeTimeout: Duration = 5.seconds
private const val KCEF_INIT_TIMEOUT_ERROR_CODE = "init_timeout"
private const val KCEF_INIT_FAILED_ERROR_CODE = "init_failed"

/** Finite embedded-browser capability states for one engine-host process generation. */
enum class KcefState(
    @get:JsonValue val wireValue: String,
) {
    DISABLED("disabled"),
    INITIALIZING("initializing"),
    READY("ready"),
    FAILED("failed"),
}

/** Sanitized embedded-browser capability evidence returned by the status endpoint. */
data class KcefStatus(
    val state: KcefState,
    val errorCode: String?,
)

/** Reports a bounded WebView-readiness failure without exposing initialization details. */
class WebViewUnavailableException : IllegalStateException("embedded browser unavailable")

/**
 * Owns the one embedded-browser producer and its terminal state for this process generation.
 * Caller timeouts only stop that caller's wait; they never cancel the shared producer or settlement.
 */
class KcefLifecycle(
    private val initialize: suspend () -> Unit,
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
        if (settled.state != KcefState.READY) throw WebViewUnavailableException()
    }

    /** Returns the current payload-safe capability state without waiting or probing. */
    fun snapshot(): KcefStatus = synchronized(lock) { status }

    override fun close() {
        synchronized(lock) {
            if (status.state == KcefState.INITIALIZING) {
                settleInitialLocked(KcefStatus(KcefState.FAILED, KCEF_INIT_FAILED_ERROR_CODE))
            }
        }
        scope.cancel()
    }

    private suspend fun runProducer() {
        val failure =
            try {
                withTimeout(initializationTimeout) { initialize() }
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
            settleInitial(KcefStatus(KcefState.FAILED, failure))
            return
        }
        if (!settleInitial(KcefStatus(KcefState.READY, null))) return
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
                synchronized(lock) {
                    if (status.state == KcefState.READY) {
                        status = KcefStatus(KcefState.FAILED, KCEF_INIT_FAILED_ERROR_CODE)
                    }
                }
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
}
