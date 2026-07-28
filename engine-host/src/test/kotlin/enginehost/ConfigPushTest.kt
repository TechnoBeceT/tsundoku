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

import java.net.Authenticator
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

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
