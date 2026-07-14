package enginehost

/*
 * Tsundoku engine-host entry point.
 *
 * Portions adapted from Suwayomi-Server (Mozilla Public License 2.0):
 *   suwayomi.tachidesk.server.ServerSetup.applicationSetup (the bootstrap subset below) and its
 *   KCEF InitBrowserHandler cookie-seed binding ([KcefCookieInitHandler]).
 *
 * Bootstraps just enough of Suwayomi's AndroidCompat runtime to load real Mihon extensions and
 * answer source calls — NO Suwayomi server, NO database, NO GraphQL. The bootstrap sequence is the
 * minimal subset of ServerSetup.applicationSetup(): Android main-loop, config registration,
 * Koin/Injekt modules (createAppModule + androidCompatModule + configManagerModule + an
 * ApplicationDirs binding + the KCEF InitBrowserHandler), AndroidCompatInitializer, then startApp.
 *
 * Usage:
 *   ./gradlew run --args="[apkPathOrUrl] [port]"
 * or set env TSUNDOKU_ENGINE_APK / TSUNDOKU_ENGINE_PORT / TSUNDOKU_ENGINE_DATA.
 *
 * This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at https://mozilla.org/MPL/2.0/.
 */

import eu.kanade.tachiyomi.App
import eu.kanade.tachiyomi.createAppModule
import eu.kanade.tachiyomi.network.NetworkHelper
import io.github.oshai.kotlinlogging.KotlinLogging
import org.cef.network.CefCookieManager
import org.koin.core.context.startKoin
import org.koin.dsl.module
import suwayomi.tachidesk.global.impl.KcefWebView.Companion.toCefCookie
import suwayomi.tachidesk.server.ApplicationDirs
import suwayomi.tachidesk.server.ServerConfig
import suwayomi.tachidesk.server.serverConfig
import suwayomi.tachidesk.server.util.CEFManager
import suwayomi.tachidesk.server.util.ConfigTypeRegistration
import uy.kohesive.injekt.Injekt
import uy.kohesive.injekt.api.get
import xyz.nulldev.androidcompat.AndroidCompat
import xyz.nulldev.androidcompat.AndroidCompatInitializer
import xyz.nulldev.androidcompat.androidCompatModule
import xyz.nulldev.androidcompat.webkit.KcefWebViewProvider
import xyz.nulldev.ts.config.CONFIG_PREFIX
import xyz.nulldev.ts.config.GlobalConfigManager
import xyz.nulldev.ts.config.configManagerModule
import java.io.File

private val logger = KotlinLogging.logger {}

/*
 * Adapted from Suwayomi-Server's ServerSetup KCEF InitBrowserHandler (Mozilla Public License 2.0):
 * on off-screen browser creation, copy NetworkHelper's stored cookies (incl. FlareSolverr's
 * cf_clearance) into CEF's global cookie manager so the WebView shares the source client's session.
 */
private object KcefCookieInitHandler : KcefWebViewProvider.InitBrowserHandler {
    override fun init(provider: KcefWebViewProvider) {
        val networkHelper = Injekt.get<NetworkHelper>()
        CefCookieManager.getGlobalManager().apply {
            networkHelper.cookieStore.getStoredCookies().forEach { cookie ->
                runCatching {
                    if (!setCookie("https://" + cookie.domain, cookie.toCefCookie())) {
                        error("setCookie returned false")
                    }
                }.onFailure { logger.warn(it) { "Loading cookie ${cookie.name} failed" } }
            }
        }
    }
}

@Suppress("DEPRECATION")
private class LooperThread : Thread() {
    override fun run() {
        android.os.Looper.prepareMainLooper()
        android.os.Looper.loop()
    }
}

