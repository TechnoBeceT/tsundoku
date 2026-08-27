package enginehost

import java.net.InetAddress
import java.net.InetSocketAddress
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.Executors
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock

internal data class ClientConnection(
    val localAddress: String,
    val localPort: Int,
    val remoteAddress: String,
    val remotePort: Int,
)

internal enum class ResponsePathDisposition {
    LIVE,
    UNUSABLE,
    AMBIGUOUS,
}

internal enum class TcpConnectionState(
    val code: String,
    val responsePath: ResponsePathDisposition,
) {
    ESTABLISHED("01", ResponsePathDisposition.LIVE),
    SYN_SENT("02", ResponsePathDisposition.AMBIGUOUS),
    SYN_RECV("03", ResponsePathDisposition.AMBIGUOUS),
    FIN_WAIT_1("04", ResponsePathDisposition.UNUSABLE),
    FIN_WAIT_2("05", ResponsePathDisposition.UNUSABLE),
    TIME_WAIT("06", ResponsePathDisposition.AMBIGUOUS),
    CLOSE("07", ResponsePathDisposition.AMBIGUOUS),
    // A peer FIN closes only the request-writing half. The client may still read the response.
    CLOSE_WAIT("08", ResponsePathDisposition.AMBIGUOUS),
    LAST_ACK("09", ResponsePathDisposition.UNUSABLE),
    LISTEN("0A", ResponsePathDisposition.AMBIGUOUS),
    CLOSING("0B", ResponsePathDisposition.UNUSABLE),
    NEW_SYN_RECV("0C", ResponsePathDisposition.AMBIGUOUS),
    UNKNOWN("", ResponsePathDisposition.AMBIGUOUS),
    ;

    companion object {
        fun fromCode(code: String): TcpConnectionState = entries.firstOrNull { it.code == code } ?: UNKNOWN
    }
}

internal fun responsePathDisposition(states: Set<TcpConnectionState>): ResponsePathDisposition =
    if (states.size == 1) states.single().responsePath else ResponsePathDisposition.AMBIGUOUS

internal fun interface ConnectionStateReader {
    /** Returns null when a complete IPv4 and IPv6 TCP snapshot is unavailable. */
    fun connectionStates(): Map<ClientConnection, Set<TcpConnectionState>>?
}

/**
 * Observes accepted client sockets with one bounded, fixed-delay monitor. JDK HttpServer exposes no
 * disconnect callback before the first response write, while the Linux engine runtime exposes the
 * same connection state through procfs. Unsupported or unreadable state, peer FIN, tuple reuse, and
 * absence before a positive observation fail open: the normal host deadline remains authoritative
 * and no disconnect is invented.
 */
internal class ClientConnectionObserver(
    private val capacity: Int = MAX_CONNECTIONS,
    private val pollInterval: Duration = DEFAULT_POLL_INTERVAL,
    private val stateReader: ConnectionStateReader = ProcConnectionStateReader(),
) : AutoCloseable {
    private val lock = ReentrantLock()
    private val observations = mutableMapOf<Long, Observation>()
    private val closed = AtomicBoolean(false)
    private val monitor: ScheduledExecutorService =
        Executors.newSingleThreadScheduledExecutor(RpcThreadFactory(CONNECTION_THREAD_PREFIX))
    private var nextId = 1L

    init {
        require(capacity > 0) { "capacity must be positive" }
        require(!pollInterval.isNegative && !pollInterval.isZero) { "pollInterval must be positive" }
        monitor.scheduleWithFixedDelay(
            ::scan,
            pollInterval.toNanos(),
            pollInterval.toNanos(),
            TimeUnit.NANOSECONDS,
        )
    }

    internal val isTerminated: Boolean
        get() = monitor.isTerminated

    fun observe(
        local: InetSocketAddress,
        remote: InetSocketAddress,
        onDisconnect: () -> Unit,
    ): AutoCloseable? {
        val connection =
            ClientConnection(
                localAddress = canonicalAddress(local.address),
                localPort = local.port,
                remoteAddress = canonicalAddress(remote.address),
                remotePort = remote.port,
            )
        val id =
            lock.withLock {
                if (closed.get() || observations.size >= capacity) return null
                nextId++.also {
                    observations[it] =
                        Observation(
                            connection = connection,
                            onDisconnect = onDisconnect,
                        )
                }
            }
        return Registration(id)
    }

    override fun close() {
        if (!closed.compareAndSet(false, true)) return
        lock.withLock { observations.clear() }
        monitor.shutdownNow()
        try {
            monitor.awaitTermination(TERMINATION_SECONDS, TimeUnit.SECONDS)
        } catch (_: InterruptedException) {
            Thread.currentThread().interrupt()
        }
    }

    private fun scan() {
        val states = stateReader.connectionStates()
        if (states == null) {
            lock.withLock { observations.values.forEach(Observation::disableInference) }
            return
        }
        val callbacks =
            lock.withLock {
                buildList {
                    val iterator = observations.iterator()
                    while (iterator.hasNext()) {
                        val observation = iterator.next().value
                        val disposition = states[observation.connection]?.let(::responsePathDisposition)
                        val disconnected = observation.observe(disposition)
                        if (disconnected) {
                            iterator.remove()
                            add(observation.onDisconnect)
                        }
                    }
                }
            }
        callbacks.forEach { callback -> runCatching(callback) }
    }

    private inner class Registration(
        private val id: Long,
    ) : AutoCloseable {
        private val registrationClosed = AtomicBoolean(false)

        override fun close() {
            if (!registrationClosed.compareAndSet(false, true)) return
            lock.withLock { observations.remove(id) }
        }
    }

    private data class Observation(
        val connection: ClientConnection,
        val onDisconnect: () -> Unit,
        var seenPresent: Boolean = false,
        var missingSnapshots: Int = 0,
        var failOpen: Boolean = false,
    ) {
        fun observe(disposition: ResponsePathDisposition?): Boolean {
            if (failOpen) return false
            when (disposition) {
                ResponsePathDisposition.LIVE -> {
                    seenPresent = true
                    missingSnapshots = 0
                }
                ResponsePathDisposition.UNUSABLE -> return true
                ResponsePathDisposition.AMBIGUOUS -> {
                    seenPresent = true
                    missingSnapshots = 0
                    failOpen = true
                }
                null -> {
                    // A tuple that was never positively observed may have disappeared before
                    // registration or may belong to a later connection after port reuse.
                    if (!seenPresent) failOpen = true else missingSnapshots++
                }
            }
            return !failOpen && missingSnapshots >= MISSING_SNAPSHOT_CONFIRMATIONS
        }

        fun disableInference() {
            failOpen = true
            missingSnapshots = 0
        }
    }

    private companion object {
        const val MAX_CONNECTIONS = 136
        val DEFAULT_POLL_INTERVAL: Duration = Duration.ofMillis(50)
        const val CONNECTION_THREAD_PREFIX = "engine-connection-"
        const val TERMINATION_SECONDS = 5L
        const val MISSING_SNAPSHOT_CONFIRMATIONS = 3
    }
}

