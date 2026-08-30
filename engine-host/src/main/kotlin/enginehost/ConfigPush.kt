package enginehost

/*
 * ConfigPush applies Tsundoku-pushed runtime config to Suwayomi's global `serverConfig`.
 *
 * The host is STATELESS re: config — Tsundoku owns the source of truth and pushes the
 * FlareSolverr (Cloudflare-bypass) + SOCKS-proxy settings in per its own settings. Every field
 * is a `MutableStateFlow` on the singleton `serverConfig`; the CloudflareInterceptor (wired into
 * every source client unconditionally) reads `serverConfig.flareSolverrEnabled.value` etc. LIVE
 * on each request, so setting `.value` here takes effect on the very next fetch — no restart,
 * no re-instantiation.
 */

import io.github.oshai.kotlinlogging.KotlinLogging
import suwayomi.tachidesk.server.serverConfig
import java.net.Authenticator
import java.net.PasswordAuthentication

private val logger = KotlinLogging.logger {}

/**
 * ConfigPush mutates the process-global Suwayomi `serverConfig` StateFlows from the RPC layer.
 * Only the fields Tsundoku's settings-proxy surfaces today are exposed (FlareSolverr + SOCKS),
 * so P2 can retire the Suwayomi settings-proxy for parity.
 */
object ConfigPush {
    /** Push a partial FlareSolverr config; only non-null fields are applied (last-writer-wins). */
    fun applyFlareSolverr(req: FlareSolverrConfigRequest) {
        req.enabled?.let { serverConfig.flareSolverrEnabled.value = it }
        req.url?.let { serverConfig.flareSolverrUrl.value = it }
        req.session?.let { serverConfig.flareSolverrSessionName.value = it }
        req.sessionTtl?.let { serverConfig.flareSolverrSessionTtl.value = it }
        req.timeout?.let { serverConfig.flareSolverrTimeout.value = it }
        req.asResponseFallback?.let { serverConfig.flareSolverrAsResponseFallback.value = it }
        logger.info {
            "FlareSolverr config applied: enabled=${serverConfig.flareSolverrEnabled.value} " +
                "url=${serverConfig.flareSolverrUrl.value} session=${serverConfig.flareSolverrSessionName.value}"
        }
    }

    /** Read back the current FlareSolverr config (round-trip proof). */
    fun readFlareSolverr(): FlareSolverrConfigRequest =
        FlareSolverrConfigRequest(
            enabled = serverConfig.flareSolverrEnabled.value,
            url = serverConfig.flareSolverrUrl.value,
            session = serverConfig.flareSolverrSessionName.value,
            sessionTtl = serverConfig.flareSolverrSessionTtl.value,
            timeout = serverConfig.flareSolverrTimeout.value,
            asResponseFallback = serverConfig.flareSolverrAsResponseFallback.value,
        )

    /**
     * Push a partial SOCKS-proxy config; only non-null fields are applied, then the FULL merged
     * state is (re-)applied to the JVM — see [applySocksProperties] for why and how (GAP-084,
     * audit item B21).
     */
    fun applySocks(req: SocksConfigRequest) {
        req.enabled?.let { serverConfig.socksProxyEnabled.value = it }
        req.version?.let { serverConfig.socksProxyVersion.value = it }
        req.host?.let { serverConfig.socksProxyHost.value = it }
        req.port?.let { serverConfig.socksProxyPort.value = it }
        req.username?.let { serverConfig.socksProxyUsername.value = it }
        req.password?.let { serverConfig.socksProxyPassword.value = it }
        applySocksProperties()
        logger.info {
            "SOCKS config applied: enabled=${serverConfig.socksProxyEnabled.value} " +
                "v${serverConfig.socksProxyVersion.value} ${serverConfig.socksProxyHost.value}:${serverConfig.socksProxyPort.value}"
        }
    }

