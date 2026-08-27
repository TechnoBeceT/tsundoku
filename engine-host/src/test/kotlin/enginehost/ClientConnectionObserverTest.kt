package enginehost

import java.net.InetSocketAddress
import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class ClientConnectionObserverTest {
    @Test
    fun `one bounded monitor detects disconnect and releases registration capacity`() {
        val first = connection(41001)
        val second = connection(41002)
        val active = AtomicReference(setOf(first, second))
        val disconnected = CountDownLatch(1)
        val observer =
            ClientConnectionObserver(
                capacity = 1,
                pollInterval = Duration.ofMillis(5),
                stateReader = ConnectionStateReader { active.get() },
            )
        try {
            val firstRegistration =
                observer.observe(local(first), remote(first)) {
                    disconnected.countDown()
                }
            assertNotNull(firstRegistration)
            assertNull(observer.observe(local(second), remote(second)) {})

            active.set(setOf(second))

            assertTrue(disconnected.await(500, TimeUnit.MILLISECONDS), "observer did not report the removed connection")
            assertNotNull(observer.observe(local(second), remote(second)) {})
        } finally {
            observer.close()
        }
        assertTrue(observer.isTerminated)
    }

    @Test
    fun `unavailable connection state never invents a disconnect`() {
        val disconnected = CountDownLatch(1)
        val observer =
            ClientConnectionObserver(
                capacity = 1,
                pollInterval = Duration.ofMillis(5),
                stateReader = ConnectionStateReader { null },
            )
        try {
            val connection = connection(42001)
            val registration = observer.observe(local(connection), remote(connection)) { disconnected.countDown() }

            assertNotNull(registration)
            assertEquals(false, disconnected.await(50, TimeUnit.MILLISECONDS))
            registration.close()
        } finally {
            observer.close()
        }
    }

    private fun connection(remotePort: Int): ClientConnection =
        ClientConnection("7f000001", 39001, "7f000001", remotePort)

    private fun local(connection: ClientConnection): InetSocketAddress =
        InetSocketAddress("127.0.0.1", connection.localPort)

    private fun remote(connection: ClientConnection): InetSocketAddress =
        InetSocketAddress("127.0.0.1", connection.remotePort)
}
