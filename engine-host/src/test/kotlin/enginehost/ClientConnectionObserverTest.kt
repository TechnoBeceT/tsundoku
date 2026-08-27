package enginehost

import java.net.InetSocketAddress
import java.nio.file.Files
import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class ClientConnectionObserverTest {
    @Test
    fun `tcp state table cancels only proven response path failures`() {
        val cases =
            mapOf(
                TcpConnectionState.ESTABLISHED to ResponsePathDisposition.LIVE,
                TcpConnectionState.CLOSE_WAIT to ResponsePathDisposition.PEER_FIN,
                TcpConnectionState.FIN_WAIT_1 to ResponsePathDisposition.UNUSABLE,
                TcpConnectionState.FIN_WAIT_2 to ResponsePathDisposition.UNUSABLE,
                TcpConnectionState.LAST_ACK to ResponsePathDisposition.UNUSABLE,
                TcpConnectionState.CLOSING to ResponsePathDisposition.UNUSABLE,
                TcpConnectionState.SYN_SENT to ResponsePathDisposition.AMBIGUOUS,
                TcpConnectionState.SYN_RECV to ResponsePathDisposition.AMBIGUOUS,
                TcpConnectionState.TIME_WAIT to ResponsePathDisposition.AMBIGUOUS,
                TcpConnectionState.CLOSE to ResponsePathDisposition.AMBIGUOUS,
                TcpConnectionState.LISTEN to ResponsePathDisposition.AMBIGUOUS,
                TcpConnectionState.NEW_SYN_RECV to ResponsePathDisposition.AMBIGUOUS,
                TcpConnectionState.UNKNOWN to ResponsePathDisposition.AMBIGUOUS,
            )

        cases.forEach { (state, expected) ->
            assertEquals(expected, responsePathDisposition(setOf(state)), state.name)
        }
        assertEquals(
            ResponsePathDisposition.AMBIGUOUS,
            responsePathDisposition(setOf(TcpConnectionState.ESTABLISHED, TcpConnectionState.TIME_WAIT)),
            "a reused tuple with multiple states must fail open",
        )
        assertEquals(TcpConnectionState.UNKNOWN, TcpConnectionState.fromCode("FF"))
    }

    @Test
    fun `proc reader preserves IPv4 and IPv6 peer FIN states`() {
        val root = Files.createTempDirectory("proc-connection-reader")
        val tcp = root.resolve("tcp")
        val tcp6 = root.resolve("tcp6")
        Files.writeString(
            tcp,
            """
            sl local_address rem_address st
            0: 0100007F:9859 0100007F:A029 01
            """.trimIndent(),
        )
        Files.writeString(
            tcp6,
            """
            sl local_address rem_address st
            1: 00000000000000000000000001000000:985A 00000000000000000000000001000000:A02A 08
            """.trimIndent(),
        )

        val states = assertNotNull(ProcConnectionStateReader(listOf(tcp, tcp6)).connectionStates())

        assertEquals(
            setOf(TcpConnectionState.ESTABLISHED),
            states[ClientConnection("7f000001", 39001, "7f000001", 41001)],
        )
        val loopbackV6 = "00000000000000000000000000000001"
        assertEquals(
            setOf(TcpConnectionState.CLOSE_WAIT),
            states[ClientConnection(loopbackV6, 39002, loopbackV6, 41002)],
        )
    }

    @Test
    fun `partial or malformed proc snapshot fails open`() {
        val root = Files.createTempDirectory("partial-proc-connection-reader")
        val tcp = root.resolve("tcp")
        val tcp6 = root.resolve("tcp6")
        Files.writeString(tcp, "sl local_address rem_address st\n")

        assertNull(ProcConnectionStateReader(listOf(tcp, tcp6)).connectionStates())

        Files.writeString(tcp6, "sl local_address rem_address st\nmalformed\n")
        assertNull(ProcConnectionStateReader(listOf(tcp, tcp6)).connectionStates())
    }

    @Test
    fun `announced peer FIN remains live while unannounced FIN releases capacity`() {
        val first = connection(40501)
        val second = connection(40502)
        val reader = ControllableConnectionStateReader(states(first, TcpConnectionState.CLOSE_WAIT))
        val unannouncedDisconnect = CountDownLatch(1)
        val announcedDisconnect = CountDownLatch(1)
        val observer =
            ClientConnectionObserver(
                capacity = 2,
                pollInterval = Duration.ofMillis(5),
                stateReader = reader,
            )
        try {
            assertNotNull(observer.observe(local(first), remote(first)) { unannouncedDisconnect.countDown() })
            val announced =
                observer.observe(local(first), remote(first), responseExpectedAfterPeerFin = true) {
                    announcedDisconnect.countDown()
                }
            assertNotNull(announced)
            assertNull(observer.observe(local(second), remote(second)) {})
            reader.start()

            assertTrue(unannouncedDisconnect.await(500, TimeUnit.MILLISECONDS), "unannounced FIN was not cancelled")
            assertEquals(false, announcedDisconnect.await(50, TimeUnit.MILLISECONDS))
            val replacement = observer.observe(local(second), remote(second)) {}
            assertNotNull(replacement, "cancelled registration did not release capacity")
            replacement.close()
            announced.close()
        } finally {
            observer.close()
        }
    }

    @Test
    fun `ambiguous terminal state fails open until explicit registration cleanup`() {
        val first = connection(40701)
        val second = connection(40702)
        val timeWait = states(first, TcpConnectionState.TIME_WAIT)
        val reader = ControllableConnectionStateReader(timeWait)
        val disconnected = CountDownLatch(1)
        val observer =
            ClientConnectionObserver(
                capacity = 1,
                pollInterval = Duration.ofMillis(5),
                stateReader = reader,
            )
        try {
            val registration = observer.observe(local(first), remote(first)) { disconnected.countDown() }
            assertNotNull(registration)
            reader.start()
            reader.awaitScan(timeWait)
            reader.current.set(emptyMap())
            reader.awaitScan(emptyMap())

            assertEquals(false, disconnected.await(50, TimeUnit.MILLISECONDS))
            assertNull(observer.observe(local(second), remote(second)) {})
            registration.close()
            assertNotNull(observer.observe(local(second), remote(second)) {})
        } finally {
            observer.close()
        }
    }

    @Test
    fun `one missing tuple snapshot does not invent a disconnect`() {
        val connection = connection(40001)
        val reader = ControllableConnectionStateReader(liveStates(connection))
        val disconnected = CountDownLatch(1)
        val observer =
            ClientConnectionObserver(
                capacity = 1,
                pollInterval = Duration.ofMillis(5),
                stateReader = reader,
            )
        try {
            assertNotNull(observer.observe(local(connection), remote(connection)) { disconnected.countDown() })
            reader.start()
            reader.awaitScan(liveStates(connection))

            reader.current.set(emptyMap())
            reader.awaitScan(emptyMap())
            reader.current.set(liveStates(connection))

            assertEquals(false, disconnected.await(50, TimeUnit.MILLISECONDS))
        } finally {
            observer.close()
        }
    }

    @Test
    fun `one bounded monitor detects disconnect and releases registration capacity`() {
        val first = connection(41001)
        val second = connection(41002)
        val active = AtomicReference(liveStates(first, second))
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

            active.set(liveStates(second))

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

    private fun liveStates(vararg connections: ClientConnection): Map<ClientConnection, Set<TcpConnectionState>> =
        connections.associateWith { setOf(TcpConnectionState.ESTABLISHED) }

    private fun states(
        connection: ClientConnection,
        state: TcpConnectionState,
    ): Map<ClientConnection, Set<TcpConnectionState>> = mapOf(connection to setOf(state))

    private class ControllableConnectionStateReader(
        initial: Map<ClientConnection, Set<TcpConnectionState>>,
    ) : ConnectionStateReader {
        val current = AtomicReference(initial)
        private val started = CountDownLatch(1)
        private val scans = LinkedBlockingQueue<Map<ClientConnection, Set<TcpConnectionState>>>()

        override fun connectionStates(): Map<ClientConnection, Set<TcpConnectionState>> {
            started.await()
            return current.get().also(scans::add)
        }

        fun start() = started.countDown()

        fun awaitScan(expected: Map<ClientConnection, Set<TcpConnectionState>>) {
            val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
            while (System.nanoTime() < deadline) {
                val observed = scans.poll(100, TimeUnit.MILLISECONDS) ?: continue
                if (observed == expected) return
            }
            error("connection reader did not scan $expected")
        }
    }
}