internal class ProcConnectionStateReader(
    private val tables: List<Path> = DEFAULT_TABLES,
) : ConnectionStateReader {
    override fun connectionStates(): Map<ClientConnection, Set<TcpConnectionState>>? {
        if (tables.any { !Files.isReadable(it) }) return null
        return runCatching {
            val connections = mutableMapOf<ClientConnection, MutableSet<TcpConnectionState>>()
            tables.forEach { table -> readTable(table, connections) }
            connections.mapValues { (_, states) -> states.toSet() }
        }.getOrNull()
    }

    private fun readTable(
        table: Path,
        connections: MutableMap<ClientConnection, MutableSet<TcpConnectionState>>,
    ) {
        Files.newBufferedReader(table).useLines { lines ->
            lines.drop(1).filter(String::isNotBlank).forEach { line ->
                val (connection, state) = parseConnection(line)
                connections.getOrPut(connection, ::mutableSetOf).add(state)
            }
        }
    }

    private fun parseConnection(line: String): Pair<ClientConnection, TcpConnectionState> {
        val fields = line.trim().split(WHITESPACE)
        require(fields.size >= 4) { "malformed procfs TCP row" }
        val local = requireNotNull(parseEndpoint(fields[1])) { "malformed procfs local endpoint" }
        val remote = requireNotNull(parseEndpoint(fields[2])) { "malformed procfs remote endpoint" }
        val connection = ClientConnection(local.first, local.second, remote.first, remote.second)
        return connection to TcpConnectionState.fromCode(fields[3])
    }

    private fun parseEndpoint(value: String): Pair<String, Int>? {
        val separator = value.lastIndexOf(':')
        if (separator <= 0 || separator == value.lastIndex) return null
        val address = decodeProcAddress(value.substring(0, separator)) ?: return null
        val port = value.substring(separator + 1).toIntOrNull(16) ?: return null
        return address to port
    }

    private fun decodeProcAddress(value: String): String? {
        if (value.length != IPV4_HEX_LENGTH && value.length != IPV6_HEX_LENGTH) return null
        val stored =
            runCatching {
                value.chunked(2).map { byte -> byte.toInt(16).toByte() }.toByteArray()
            }.getOrNull() ?: return null
        val networkOrder = ByteArray(stored.size)
        stored.indices.step(4).forEach { offset ->
            repeat(4) { index -> networkOrder[offset + index] = stored[offset + 3 - index] }
        }
        return canonicalAddress(networkOrder)
    }

    private companion object {
        const val IPV4_HEX_LENGTH = 8
        const val IPV6_HEX_LENGTH = 32
        val WHITESPACE = Regex("\\s+")
        val DEFAULT_TABLES = listOf(Path.of("/proc/self/net/tcp"), Path.of("/proc/self/net/tcp6"))
    }
}

private fun canonicalAddress(address: InetAddress): String = canonicalAddress(address.address)

private fun canonicalAddress(address: ByteArray): String {
    val normalized =
        if (
            address.size == 16 &&
            address.take(10).all { it == 0.toByte() } &&
            address[10] == 0xff.toByte() &&
            address[11] == 0xff.toByte()
        ) {
            address.copyOfRange(12, 16)
        } else {
            address
        }
    return normalized.joinToString("") { byte -> "%02x".format(byte.toInt() and 0xff) }
}
