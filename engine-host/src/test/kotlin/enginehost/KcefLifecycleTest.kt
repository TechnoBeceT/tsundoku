package enginehost

import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.delay
import kotlinx.coroutines.joinAll
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertNull
import kotlin.test.assertTrue
import kotlin.time.Duration
import kotlin.time.Duration.Companion.milliseconds
import kotlin.time.Duration.Companion.seconds

class KcefLifecycleTest {
    @Test
    fun `disabled settles synchronously and rejects callers without starting producer`() =
        runBlocking {
            val starts = AtomicInteger()
            val lifecycle = testLifecycle(initialize = { starts.incrementAndGet() })
            try {
                lifecycle.start(enabled = false)

                assertEquals(KcefStatus(KcefState.DISABLED, null), lifecycle.snapshot())
                val error = runCatching { lifecycle.awaitReady(1.seconds) }.exceptionOrNull()
                assertIs<WebViewUnavailableException>(error)
                assertEquals("embedded browser unavailable", error.message)
                assertNull(error.cause)
                lifecycle.start(enabled = true)
                assertEquals(0, starts.get())
            } finally {
                lifecycle.close()
            }
        }

    @Test
    fun `successful initialization settles ready`() =
        runBlocking {
            val release = CompletableDeferred<Unit>()
            val lifecycle = testLifecycle(initialize = { release.await() })
            try {
                lifecycle.start(enabled = true)
                assertEquals(KcefStatus(KcefState.INITIALIZING, null), lifecycle.snapshot())

                release.complete(Unit)
                lifecycle.awaitReady(1.seconds)

                assertEquals(KcefStatus(KcefState.READY, null), lifecycle.snapshot())
            } finally {
                lifecycle.close()
            }
        }

    @Test
    fun `initializer exception settles failed without exposing exception text`() =
        runBlocking {
            val secret = "https://private.invalid token=do-not-leak"
            val lifecycle = testLifecycle(initialize = { error(secret) })
            try {
                lifecycle.start(enabled = true)
                val error = runCatching { lifecycle.awaitReady(1.seconds) }.exceptionOrNull()

                assertIs<WebViewUnavailableException>(error)
                assertEquals("embedded browser unavailable", error.message)
                assertNull(error.cause)
                assertEquals(KcefStatus(KcefState.FAILED, "init_failed"), lifecycle.snapshot())
                assertFalse(jacksonObjectMapper().writeValueAsString(lifecycle.snapshot()).contains(secret))
            } finally {
                lifecycle.close()
            }
        }

    @Test
    fun `initializer cancellation settles failed when the lifecycle itself remains active`() =
        runBlocking {
            val lifecycle = testLifecycle(initialize = { throw CancellationException("upstream cancelled") })
            try {
                lifecycle.start(enabled = true)

                awaitState(lifecycle, KcefState.FAILED)

                assertEquals(KcefStatus(KcefState.FAILED, "init_failed"), lifecycle.snapshot())
            } finally {
                lifecycle.close()
            }
        }

    @Test
    fun `producer timeout settles failed with timeout code`() =
        runBlocking {
            val never = CompletableDeferred<Unit>()
            val lifecycle =
                testLifecycle(
                    initialize = { never.await() },
                    initializationTimeout = 40.milliseconds,
                )
            try {
                lifecycle.start(enabled = true)

                awaitState(lifecycle, KcefState.FAILED)

                assertEquals(KcefStatus(KcefState.FAILED, "init_timeout"), lifecycle.snapshot())
            } finally {
                lifecycle.close()
            }
        }

    @Test
    fun `concurrent starters and callers share one producer`() =
        runBlocking {
            val starts = AtomicInteger()
            val entered = CompletableDeferred<Unit>()
            val release = CompletableDeferred<Unit>()
            val lifecycle =
                testLifecycle(
                    initialize = {
                        starts.incrementAndGet()
                        entered.complete(Unit)
                        release.await()
                    },
                )
            try {
                List(12) { launch(Dispatchers.Default) { lifecycle.start(enabled = true) } }.joinAll()
                entered.await()
                val callers = List(12) { async(Dispatchers.Default) { lifecycle.awaitReady(1.seconds) } }

                release.complete(Unit)
                callers.awaitAll()

                assertEquals(1, starts.get())
                assertEquals(KcefState.READY, lifecycle.snapshot().state)
            } finally {
                lifecycle.close()
            }
        }

