package enginehost

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import java.lang.reflect.Modifier
import kotlin.coroutines.Continuation
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue
import kotlin.time.Duration.Companion.seconds

class PinnedKcefProcessTest {
    @Test
    fun `pinned physical initializer contract remains directly invokable in caller coroutine`() {
        val method = pinnedCefInitializerMethod()

        assertEquals("initAsync", method.name)
        assertTrue(Modifier.isPrivate(method.modifiers))
        assertEquals(listOf(Continuation::class.java), method.parameterTypes.toList())
        assertEquals(Any::class.java, method.returnType)
    }

    @Test
    fun `process initialization enables KCEF then runs pinned physical initializer before readiness validation`() =
        runBlocking {
            val events = mutableListOf<String>()
            val process =
                PinnedKcefProcess(
                    enable = { events += "enable" },
                    initializePinned = { events += "initialize" },
                    ready = {
                        events += "ready"
                        true
                    },
                    dispose = { events += "dispose" },
                )

            process.initialize()
            process.close()

            assertEquals(listOf("enable", "initialize", "ready", "dispose"), events)
        }

    @Test
    fun `process initialization rejects pinned completion without a ready app`() =
        runBlocking {
            val process =
                PinnedKcefProcess(
                    enable = {},
                    initializePinned = {},
                    ready = { false },
                    dispose = {},
                )

            assertFailsWith<IllegalStateException> { process.initialize() }
            Unit
        }

    @Test
    fun `Main lifecycle wiring owns process initialization monitoring and cleanup`() =
        runBlocking {
            val initializeEntered = CompletableDeferred<Unit>()
            val release = CompletableDeferred<Unit>()
            var cleanupCalls = 0
            val process =
                object : KcefProcess {
                    override suspend fun initialize() {
                        initializeEntered.complete(Unit)
                        release.await()
                    }

                    override fun isReady(): Boolean = true

                    override fun close() {
                        cleanupCalls++
                    }
                }
            val lifecycle = createKcefLifecycle(process)
            try {
                lifecycle.start(enabled = true)
                initializeEntered.await()
                release.complete(Unit)
                lifecycle.awaitReady(1.seconds)

                assertEquals(KcefState.READY, lifecycle.snapshot().state)
            } finally {
                lifecycle.close()
            }
            withTimeout(1.seconds) {
                while (cleanupCalls < 1) delay(5)
            }
            assertEquals(1, cleanupCalls)
        }
}
