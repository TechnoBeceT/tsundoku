package enginehost

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlin.time.Duration.Companion.milliseconds
import kotlin.time.Duration.Companion.seconds

class MainTest {
    @Test
    fun `shutdown starts KCEF cleanup before server stop and lets both run concurrently`() =
        runBlocking {
            val cleanupEntered = CountDownLatch(1)
            val releaseCleanup = CountDownLatch(1)
            val cleanupExited = CountDownLatch(1)
            val serverStopEntered = CountDownLatch(1)
            val releaseServerStop = CountDownLatch(1)
            val cleanupWasRunningAtServerStop = AtomicBoolean()
            val extensionsClosed = CountDownLatch(1)
            val lifecycle =
                testLifecycle(
                    cleanup = {
                        cleanupEntered.countDown()
                        try {
                            releaseCleanup.await()
                        } finally {
                            cleanupExited.countDown()
                        }
                    },
                )
            lifecycle.start(enabled = true)
            lifecycle.awaitReady(1.seconds)
            try {
                val shutdown =
                    async(Dispatchers.Default) {
                        shutdownEngineHost(
                            kcefLifecycle = lifecycle,
                            stopServer = {
                                cleanupWasRunningAtServerStop.set(cleanupEntered.await(1, TimeUnit.SECONDS))
                                serverStopEntered.countDown()
                                releaseServerStop.await()
                            },
                            closeExtensions = extensionsClosed::countDown,
                            kcefCleanupTimeout = 1.seconds,
                        )
                    }
                assertTrue(serverStopEntered.await(2, TimeUnit.SECONDS))
                assertTrue(cleanupWasRunningAtServerStop.get())

                releaseCleanup.countDown()
                assertTrue(cleanupExited.await(1, TimeUnit.SECONDS))
                assertFalse(shutdown.isCompleted)
                assertFalse(extensionsClosed.await(0, TimeUnit.MILLISECONDS))

                releaseServerStop.countDown()

                assertTrue(withTimeout(1.seconds) { shutdown.await() })
                assertTrue(extensionsClosed.await(0, TimeUnit.MILLISECONDS))
            } finally {
                releaseCleanup.countDown()
                releaseServerStop.countDown()
                lifecycle.close()
            }
        }

    @Test
    fun `shutdown server stop consumes the existing KCEF cleanup deadline`() =
        runBlocking {
            val cleanupEntered = CountDownLatch(1)
            val releaseCleanup = CountDownLatch(1)
            val serverStopEntered = CountDownLatch(1)
            val releaseServerStop = CountDownLatch(1)
            val extensionsClosed = CountDownLatch(1)
            val lifecycle =
                testLifecycle(
                    cleanup = {
                        cleanupEntered.countDown()
                        releaseCleanup.await()
                    },
                )
            lifecycle.start(enabled = true)
            lifecycle.awaitReady(1.seconds)
            try {
                val shutdown =
                    async(Dispatchers.Default) {
                        shutdownEngineHost(
                            kcefLifecycle = lifecycle,
                            stopServer = {
                                serverStopEntered.countDown()
                                releaseServerStop.await()
                            },
                            closeExtensions = extensionsClosed::countDown,
                            kcefCleanupTimeout = 300.milliseconds,
                        )
                    }
                assertTrue(cleanupEntered.await(1, TimeUnit.SECONDS))
                assertTrue(serverStopEntered.await(1, TimeUnit.SECONDS))
                delay(400.milliseconds)

                releaseServerStop.countDown()

                assertFalse(withTimeout(150.milliseconds) { shutdown.await() })
                assertTrue(extensionsClosed.await(0, TimeUnit.MILLISECONDS))
            } finally {
                releaseCleanup.countDown()
                releaseServerStop.countDown()
                lifecycle.close()
            }
        }

    private fun testLifecycle(cleanup: () -> Unit): KcefLifecycle =
        KcefLifecycle(
            initialize = {},
            cleanup = cleanup,
            initializationTimeout = 1.seconds,
            callerTimeout = 1.seconds,
            monitorInterval = 1.seconds,
            monitorProbeTimeout = 1.seconds,
        )
}
