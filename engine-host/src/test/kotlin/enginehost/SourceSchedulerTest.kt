package enginehost

import java.time.Clock
import java.time.Instant
import java.time.ZoneId
import java.time.ZoneOffset
import java.util.concurrent.CompletableFuture
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertTrue

class SourceSchedulerTest {
    @Test
    fun `one source is admitted in FIFO order`() {
        SourceScheduler().use { scheduler ->
            val blockers = occupyAllWorkers(scheduler)
            val order = ConcurrentLinkedQueue<String>()
            val futures =
                listOf("A1", "A2", "A3").map { label ->
                    scheduler.accepted(1L) {
                        order += label
                    }
                }

            blockers.first().release.countDown()
            futures.forEach { it.get(5, TimeUnit.SECONDS) }

            assertEquals(listOf("A1", "A2", "A3"), order.toList())
            blockers.drop(1).forEach { it.release.countDown() }
        }
    }

    @Test
    fun `one source never occupies more than two physical workers`() {
        SourceScheduler().use { scheduler ->
            val releaseA = CountDownLatch(1)
            val enteredA = CountDownLatch(2)
            val thirdEntered = AtomicBoolean(false)
            val first = scheduler.accepted(1L) { block(enteredA, releaseA) }
            val second = scheduler.accepted(1L) { block(enteredA, releaseA) }
            assertTrue(enteredA.await(5, TimeUnit.SECONDS), "two A calls did not start")
            val third = scheduler.accepted(1L) { thirdEntered.set(true) }

            val enteredB = CountDownLatch(1)
            val healthy = scheduler.accepted(2L) { enteredB.countDown() }

            assertTrue(enteredB.await(5, TimeUnit.SECONDS), "healthy source B did not use idle global capacity")
            assertFalse(thirdEntered.get(), "A3 started while two physical A calls were still running")
            assertEquals(2, scheduler.snapshot(Instant.now()).source(1L).running)

            releaseA.countDown()
            listOf(first, second, third, healthy).forEach { it.get(5, TimeUnit.SECONDS) }
        }
    }

    @Test
    fun `runnable sources are admitted A1 B1 A2 B2`() {
        SourceScheduler().use { scheduler ->
            val blockers = occupyAllWorkers(scheduler)
            val order = ConcurrentLinkedQueue<String>()
            val futures =
                listOf(1L to "A1", 1L to "A2", 2L to "B1", 2L to "B2").map { (sourceId, label) ->
                    scheduler.accepted(sourceId) { order += label }
                }

            blockers.first().release.countDown()
            futures.forEach { it.get(5, TimeUnit.SECONDS) }

            assertEquals(listOf("A1", "B1", "A2", "B2"), order.toList())
            blockers.drop(1).forEach { it.release.countDown() }
        }
    }

    @Test
    fun `a refilled source cannot starve an already runnable source`() {
        SourceScheduler().use { scheduler ->
            val blockers = occupyAllWorkers(scheduler)
            val a1Entered = CountDownLatch(1)
            val releaseA1 = CountDownLatch(1)
            val order = ConcurrentLinkedQueue<String>()
            val a1 =
                scheduler.accepted(1L) {
                    order += "A1"
                    block(a1Entered, releaseA1)
                }
            val b1 = scheduler.accepted(2L) { order += "B1" }
            val a2 = scheduler.accepted(1L) { order += "A2" }

            blockers.first().release.countDown()
            assertTrue(a1Entered.await(5, TimeUnit.SECONDS), "A1 did not consume the refilled slot")
            val a3 = scheduler.accepted(1L) { order += "A3" }
            releaseA1.countDown()
            listOf(a1, b1, a2, a3).forEach { it.get(5, TimeUnit.SECONDS) }

            assertEquals(listOf("A1", "B1", "A2", "A3"), order.toList())
            blockers.drop(1).forEach { it.release.countDown() }
        }
    }

    @Test
    fun `aggregate waiting queue rejects the one hundred twenty ninth call without invoking it`() {
        SourceScheduler().use { scheduler ->
            val blockers = occupyAllWorkers(scheduler)
            val queued = List(128) { scheduler.submit(99L) {} }
            val rejectedInvoked = AtomicBoolean(false)

            val rejected = scheduler.submit(100L) { rejectedInvoked.set(true) }

            assertTrue(queued.all { it is Submission.Accepted<*> })
            assertIs<Submission.Rejected>(rejected)
            assertEquals(128, scheduler.snapshot(Instant.now()).queued)
            assertFalse(rejectedInvoked.get())
            blockers.forEach { it.release.countDown() }
        }
    }