    /**
     * Applies (or clears) the JVM-global SOCKS `System` properties + [Authenticator] from the
     * CURRENT (merged) `serverConfig.socksProxy*` state. This is the piece GAP-084 / audit item
     * B21 found missing: `serverConfig.socksProxy*` used to be written by [applySocks] and read by
     * NOTHING — OkHttp (every source client, via `NetworkHelper.baseClientBuilder`) doesn't read
     * `serverConfig` for proxying at all; it relies on the JVM's ambient, global SOCKS support,
     * which is driven ENTIRELY by `System.setProperty("socksProxyHost"/"Port"/"Version")` +
     * `Authenticator.setDefault(...)`. Mirrors Suwayomi's own `applicationSetup()`
     * `serverConfig.subscribeTo(...)` block — the ONLY place Suwayomi ever touches those two APIs
     * (`ServerSetup.kt:452-502`).
     *
     * 🔴 CHOSEN APPROACH (option b of two considered): Suwayomi installs a long-lived
     * `serverConfig.subscribeTo` COROUTINE COLLECTOR at boot that re-applies on every subsequent
     * change. Engine-host's bootstrap is a deliberately MINIMAL SUBSET of `applicationSetup()`
     * (see Main.kt's header doc) with no equivalent persistent `serverConfig` collector loop, so
     * starting one here just to cover this single field group would be the heavier option. Instead
     * this runs SYNCHRONOUSLY inside [applySocks] itself, re-deriving the full JVM state on every
     * push. This is correct because [applySocks] is the ONLY writer of these StateFlows (the host
     * is stateless re: config; Tsundoku is the source of truth — see this file's header) and
     * reconcile calls it once at boot, so a fresh process gets the persisted SOCKS state applied
     * before the first source fetch, with no restart and no background coroutine to leak/outlive.
     *
     * 🔴 KNOWN CAVEAT — do not "fix" this here: `System.setProperty`/`Authenticator.setDefault`
     * are JVM-AMBIENT globals, honored by OkHttp (every source client, via plain `java.net.Socket`)
     * but NOT by KCEF/Chromium (the embedded off-screen WebView that solves Cloudflare challenges —
     * `CEFManager`/`KcefWebViewProvider`) — Chromium has its own independent network stack and
     * never consults JVM SOCKS system properties. **SOCKS therefore does not route Cloudflare-
     * bypass WebView traffic.** This is a real, structural limitation of the JVM-ambient-proxy
     * mechanism (the same one Suwayomi itself has), not a regression to chase.
     */
    private fun applySocksProperties() {
        if (serverConfig.socksProxyEnabled.value) {
            val host = serverConfig.socksProxyHost.value
            val port = serverConfig.socksProxyPort.value
            val version = serverConfig.socksProxyVersion.value
            val username = serverConfig.socksProxyUsername.value
            val password = serverConfig.socksProxyPassword.value

            System.setProperty("socksProxyHost", host)
            System.setProperty("socksProxyPort", port)
            System.setProperty("socksProxyVersion", version.toString())

            Authenticator.setDefault(
                object : Authenticator() {
                    override fun getPasswordAuthentication(): PasswordAuthentication? {
                        if (requestingProtocol.startsWith("SOCKS", ignoreCase = true)) {
                            return PasswordAuthentication(username, password.toCharArray())
                        }
                        return null
                    }
                },
            )
        } else {
            System.clearProperty("socksProxyHost")
            System.clearProperty("socksProxyPort")
            System.clearProperty("socksProxyVersion")

            Authenticator.setDefault(null)
        }
    }

    /** Read back the current SOCKS config. The password is intentionally OMITTED (never echoed back). */
    fun readSocks(): SocksConfigRequest =
        SocksConfigRequest(
            enabled = serverConfig.socksProxyEnabled.value,
            version = serverConfig.socksProxyVersion.value,
            host = serverConfig.socksProxyHost.value,
            port = serverConfig.socksProxyPort.value,
            username = serverConfig.socksProxyUsername.value,
            password = null,
        )

