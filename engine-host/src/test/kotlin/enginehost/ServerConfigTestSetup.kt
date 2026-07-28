package enginehost

/*
 * Shared test-only bootstrap for Suwayomi's process-global `serverConfig`.
 *
 * The top-level `serverConfig` property is a lazy singleton resolved through GlobalConfigManager's
 * module map, which nothing in a plain test JVM registers — reading it unregistered throws
 * `NullPointerException: null cannot be cast to non-null type ServerConfig`. Main.kt's
 * `bootstrapAndroidCompat` performs this registration in production; any test whose code path
 * READS serverConfig must perform it too.
 *
 * 🔴 WHY THIS IS SHARED RATHER THAN PER-FILE. Gradle runs every test class in ONE JVM, so whether
 * serverConfig happened to be registered used to depend on which test class ran first — an
 * order-dependent trap: a test asserting a null/fall-back result passes for the WRONG reason when
 * the serverConfig read throws and a `runCatching` swallows it. Registering from one shared object
 * that every serverConfig-touching test references removes the ordering dependency.
 */

import suwayomi.tachidesk.server.ServerConfig
import suwayomi.tachidesk.server.util.ConfigTypeRegistration
import xyz.nulldev.ts.config.GlobalConfigManager

/**
 * ServerConfigTestSetup registers the Suwayomi ServerConfig module exactly once per test JVM.
 *
 * A Kotlin `object`'s initializer runs at most ONCE per JVM (lazily, on first access) — load-bearing
 * here: `ServerConfig()`'s constructor registers every setting name into the process-global
 * `SettingsRegistry`, which throws `IllegalStateException` ("uses protoNumber N already used by ...")
 * if the SAME setting is registered twice. JUnit5 creates a fresh test-class instance per `@Test`
 * method, so the registration must NOT live in a test class's own `init` block — that would re-run
 * (and blow up) on the second test.
 */
internal object ServerConfigTestSetup {
    init {
        ConfigTypeRegistration.registerCustomTypes()
        GlobalConfigManager.registerModule(ServerConfig.register { GlobalConfigManager.config })
    }

    /** No-op call site — merely referencing this object is enough to trigger its one-time [init]. */
    fun ensureRegistered() = Unit
}
