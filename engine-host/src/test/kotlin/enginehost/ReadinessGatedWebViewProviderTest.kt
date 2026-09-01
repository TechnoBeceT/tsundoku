package enginehost

import android.webkit.WebViewProvider
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import java.lang.reflect.Proxy
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertIs
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
}
