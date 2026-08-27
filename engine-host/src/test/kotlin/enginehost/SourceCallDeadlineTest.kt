package enginehost

import eu.kanade.tachiyomi.source.online.HttpSource
import kotlinx.coroutines.awaitCancellation
import okhttp3.EventListener
import okhttp3.Headers
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import java.time.Duration
import java.time.Instant
import java.util.concurrent.CompletableFuture
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Future
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

private class DirectImageHttpSource(
    override val client: OkHttpClient,
) : HttpSource() {
    override val id: Long = 9L
    override val name: String = "Direct image test source"
    override val lang: String = "en"
    override val supportsLatest: Boolean = false
    override val baseUrl: String = "https://example.test"

    override fun headersBuilder(): Headers.Builder = Headers.Builder()
}

class SourceCallDeadlineTest {
    @Test
    fun `deadline cancels a cooperative coroutine without waiting for its callable`() {
        val timer = ManualDeadlineTimer()
        val deadline = SourceCallDeadline(Duration.ofSeconds(150), timer)
        val cancellation = SourceCallCancellation()
        val executor = java.util.concurrent.Executors.newSingleThreadExecutor()
        val publicResult = CompletableFuture<Unit>()
        val entered = CountDownLatch(1)
        val cancelled = CountDownLatch(1)
        val physical: Future<Unit> =
            executor.submit<Unit> {
                try {
                    cancellation.run {
                        entered.countDown()
                        awaitCancellation()
                    }
                } finally {
                    cancelled.countDown()
                }
            }
        try {
            deadline.supervise(physical, publicResult, cancellation::cancel)
            assertTrue(entered.await(5, TimeUnit.SECONDS), "coroutine did not start")

            timer.fireAll()

            assertTimeout(publicResult)
            assertTrue(cancelled.await(5, TimeUnit.SECONDS), "cooperative coroutine did not observe cancellation")
        } finally {
            cancellation.cancel()
            physical.cancel(true)
            executor.shutdownNow()
            deadline.close()
        }
    }

