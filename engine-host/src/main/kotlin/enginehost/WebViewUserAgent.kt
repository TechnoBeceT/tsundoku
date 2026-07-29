package enginehost

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.launchIn
import kotlinx.coroutines.flow.onEach

/**
 * Binds NetworkHelper's configured user agent to the `http.agent` system property, replacing the
 * Chrome/91 (2021) Windows default that `AndroidCompatInitializer.init()` hardcodes.
 *
 * Adapted from Suwayomi-Server's `ServerSetup.applicationSetup()` (Mozilla Public License 2.0,
 * ServerSetup.kt:345-350), which performs this subscription immediately after the initializer runs.
 * Engine-host implements only a subset of that function and originally ported the initializer call
 * WITHOUT this binding — see [bootstrapAndroidCompat], which is the only caller.
 *
 * ⚠ This property is NOT cosmetic, which is why the omission was expensive (GAP-137).
 * `KcefWebSettings.defaultUserAgent()` reads it, and `KcefWebViewProvider.onBeforeResourceLoad`
 * force-sets it as the `user-agent` header on EVERY WebView resource load. Left at the default, the
 * embedded Chromium (144, Linux) announced itself as Chrome/91 on Windows — contradicting its own TLS
 * fingerprint and JS environment, and disagreeing with the UA the okhttp path used to earn
 * `cf_clearance`. That cookie is UA-bound, so a seeded clearance was rejected and the WebView was
 * re-challenged, then sat on the Cloudflare interstitial until the source extension's own 90s timeout.
 *
 * The subscription (rather than a one-shot read of `defaultUserAgentProvider()`) is load-bearing: the
 * UA is owner-configurable at runtime, and the WebView must never drift away from the client whose
 * clearance it depends on.
 */
internal fun bindWebViewUserAgent(
    userAgentFlow: StateFlow<String>,
    scope: CoroutineScope,
) {
    userAgentFlow
        .onEach { System.setProperty("http.agent", it) }
        .launchIn(scope)
}
