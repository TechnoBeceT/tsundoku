package enginehost

/*
 * Pins the `http.agent` binding engine-host's bootstrap MUST perform after AndroidCompatInitializer
 * (GAP-137).
 *
 * AndroidCompatInitializer.init() hardcodes `http.agent` to a Chrome/91 (2021) Windows UA. Suwayomi's
 * ServerSetup.applicationSetup() immediately overwrites it by subscribing NetworkHelper's
 * userAgentFlow (ServerSetup.kt:345-350); engine-host ported the initializer call but NOT that
 * subscription, so the property stayed pinned at Chrome/91 forever.
 *
 * That property is not cosmetic. KcefWebSettings.defaultUserAgent() reads it, and
 * KcefWebViewProvider.onBeforeResourceLoad force-sets it as the `user-agent` header on EVERY WebView
 * resource load. So the embedded Chromium (144) announced itself as Chrome/91 on Windows while its
 * TLS fingerprint and JS environment were Chromium 144 on Linux — an immediate bot signal, and a UA
 * that does not match the one the okhttp path (NetworkHelper, Chrome/120+) used to earn cf_clearance.
 * Since cf_clearance is UA-bound, the seeded clearance was rejected and Cloudflare re-challenged the
 * WebView, which then sat on the interstitial until the source extension's own 90s timeout.
 */

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotEquals

class WebViewUserAgentTest {
    private companion object {
        /** The exact value AndroidCompatInitializer.init() pins — see that file's `http.agent` set. */
        const val STALE_ANDROID_COMPAT_UA =
            "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
                "(KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"

        const val CONFIGURED_UA =
            "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
                "(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

        const val RECONFIGURED_UA =
            "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
                "(KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36"
    }

    /**
     * Runs [block] with `http.agent` pre-set to the stale AndroidCompat default, restoring whatever
     * the property held before. The property is JVM-global, so leaking it would silently couple this
     * test to every other test in the same binary.
     */
    private fun withStaleHttpAgent(block: (CoroutineScope) -> Unit) {
        val original = System.getProperty("http.agent")
        System.setProperty("http.agent", STALE_ANDROID_COMPAT_UA)
        // Unconfined so a StateFlow's current value is delivered synchronously on subscribe — the
        // assertions then need no sleep and cannot flake.
        val scope = CoroutineScope(Dispatchers.Unconfined)
        try {
            block(scope)
        } finally {
            scope.cancel()
            if (original == null) System.clearProperty("http.agent") else System.setProperty("http.agent", original)
        }
    }

    @Test
    fun `replaces AndroidCompat's stale Chrome 91 default with the configured user agent`() {
        withStaleHttpAgent { scope ->
            bindWebViewUserAgent(MutableStateFlow(CONFIGURED_UA), scope)

            assertEquals(CONFIGURED_UA, System.getProperty("http.agent"))
            assertNotEquals(STALE_ANDROID_COMPAT_UA, System.getProperty("http.agent"))
        }
    }

    @Test
    fun `tracks later user-agent changes instead of binding once at startup`() {
        withStaleHttpAgent { scope ->
            val userAgent = MutableStateFlow(CONFIGURED_UA)
            bindWebViewUserAgent(userAgent, scope)

            // A one-shot read of NetworkHelper.defaultUserAgentProvider() would pass the test above
            // and fail here: the owner can change the UA at runtime, and the WebView must not drift
            // away from the okhttp client that earns the cf_clearance it relies on.
            userAgent.value = RECONFIGURED_UA

            assertEquals(RECONFIGURED_UA, System.getProperty("http.agent"))
        }
    }
}