    @Test
    fun `caller timeout does not cancel or poison shared initialization`() =
        runBlocking {
            val release = CompletableDeferred<Unit>()
            val lifecycle = testLifecycle(initialize = { release.await() })
            try {
                lifecycle.start(enabled = true)

                val first = runCatching { lifecycle.awaitReady(30.milliseconds) }.exceptionOrNull()

                assertIs<WebViewUnavailableException>(first)
                assertEquals(KcefState.INITIALIZING, lifecycle.snapshot().state)
                release.complete(Unit)
                lifecycle.awaitReady(1.seconds)
                assertEquals(KcefState.READY, lifecycle.snapshot().state)
            } finally {
                lifecycle.close()
            }
        }

    @Test
    fun `caller cancellation does not cancel or poison shared initialization`() =
        runBlocking {
            val release = CompletableDeferred<Unit>()
            val lifecycle = testLifecycle(initialize = { release.await() })
            try {
                lifecycle.start(enabled = true)
                val caller = launch(start = CoroutineStart.UNDISPATCHED) { lifecycle.awaitReady(1.seconds) }

                caller.cancelAndJoin()

                assertEquals(KcefState.INITIALIZING, lifecycle.snapshot().state)
                release.complete(Unit)
                lifecycle.awaitReady(1.seconds)
                assertEquals(KcefState.READY, lifecycle.snapshot().state)
            } finally {
                lifecycle.close()
            }
        }

    @Test
    fun `late producer success serves callers after an earlier caller timed out`() =
        runBlocking {
            val release = CompletableDeferred<Unit>()
            val lifecycle = testLifecycle(initialize = { release.await() })
            try {
                lifecycle.start(enabled = true)
                assertIs<WebViewUnavailableException>(
                    runCatching { lifecycle.awaitReady(20.milliseconds) }.exceptionOrNull(),
                )

                delay(20.milliseconds)
                release.complete(Unit)
                lifecycle.awaitReady(1.seconds)

                assertEquals(KcefStatus(KcefState.READY, null), lifecycle.snapshot())
            } finally {
                lifecycle.close()
            }
        }

    @Test
    fun `capability monitor makes ready to failed terminal for the process generation`() =
        runBlocking {
            val healthy = AtomicBoolean(true)
            val lifecycle =
                testLifecycle(
                    initialize = {},
                    capabilityProbe = { healthy.get() },
                    monitorInterval = 5.milliseconds,
                )
            try {
                lifecycle.start(enabled = true)
                lifecycle.awaitReady(1.seconds)
                healthy.set(false)

                awaitState(lifecycle, KcefState.FAILED)
                healthy.set(true)
                delay(30.milliseconds)

                assertEquals(KcefStatus(KcefState.FAILED, "init_failed"), lifecycle.snapshot())
            } finally {
                lifecycle.close()
            }
        }

    @Test
    fun `capability probe cancellation reports loss when the monitor itself remains active`() =
        runBlocking {
            val lifecycle =
                testLifecycle(
                    initialize = {},
                    capabilityProbe = { throw CancellationException("probe cancelled") },
                    monitorInterval = 5.milliseconds,
                )
            try {
                lifecycle.start(enabled = true)
                lifecycle.awaitReady(1.seconds)

                awaitState(lifecycle, KcefState.FAILED)

                assertEquals(KcefStatus(KcefState.FAILED, "init_failed"), lifecycle.snapshot())
            } finally {
                lifecycle.close()
            }
        }

    private fun testLifecycle(
        initialize: suspend () -> Unit,
        capabilityProbe: (suspend () -> Boolean)? = null,
        initializationTimeout: Duration = 2.seconds,
        monitorInterval: Duration = 1.seconds,
    ): KcefLifecycle =
        KcefLifecycle(
            initialize = initialize,
            capabilityProbe = capabilityProbe,
            initializationTimeout = initializationTimeout,
            callerTimeout = 1.seconds,
            monitorInterval = monitorInterval,
            monitorProbeTimeout = 100.milliseconds,
        )

    private suspend fun awaitState(
        lifecycle: KcefLifecycle,
        state: KcefState,
    ) {
        withTimeout(1.seconds) {
            while (lifecycle.snapshot().state != state) delay(5.milliseconds)
        }
        assertTrue(lifecycle.snapshot().state == state)
    }
}