/** Stand up the AndroidCompat runtime on a plain JVM. Returns the app data dir. */
fun bootstrapAndroidCompat(dataRoot: File): ApplicationDirs {
    // Point every Suwayomi dir-resolver (ApplicationRootDir + ConfigManager) at our temp root.
    System.setProperty("$CONFIG_PREFIX.server.rootDir", dataRoot.absolutePath)
    dataRoot.mkdirs()

    // Android main loop (Handler/Looper the network + webview stacks expect).
    LooperThread().apply { isDaemon = true }.start()

    // Register Suwayomi's server config so `serverConfig` (used by NetworkHelper) resolves.
    ConfigTypeRegistration.registerCustomTypes()
    GlobalConfigManager.registerModule(ServerConfig.register { GlobalConfigManager.config })

    val applicationDirs = ApplicationDirs(dataRoot = dataRoot.absolutePath)
    File(applicationDirs.extensionsRoot).mkdirs()

    val app = App()
    startKoin {
        modules(
            createAppModule(app),
            androidCompatModule(),
            configManagerModule(),
            module {
                single { applicationDirs }
                // KCEF WebView init hook — seeds NetworkHelper's stored cookies into CEF's cookie
                // manager on browser creation (adapted 1:1 from Suwayomi's ServerSetup, MPL-2.0),
                // so cf_clearance / session cookies carry into the off-screen Chromium.
                single<KcefWebViewProvider.InitBrowserHandler> {
                    KcefCookieInitHandler
                }
            },
        )
    }

    AndroidCompatInitializer().init()
    AndroidCompat().startApp(app)

    logger.info { "AndroidCompat ready (dataRoot=${dataRoot.absolutePath})" }
    return applicationDirs
}

/**
 * Enable the embedded Chromium (KCEF) WebView so JS-challenge / WebView-dependent sources work.
 * KcefWebViewProvider is already registered by AndroidCompatInitializer; this flips the config
 * flag and kicks off CEFManager (off-screen, no X display). For local dev the Chromium runtime
 * is downloaded to `<dataRoot>/bin/kcef` on first run; the Docker image bundles it (Task 8).
 */
fun enableKcef() {
    serverConfig.kcefEnabled.value = true
    CEFManager.init()
    logger.info { "KCEF enabled (off-screen Chromium); initializing in background" }
}

fun main(args: Array<String>) {
    val apk = (args.getOrNull(0) ?: System.getenv("TSUNDOKU_ENGINE_APK"))?.takeUnless { it.isBlank() }
    val port = (args.getOrNull(1) ?: System.getenv("TSUNDOKU_ENGINE_PORT") ?: "7777").toInt()

    val dataRoot = File(System.getenv("TSUNDOKU_ENGINE_DATA") ?: "${System.getProperty("java.io.tmpdir")}/tsundoku-engine")
    val dirs = bootstrapAndroidCompat(dataRoot)

    // Opt-in WebView (heavy Chromium download on first run) — default off keeps the host lean.
    if (System.getenv("TSUNDOKU_ENGINE_KCEF")?.equals("true", ignoreCase = true) == true) {
        enableKcef()
    }

    val extensionsDir = File(dirs.extensionsRoot)
    val loader = ExtensionLoader(extensionsDir)
    val extensions = ExtensionManager(loader, extensionsDir)

    // Re-instantiate every extension already on the volume (the persistent working-set).
    extensions.reloadInstalled()

    // Optional bootstrap APK (a local path or http URL): install it into the working-set so it
    // survives a restart and appears in /extensions + /sources.
    if (apk != null) {
        val list = extensions.install(apkUrl = apk)
        val installed = list.filter { it.isInstalled }
        logger.info { "=== Loaded ${installed.sumOf { it.sources.size }} source(s) from ${installed.size} installed extension(s), no Suwayomi server/DB ===" }
    }
    loader.loaded().forEach { logger.info { "  source id=${it.id} name='${it.name}' lang='${it.lang}'" } }

    val server = RpcServer(loader, extensions, port)
    server.start()
    Runtime.getRuntime().addShutdownHook(Thread { server.stop() })

    logger.info { "Engine-host up. curl http://localhost:$port/health" }
    Thread.currentThread().join()
}
