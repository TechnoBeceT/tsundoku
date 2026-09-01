package enginehost

import android.webkit.WebView
import android.webkit.WebViewProvider
import kotlinx.coroutines.runBlocking
import xyz.nulldev.androidcompat.webkit.KcefWebViewProvider
import kotlin.time.Duration

/**
 * Gates AndroidCompat's concrete provider on the engine-owned lifecycle while delegating its full
 * pinned `WebViewProvider` contract unchanged.
 */
class ReadinessGatedWebViewProvider(
    private val delegate: WebViewProvider,
    private val lifecycle: KcefLifecycle,
    private val callerTimeout: Duration = KCEFCallerTimeout,
) : WebViewProvider by delegate {
    override fun init(
        javaScriptInterfaces: Map<String, Any>?,
        privateBrowsing: Boolean,
    ) {
        runBlocking { lifecycle.awaitReady(callerTimeout) }
        delegate.init(javaScriptInterfaces, privateBrowsing)
    }
}

/** Replaces AndroidCompat's factory with the engine-owned readiness gate over its pinned provider. */
internal fun installReadinessGatedWebViewProvider(lifecycle: KcefLifecycle) {
    WebView.setProviderFactory { view ->
        ReadinessGatedWebViewProvider(KcefWebViewProvider(view), lifecycle)
    }
}