    @Test
    fun `cancelling before admission removes the call without invoking it`() {
        SourceScheduler().use { scheduler ->
            val blockers = occupyAllWorkers(scheduler)
            val invoked = AtomicBoolean(false)
            val queued = scheduler.accepted(99L) { invoked.set(true) }

            assertTrue(queued.cancel(false))
            eventually { scheduler.snapshot(Instant.now()).queued == 0 }
            blockers.forEach { it.release.countDown() }
            eventually { scheduler.snapshot(Instant.now()).running == 0 }

            assertFalse(invoked.get())
            assertEquals(1, scheduler.snapshot(Instant.now()).cancelled)
        }
    }

    @Test
    fun `timed public results retain physical occupancy until non cooperative callables return`() {
        SourceScheduler().use { scheduler ->
            val releaseA = CountDownLatch(1)
            val enteredA = CountDownLatch(2)
            val a1 = scheduler.accepted(1L) { blockIgnoringInterrupt(enteredA, releaseA) }
            val a2 = scheduler.accepted(1L) { blockIgnoringInterrupt(enteredA, releaseA) }
            assertTrue(enteredA.await(5, TimeUnit.SECONDS), "two physical A calls did not start")
            val a3Entered = AtomicBoolean(false)
            val a3 = scheduler.accepted(1L) { a3Entered.set(true) }

            assertTrue(a1.completeExceptionally(TimeoutException("test deadline")))
            assertTrue(a2.completeExceptionally(TimeoutException("test deadline")))

            val bEntered = CountDownLatch(1)
            val b = scheduler.accepted(2L) { bEntered.countDown() }
            assertTrue(bEntered.await(5, TimeUnit.SECONDS), "healthy B did not start after A timed out publicly")
            val timedSnapshot = scheduler.snapshot(Instant.now())
            assertEquals(2, timedSnapshot.source(1L).running)
            assertEquals(1, timedSnapshot.source(1L).queued)
            assertEquals(2, timedSnapshot.timedOut)
            assertFalse(a3Entered.get(), "A3 bypassed the physical per-source cap")

            releaseA.countDown()
            a3.get(5, TimeUnit.SECONDS)
            b.get(5, TimeUnit.SECONDS)
        }
    }

    @Test
    fun `snapshot reports the oldest physical running age from the injected clock`() {
        val clock = MutableClock(Instant.parse("2026-08-27T10:00:00Z"))
        SourceScheduler(clock = clock).use { scheduler ->
            val release = CountDownLatch(1)
            val firstEntered = CountDownLatch(1)
            scheduler.accepted(1L) { block(firstEntered, release) }
            assertTrue(firstEntered.await(5, TimeUnit.SECONDS))
            clock.current = clock.current.plusSeconds(5)
            val secondEntered = CountDownLatch(1)
            scheduler.accepted(2L) { block(secondEntered, release) }
            assertTrue(secondEntered.await(5, TimeUnit.SECONDS))

            val snapshot = scheduler.snapshot(clock.current.plusSeconds(5))

            assertEquals(10_000, snapshot.oldestRunningMillis)
            release.countDown()
        }
    }

    private fun occupyAllWorkers(scheduler: SourceScheduler): List<Blocker> {
        val blockers = List(8) { Blocker(CountDownLatch(1), CountDownLatch(1)) }
        blockers.forEachIndexed { index, blocker ->
            scheduler.accepted(10L + index / 2) { block(blocker.entered, blocker.release) }
        }
        blockers.forEach { blocker ->
            assertTrue(blocker.entered.await(5, TimeUnit.SECONDS), "source worker did not become occupied")
        }
        return blockers
    }

    private fun SourceScheduler.accepted(
        sourceId: Long,
        work: () -> Unit,
    ): CompletableFuture<Unit> = assertIs<Submission.Accepted<Unit>>(submit(sourceId, work)).future

    private fun SourceSchedulerSnapshot.source(sourceId: Long): SourceSchedulerSourceSnapshot =
        sources.single { it.sourceId == sourceId }

    private fun block(
        entered: CountDownLatch,
        release: CountDownLatch,
    ) {
        entered.countDown()
        try {
            release.await()
        } catch (_: InterruptedException) {
            Thread.currentThread().interrupt()
        }
    }

    private fun blockIgnoringInterrupt(
        entered: CountDownLatch,
        release: CountDownLatch,
    ) {
        entered.countDown()
        while (release.count > 0) {
            try {
                release.await()
            } catch (_: InterruptedException) {
                // Models extension code that ignores advisory interruption.
            }
        }
    }

    private fun eventually(condition: () -> Boolean) {
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        while (!condition() && System.nanoTime() < deadline) Thread.sleep(5)
        assertTrue(condition(), "condition did not become true")
    }

    private data class Blocker(
        val entered: CountDownLatch,
        val release: CountDownLatch,
    )

    private class MutableClock(
        var current: Instant,
    ) : Clock() {
        override fun getZone(): ZoneId = ZoneOffset.UTC

        override fun withZone(zone: ZoneId): Clock = this

        override fun instant(): Instant = current
    }
}
