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

    /** Push a partial SOCKS-proxy config; only non-null fields are applied. */
    fun applySocks(req: SocksConfigRequest) {
        req.enabled?.let { serverConfig.socksProxyEnabled.value = it }
        req.version?.let { serverConfig.socksProxyVersion.value = it }
        req.host?.let { serverConfig.socksProxyHost.value = it }
        req.port?.let { serverConfig.socksProxyPort.value = it }
        req.username?.let { serverConfig.socksProxyUsername.value = it }
        req.password?.let { serverConfig.socksProxyPassword.value = it }
        logger.info {
            "SOCKS config applied: enabled=${serverConfig.socksProxyEnabled.value} " +
                "v${serverConfig.socksProxyVersion.value} ${serverConfig.socksProxyHost.value}:${serverConfig.socksProxyPort.value}"
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
}
