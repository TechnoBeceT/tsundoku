package enginehost

import org.cef.CefApp
import suwayomi.tachidesk.server.serverConfig
import suwayomi.tachidesk.server.util.CEFManager
import xyz.nulldev.androidcompat.webkit.CefHelper
import java.lang.reflect.InvocationTargetException
import java.lang.reflect.Method
import kotlin.coroutines.Continuation
import kotlin.coroutines.intrinsics.COROUTINE_SUSPENDED
import kotlin.coroutines.intrinsics.suspendCoroutineUninterceptedOrReturn

/** Physical embedded-browser process controlled by one [KcefLifecycle]. */
internal interface KcefProcess : AutoCloseable {
    suspend fun initialize()

    fun isReady(): Boolean
}

/**
 * Invokes Suwayomi's pinned physical initializer in the caller's coroutine and owns JCEF cleanup.
 * The pinned public `CEFManager.init()` is deliberately not used because it moves initialization
 * into Suwayomi's process-global configuration scope.
 */
internal class PinnedKcefProcess(
    private val enable: () -> Unit = { serverConfig.kcefEnabled.value = true },
    private val initializePinned: suspend () -> Unit = ::invokePinnedCefInitializer,
    private val ready: () -> Boolean = ::pinnedKcefReady,
    private val dispose: () -> Unit = ::disposePinnedKcef,
) : KcefProcess {
    override suspend fun initialize() {
        enable()
        initializePinned()
        check(ready()) { "pinned KCEF initializer completed without a ready app" }
    }

    override fun isReady(): Boolean = ready()

    override fun close() = dispose()
}

private val pinnedInitializerMethod: Method by lazy {
    CEFManager::class.java
        .getDeclaredMethod("initAsync", Continuation::class.java)
        .also { method ->
            check(method.trySetAccessible()) { "pinned CEFManager.initAsync is not accessible" }
        }
}

/** [pinnedCefInitializerMethod] returns the compile-inspected production initializer contract. */
internal fun pinnedCefInitializerMethod(): Method = pinnedInitializerMethod

private suspend fun invokePinnedCefInitializer(): Unit =
    suspendCoroutineUninterceptedOrReturn { continuation ->
        val result =
            try {
                pinnedInitializerMethod.invoke(CEFManager, continuation)
            } catch (error: InvocationTargetException) {
                throw error.targetException
            }
        if (result === COROUTINE_SUSPENDED) COROUTINE_SUSPENDED else Unit
    }

private fun pinnedKcefReady(): Boolean {
    val app = CefHelper.cefApp.value.getOrNull() ?: return false
    return CefApp.getState() == CefApp.CefAppState.INITIALIZED && !app.isShuttingDown && !app.isTerminated
}

private fun disposePinnedKcef() {
    serverConfig.kcefEnabled.value = false
    val apps =
        listOfNotNull(
            CefHelper.cefApp.value.getOrNull(),
            CefApp.getInstanceIfAny(),
        ).distinct()
    CefHelper.cefApp.value = Result.failure(IllegalStateException("embedded browser lifecycle ended"))
    apps.forEach { app -> app.dispose() }
}
