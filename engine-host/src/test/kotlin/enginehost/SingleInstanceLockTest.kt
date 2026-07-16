package enginehost

/*
 * Proves acquireSingleInstanceLock degrades gracefully instead of crashing engine-host boot when
 * the underlying filesystem cannot provide OS-level file locks (GAP-087). The owner's prod data dir
 * is on NFS, where a missing lock daemon (rpc.lockd/statd) makes FileChannel.tryLock() — or even
 * opening the lock file's channel — throw java.io.IOException ("No locks available", ENOLCK) instead
 * of returning null, which the old single-catch (OverlappingFileLockException only) let propagate out
 * of bootstrapAndroidCompat and crash boot. A real NFS mount is impractical here, so the two degrade
 * paths are forced at their exact throw points: a second same-JVM acquire (OverlappingFileLockException)
 * and a lock path that cannot be opened as a file (IOException on open, standing in for the
 * lock-unsupported mount). The guard is advisory best-effort under the single-owner threat model, so
 * both must return normally rather than propagate the exception to the caller.
 */

import org.junit.jupiter.api.Assertions.assertDoesNotThrow
import java.io.File
import java.nio.file.Files
import kotlin.test.Test

class SingleInstanceLockTest {
    @Test
    fun `a redundant acquire in the same JVM degrades instead of crashing`() {
        val dataRoot = Files.createTempDirectory("enginehost-lock").toFile()
        try {
            // First acquire holds the lock; the redundant second acquire from the same JVM hits
            // OverlappingFileLockException and must degrade (log + return), never throw.
            acquireSingleInstanceLock(dataRoot)
            assertDoesNotThrow { acquireSingleInstanceLock(dataRoot) }
        } finally {
            dataRoot.deleteRecursively()
        }
    }

    @Test
    fun `degrades gracefully when the lock file cannot be opened`() {
        val dataRoot = Files.createTempDirectory("enginehost-nolock").toFile()
        try {
            // Occupy the .enginehost.lock path with a DIRECTORY so RandomAccessFile(.., "rw") throws
            // IOException — the same failure class a lock-unsupported NFS mount produces (GAP-087).
            File(dataRoot, ".enginehost.lock").mkdirs()
            assertDoesNotThrow { acquireSingleInstanceLock(dataRoot) }
        } finally {
            dataRoot.deleteRecursively()
        }
    }
}