    @Test
    fun `deadline cancels the retained OkHttp call after the server receives it`() {
        MockWebServer().use { server ->
            server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.NO_RESPONSE))
            server.start()
            val cancelled = CountDownLatch(1)
            val client =
                OkHttpClient.Builder()
                    .eventListener(
                        object : EventListener() {
                            override fun canceled(call: okhttp3.Call) {
                                cancelled.countDown()
                            }
                        },
                    ).build()
            val timer = ManualDeadlineTimer()
            val deadline = SourceCallDeadline(Duration.ofSeconds(150), timer)
            val cancellation = SourceCallCancellation()
            val scheduler =
                SourceScheduler(
                    limits = SourceSchedulerLimits(workerCount = 1, perSourceLimit = 1, queueCapacity = 1),
                    sourceCallDeadline = deadline,
                )
            try {
                val result =
                    scheduler.accepted(1L, cancellation::cancel) {
                        SourceCalls.fetchViaGateway(
                            client = client,
                            gatewayUrl = server.url("/").toString(),
                            socks = null,
                            upstream = Request.Builder().url("https://images.example.test/page.jpg").build(),
                            cancellation = cancellation,
                        )
                    }
                assertTrue(server.takeRequest(5, TimeUnit.SECONDS) != null, "gateway request did not reach MockWebServer")

                timer.fireAll()

                assertTimeout(result)
                assertTrue(cancelled.await(5, TimeUnit.SECONDS), "OkHttp Call.cancel was not observed")
            } finally {
                scheduler.close()
                deadline.close()
            }
        }
    }

    @Test
    fun `direct image deadline retains the OkHttp call through a stalled response body`() {
        val server = MockWebServer()
        val responseHeadersReceived = CountDownLatch(1)
        val cancelled = CountDownLatch(1)
        val client =
            OkHttpClient.Builder()
                .eventListener(
                    object : EventListener() {
                        override fun responseHeadersEnd(
                            call: okhttp3.Call,
                            response: okhttp3.Response,
                        ) {
                            responseHeadersReceived.countDown()
                        }

                        override fun canceled(call: okhttp3.Call) {
                            cancelled.countDown()
                        }
                    },
                ).build()
        val source = DirectImageHttpSource(client)
        val cancellation = SourceCallCancellation()
        val timer = ManualDeadlineTimer()
        val deadline = SourceCallDeadline(Duration.ofSeconds(150), timer)
        val scheduler =
            SourceScheduler(
                limits = SourceSchedulerLimits(workerCount = 1, perSourceLimit = 1, queueCapacity = 1),
                sourceCallDeadline = deadline,
            )
        server.enqueue(
            MockResponse()
                .setHeader("Content-Type", "image/jpeg")
                .setBody("image bytes delayed after headers")
                .setBodyDelay(1, TimeUnit.DAYS),
        )
        server.start()
        val result =
            assertIs<Submission.Accepted<Pair<ByteArray, String>>>(
                scheduler.submit(source.id, cancellation::cancel) {
                    SourceCalls.image(source, pageUrl = "", imageUrl = server.url("/page.jpg").toString(), cancellation)
                },
            ).future
        try {
            assertNotNull(server.takeRequest(5, TimeUnit.SECONDS), "direct image request did not reach MockWebServer")
            assertTrue(responseHeadersReceived.await(5, TimeUnit.SECONDS), "response headers were not received")
            assertFalse(result.isDone, "body read must still be stalled after response headers")

            timer.fireAll()

            assertTimeout(result)
            assertTrue(cancelled.await(5, TimeUnit.SECONDS), "post-headers cancellation did not reach OkHttp")
        } finally {
            cancellation.cancel()
            result.cancel(true)
            scheduler.close()
            deadline.close()
            server.shutdown()
        }
    }

    @Test
    fun `queued time does not schedule or consume the execution deadline`() {
        val timer = ManualDeadlineTimer()
        val deadline = SourceCallDeadline(Duration.ofSeconds(150), timer)
        SourceScheduler(
            limits = SourceSchedulerLimits(workerCount = 1, perSourceLimit = 1, queueCapacity = 2),
            sourceCallDeadline = deadline,
        ).use { scheduler ->
            val releaseFirst = CountDownLatch(1)
            val firstEntered = CountDownLatch(1)
            val first = scheduler.accepted(1L) { blockIgnoringInterrupt(firstEntered, releaseFirst) }
            assertTrue(firstEntered.await(5, TimeUnit.SECONDS))
            val secondEntered = CountDownLatch(1)
            val releaseSecond = CountDownLatch(1)
            val second = scheduler.accepted(1L) { blockIgnoringInterrupt(secondEntered, releaseSecond) }

            assertEquals(1, timer.pendingCount, "only the physically running call may own a timer")
            timer.fireAll()
            assertFalse(second.isDone, "queued work consumed a deadline before physical execution")

            releaseFirst.countDown()
            assertTrue(secondEntered.await(5, TimeUnit.SECONDS), "queued call did not start after capacity returned")
            assertEquals(1, timer.pendingCount, "the second deadline must begin at physical start")
            timer.fireAll()
            assertTimeout(second)
            releaseSecond.countDown()
            runCatching { first.get(5, TimeUnit.SECONDS) }
        }
        deadline.close()
    }

    @Test
    fun `non cooperative timeout returns publicly while retaining the physical source cap`() {
        val timer = ManualDeadlineTimer()
        val deadline = SourceCallDeadline(Duration.ofSeconds(150), timer)
        SourceScheduler(
            limits = SourceSchedulerLimits(workerCount = 3, perSourceLimit = 2, queueCapacity = 4),
            sourceCallDeadline = deadline,
        ).use { scheduler ->
            val releaseA = CountDownLatch(1)
            val enteredA = CountDownLatch(2)
            val a1 = scheduler.accepted(1L) { blockIgnoringInterrupt(enteredA, releaseA) }
            val a2 = scheduler.accepted(1L) { blockIgnoringInterrupt(enteredA, releaseA) }
            assertTrue(enteredA.await(5, TimeUnit.SECONDS))
            val a3Entered = AtomicBoolean(false)
            val a3 = scheduler.accepted(1L) { a3Entered.set(true) }

            timer.fireAll()

            assertTimeout(a1)
            assertTimeout(a2)
            val bEntered = CountDownLatch(1)
            val b = scheduler.accepted(2L) { bEntered.countDown() }
            assertTrue(bEntered.await(5, TimeUnit.SECONDS), "healthy source did not use remaining physical capacity")
            val snapshot = scheduler.snapshot(Instant.now())
            assertEquals(2, snapshot.sources.single { it.sourceId == 1L }.running)
            assertEquals(1, snapshot.sources.single { it.sourceId == 1L }.queued)
            assertEquals(2, snapshot.timedOut)
            assertFalse(a3Entered.get(), "timed-out non-cooperative calls released the per-source cap")

            releaseA.countDown()
            a3.get(5, TimeUnit.SECONDS)
            b.get(5, TimeUnit.SECONDS)
        }
        deadline.close()
    }

    private fun <T> assertTimeout(result: CompletableFuture<T>) {
        val failure = assertFailsWith<java.util.concurrent.ExecutionException> { result.get(5, TimeUnit.SECONDS) }
        assertIs<TimeoutException>(failure.cause)
        assertEquals("source call timed out", failure.cause?.message)
    }

    private fun SourceScheduler.accepted(
        sourceId: Long,
        cancelUnderlying: () -> Unit = {},
        work: () -> Unit,
    ): CompletableFuture<Unit> =
        assertIs<Submission.Accepted<Unit>>(submit(sourceId, cancelUnderlying, work)).future

    private fun blockIgnoringInterrupt(
        entered: CountDownLatch,
        release: CountDownLatch,
    ) {
        entered.countDown()
        while (release.count > 0) {
            try {
                release.await()
            } catch (_: InterruptedException) {
                // Models source code that ignores advisory interruption.
            }
        }
    }
}

internal class ManualDeadlineTimer : DeadlineTimer {
    private val tasks = ConcurrentLinkedQueue<ManualTimerTask>()

    val pendingCount: Int
        get() = tasks.count { !it.cancelled.get() }

    override fun schedule(
        delay: Duration,
        task: () -> Unit,
    ): DeadlineTimerHandle {
        assertEquals(Duration.ofSeconds(150), delay)
        val scheduled = ManualTimerTask(task)
        tasks += scheduled
        return DeadlineTimerHandle { scheduled.cancelled.set(true) }
    }

    fun fireAll() {
        val snapshot = tasks.toList()
        tasks.removeAll(snapshot.toSet())
        snapshot.forEach { scheduled ->
            if (!scheduled.cancelled.compareAndSet(false, true)) return@forEach
            scheduled.task()
        }
    }

    private data class ManualTimerTask(
        val task: () -> Unit,
        val cancelled: AtomicBoolean = AtomicBoolean(false),
    )
}
