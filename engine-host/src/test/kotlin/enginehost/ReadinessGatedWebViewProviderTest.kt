package enginehost

import android.os.Looper
import android.webkit.WebView
import android.webkit.WebViewProvider
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import sun.misc.Unsafe
import java.lang.reflect.Proxy
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertTrue
import kotlin.time.Duration.Companion.milliseconds
import kotlin.time.Duration.Companion.seconds

class ReadinessGatedWebViewProviderTest {
    @Test
    fun `disabled provider invocation fails immediately without reaching delegate`() {
        val delegateCalls = AtomicInteger()
        val lifecycle = KcefLifecycle(initialize = {})
        try {
            lifecycle.start(enabled = false)
            val provider =
                ReadinessGatedWebViewProvider(
                    delegate = providerDouble(delegateCalls),
                    lifecycle = lifecycle,
                    callerTimeout = 1.seconds,
                )

            val error = runCatching { provider.init(emptyMap(), false) }.exceptionOrNull()

            assertIs<WebViewUnavailableException>(error)
            assertEquals(0, delegateCalls.get())
        } finally {
            lifecycle.close()
        }
    }

    @Test
    fun `provider waits for shared readiness before delegating once`() =
        runBlocking {
            val release = CompletableDeferred<Unit>()
            val delegateCalls = AtomicInteger()
            val lifecycle = KcefLifecycle(initialize = { release.await() })
            try {
                lifecycle.start(enabled = true)
                val provider =
                    ReadinessGatedWebViewProvider(
                        delegate = providerDouble(delegateCalls),
                        lifecycle = lifecycle,
                        callerTimeout = 1.seconds,
                    )

                val invocation = async(Dispatchers.Default) { provider.init(emptyMap(), false) }
                delay(30.milliseconds)
                assertFalse(invocation.isCompleted)
                assertEquals(0, delegateCalls.get())

                release.complete(Unit)
                invocation.await()

                assertEquals(1, delegateCalls.get())
            } finally {
                lifecycle.close()
            }
        }

    @Test
    fun `provider invocation after terminal initialization failure never reaches delegate`() =
        runBlocking {
            val delegateCalls = AtomicInteger()
            val lifecycle = KcefLifecycle(initialize = { error("private initializer detail") })
            try {
                lifecycle.start(enabled = true)
                withTimeout(1.seconds) {
                    while (lifecycle.snapshot().state != KcefState.FAILED) delay(5.milliseconds)
                }
                val provider = ReadinessGatedWebViewProvider(providerDouble(delegateCalls), lifecycle)

                val error = runCatching { provider.init(emptyMap(), false) }.exceptionOrNull()

                assertIs<WebViewUnavailableException>(error)
                assertEquals("embedded browser unavailable", error.message)
                assertEquals(0, delegateCalls.get())
            } finally {
                lifecycle.close()
            }
        }

    @Test
    fun `installed production factory wraps the pinned concrete provider`() {
        EngineRuntimeIntegrationTestSetup.ensureReady()
        val lifecycle = KcefLifecycle(initialize = {})
        val factoryField = WebView::class.java.getDeclaredField("mProviderFactory").apply { trySetAccessible() }
        val previousFactory = factoryField.get(null)
        try {
            installReadinessGatedWebViewProvider(lifecycle)
            val factory = factoryField.get(null) as xyz.nulldev.androidcompat.CallableArgument<*, *>
            @Suppress("UNCHECKED_CAST")
            val provider =
                (factory as xyz.nulldev.androidcompat.CallableArgument<WebView, WebViewProvider>)
                    .call(webViewWithoutConstructor())

            assertIs<ReadinessGatedWebViewProvider>(provider)
            val delegateField =
                ReadinessGatedWebViewProvider::class.java
                    .getDeclaredField("delegate")
                    .apply { trySetAccessible() }
            assertTrue(delegateField.get(provider) is xyz.nulldev.androidcompat.webkit.KcefWebViewProvider)
        } finally {
            factoryField.set(null, previousFactory)
            lifecycle.close()
        }
    }

    private fun providerDouble(initCalls: AtomicInteger): WebViewProvider =
        Proxy.newProxyInstance(
            WebViewProvider::class.java.classLoader,
            arrayOf(WebViewProvider::class.java),
        ) { _, method, _ ->
            if (method.name == "init") initCalls.incrementAndGet()
            when (method.returnType) {
                java.lang.Boolean.TYPE -> false
                java.lang.Integer.TYPE -> 0
                java.lang.Long.TYPE -> 0L
                java.lang.Float.TYPE -> 0F
                java.lang.Double.TYPE -> 0.0
                java.lang.Void.TYPE -> null
                else -> null
            }
        } as WebViewProvider

    private fun webViewWithoutConstructor(): WebView {
        if (Looper.myLooper() == null) Looper.prepare()
        val unsafeField = Unsafe::class.java.getDeclaredField("theUnsafe").apply { trySetAccessible() }
        val unsafe = unsafeField.get(null) as Unsafe
        val view = unsafe.allocateInstance(WebView::class.java) as WebView
        val looperField = WebView::class.java.getDeclaredField("mWebViewThread").apply { trySetAccessible() }
        looperField.set(view, Looper.myLooper())
        return view
    }
}
