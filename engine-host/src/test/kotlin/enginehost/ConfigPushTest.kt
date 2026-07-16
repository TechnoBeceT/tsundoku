package enginehost

/*
 * Proves ConfigPush.applySocks actually applies the JVM-global SOCKS System properties + the
 * Authenticator (GAP-084 / audit item B21) instead of only writing serverConfig's
 * MutableStateFlows into the void. Before this fix, `applySocks` was write-only: it updated
 * `serverConfig.socksProxy*` but nothing ever read those flows, so an owner enabling SOCKS from
 * Tsundoku's settings UI had zero effect on the actual OkHttp clients. A real SOCKS server is
 * impractical to spin up here, so this pins the regression at the System-property boundary
 * instead — the exact surface OkHttp's ambient SOCKS support reads.
 */

import suwayomi.tachidesk.server.ServerConfig
import suwayomi.tachidesk.server.serverConfig
import suwayomi.tachidesk.server.util.ConfigTypeRegistration
import xyz.nulldev.ts.config.GlobalConfigManager
import java.net.Authenticator
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

/**
 * Mirrors the registration step Main.kt's `bootstrapAndroidCompat` performs — the top-level
 * `serverConfig` property is a lazy singleton resolved through `GlobalConfigManager`'s module map,
 * which nothing else in a plain test JVM registers. A Kotlin `object`'s initializer runs at most
 * ONCE per JVM (lazily, on first access) — load-bearing here: `ServerConfig()`'s constructor
 * registers every setting name into the process-global `SettingsRegistry`
 * (`SettingsRegistry.register`), which throws `IllegalStateException` ("uses protoNumber N already
 * used by ...") if the SAME setting is registered twice. JUnit5 creates a FRESH `ConfigPushTest`
 * instance per `@Test` method, so this setup must NOT live in `ConfigPushTest`'s own `init` block —
 * that would re-run (and blow up) on the second test.
 */
private object ServerConfigTestSetup {
    init {
        ConfigTypeRegistration.registerCustomTypes()
        GlobalConfigManager.registerModule(ServerConfig.register { GlobalConfigManager.config })
    }

    /** No-op call site — merely referencing this object is enough to trigger its one-time [init]. */
    fun ensureRegistered() = Unit
}

class ConfigPushTest {
    init {
        ServerConfigTestSetup.ensureRegistered()
    }

    // Leave no SOCKS state behind for any other test/class sharing this JVM's System properties.
    @AfterTest
    fun clearSocksState() {
        ConfigPush.applySocks(SocksConfigRequest(enabled = false))
    }

    @Test
    fun `enabling socks sets the expected JVM system properties and an Authenticator`() {
        ConfigPush.applySocks(
            SocksConfigRequest(
                enabled = true,
                version = 5,
                host = "127.0.0.1",
                port = "1080",
                username = "owner",
                password = "secret",
            ),
        )

        assertEquals("127.0.0.1", System.getProperty("socksProxyHost"))
        assertEquals("1080", System.getProperty("socksProxyPort"))
        assertEquals("5", System.getProperty("socksProxyVersion"))
        assertEquals(
            true,
            Authenticator.getDefault() != null,
            "enabling SOCKS must install a default Authenticator for proxy credentials",
        )
    }

    @Test
    fun `disabling socks clears the JVM system properties and the Authenticator`() {
        ConfigPush.applySocks(
            SocksConfigRequest(enabled = true, version = 4, host = "10.0.0.1", port = "9050"),
        )

        ConfigPush.applySocks(SocksConfigRequest(enabled = false))

        assertNull(System.getProperty("socksProxyHost"))
        assertNull(System.getProperty("socksProxyPort"))
        assertNull(System.getProperty("socksProxyVersion"))
        assertNull(Authenticator.getDefault())
    }

    @Test
    fun `a partial push merges onto the existing state before re-applying`() {
        ConfigPush.applySocks(
            SocksConfigRequest(enabled = true, version = 5, host = "127.0.0.1", port = "1080"),
        )

        // Only the port changes; enabled/version/host must be carried over from the prior push.
        ConfigPush.applySocks(SocksConfigRequest(port = "1081"))

        assertEquals("127.0.0.1", System.getProperty("socksProxyHost"))
        assertEquals("1081", System.getProperty("socksProxyPort"))
        assertEquals("5", System.getProperty("socksProxyVersion"))
    }
}
