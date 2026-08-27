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

internal fun interface ConnectionStateReader {
    /** Returns null when this platform cannot expose TCP state safely. */
    fun establishedConnections(): Set<ClientConnection>?
}

/**
 * Observes accepted client sockets with one bounded, fixed-rate monitor. JDK HttpServer exposes no
 * disconnect callback before the first response write, while the Linux engine runtime exposes the
 * same connection state through procfs. Unsupported or unreadable state fails open: the normal host
 * deadline remains authoritative and no disconnect is invented.
 */
internal class ClientConnectionObserver(
    private val capacity: Int = MAX_CONNECTIONS,
    private val pollInterval: Duration = DEFAULT_POLL_INTERVAL,
    private val stateReader: ConnectionStateReader = ProcConnectionStateReader,
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
                nextId++.also { observations[it] = Observation(connection, onDisconnect) }
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
        val established = stateReader.establishedConnections() ?: return
        val callbacks =
            lock.withLock {
                val disconnected = observations.filterValues { it.connection !in established }
                disconnected.keys.forEach(observations::remove)
                disconnected.values.map(Observation::onDisconnect)
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
    )

    private companion object {
        const val MAX_CONNECTIONS = 136
        val DEFAULT_POLL_INTERVAL: Duration = Duration.ofMillis(50)
        const val CONNECTION_THREAD_PREFIX = "engine-connection-"
        const val TERMINATION_SECONDS = 5L
    }
}

private object ProcConnectionStateReader : ConnectionStateReader {
    private val tables = listOf(Path.of("/proc/self/net/tcp"), Path.of("/proc/self/net/tcp6"))

    override fun establishedConnections(): Set<ClientConnection>? {
        val connections = mutableSetOf<ClientConnection>()
        var readable = false
        tables.forEach { table ->
            if (!Files.isReadable(table)) return@forEach
            val read = runCatching { readTable(table, connections) }.isSuccess
            readable = readable || read
        }
        return if (readable) connections else null
    }

    private fun readTable(
        table: Path,
        connections: MutableSet<ClientConnection>,
    ) {
        Files.newBufferedReader(table).useLines { lines ->
            lines.drop(1).forEach { line -> parseEstablished(line)?.let(connections::add) }
        }
    }

    private fun parseEstablished(line: String): ClientConnection? {
        val fields = line.trim().split(WHITESPACE)
        if (fields.size < 4 || fields[3] != ESTABLISHED_STATE) return null
        val local = parseEndpoint(fields[1]) ?: return null
        val remote = parseEndpoint(fields[2]) ?: return null
        return ClientConnection(local.first, local.second, remote.first, remote.second)
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

    private const val ESTABLISHED_STATE = "01"
    private const val IPV4_HEX_LENGTH = 8
    private const val IPV6_HEX_LENGTH = 32
    private val WHITESPACE = Regex("\\s+")
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