    /**
     * Push a partial impersonate-gateway config; only non-null fields are applied (last-writer-wins).
     * An explicitly EMPTY [ImpersonateConfigRequest.sourceIds] is applied as the empty set — it is the
     * meaningful "no source uses the gateway" value, not an omission.
     */
    fun applyImpersonate(req: ImpersonateConfigRequest) {
        req.enabled?.let { ImpersonateConfig.enabled = it }
        req.url?.let { ImpersonateConfig.url = it }
        req.sourceIds?.let { ImpersonateConfig.sourceIds = it.toSortedSet() }
        logger.info {
            "Impersonate config applied: enabled=${ImpersonateConfig.enabled} url=${ImpersonateConfig.url} " +
                "sources=${ImpersonateConfig.sourceIds}"
        }
    }

    /** Read back the current impersonate-gateway config (round-trip proof). */
    fun readImpersonate(): ImpersonateConfigRequest =
        ImpersonateConfigRequest(
            enabled = ImpersonateConfig.enabled,
            url = ImpersonateConfig.url,
            sourceIds = ImpersonateConfig.sourceIds.toList(),
        )

    /**
     * Push a partial image connection-policy config. An explicit empty list
     * clears the selection; an omitted list preserves the prior generation.
     */
    fun applyImageTransport(req: ImageTransportConfigRequest) {
        req.reuseSourceIds?.let { ImageTransportConfig.reuseSourceIds = it.toSortedSet() }
    }

    /** Read back the current normalized image connection-policy config. */
    fun readImageTransport(): ImageTransportConfigRequest =
        ImageTransportConfigRequest(reuseSourceIds = ImageTransportConfig.reuseSourceIds.toList())
}

/**
 * ImpersonateConfig holds Tsundoku's pushed Chrome-fingerprint image-fetch gateway config (GAP-111,
 * scoped per source by GAP-131).
 *
 * Unlike the FlareSolverr + SOCKS groups this is NOT a Suwayomi `serverConfig` field — Suwayomi has
 * no such concept — so it lives in its own process-global holder. [SourceCalls.image] reads the
 * [snapshot] LIVE on each image fetch, so a `PUT /config/impersonate` takes effect on the very next
 * fetch with no restart. The three `@Volatile` fields are written only by
 * [ConfigPush.applyImpersonate] (the single RPC writer) and read concurrently by the RPC image
 * threads; a snapshot pairs the reads so a fetch never sees a half-applied update.
 *
 * [sourceIds] is the per-source gating set. Each push builds a FRESH sorted set and REPLACES the
 * reference; the set is never mutated after assignment, so a concurrent reader always sees one
 * complete generation of it rather than a half-updated collection. It is empty by default: the
 * gateway is opt-in per source, because the gateway path and the okhttp path are NOT equivalent —
 * see [SourceCalls.image].
 */
object ImpersonateConfig {
    @Volatile
    var enabled: Boolean = false

    @Volatile
    var url: String = ""

    @Volatile
    var sourceIds: Set<Long> = emptySet()

    /** A consistent (enabled, url, sourceIds) triple read for one image fetch. */
    fun snapshot(): Snapshot = Snapshot(enabled = enabled, url = url, sourceIds = sourceIds)

    /** One image fetch's view of the impersonate config. */
    data class Snapshot(val enabled: Boolean, val url: String, val sourceIds: Set<Long>) {
        /**
         * Whether [sourceId] may use the gateway on this fetch — ALL THREE conditions: the group is
         * enabled, a gateway url is configured, and this source is in the gating set. An unlisted
         * source is never gated, which is what keeps its OkHttp interceptor chain (and therefore its
         * image descrambling) in play.
         */
        fun allows(sourceId: Long): Boolean = enabled && url.isNotBlank() && sourceId in sourceIds
    }
}

/**
 * ImageTransportConfig holds the source IDs whose cacheless image calls use
 * the source's normal pooled client. Every update installs one freshly-built,
 * sorted set reference, so concurrent readers observe a complete generation.
 */
object ImageTransportConfig {
    @Volatile
    var reuseSourceIds: Set<Long> = emptySet()

    /** A single image fetch's immutable view of the source selection. */
    fun snapshot(): Snapshot = Snapshot(reuseSourceIds = reuseSourceIds)

    data class Snapshot(val reuseSourceIds: Set<Long>) {
        fun reuses(sourceId: Long): Boolean = sourceId in reuseSourceIds
    }
}
