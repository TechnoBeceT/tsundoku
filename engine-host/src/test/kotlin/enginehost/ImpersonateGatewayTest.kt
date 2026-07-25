package enginehost

/*
 * GAP-111 — impersonate-gateway image routing.
 *
 * SourceCalls.image() tries the Chrome-fingerprint impersonate gateway first (when enabled) and falls
 * back to okhttp on any failure. The routing decision lives in SourceCalls.fetchViaGateway: a non-null
 * return is the SUCCESS path (image() returns those bytes and NEVER touches okhttp); a null return is
 * the FALLBACK signal (image() runs its okhttp path). These tests drive fetchViaGateway against a stub
 * HTTP gateway (com.sun.net.httpserver.HttpServer — no real source, no network), so:
 *   (a) gateway 200 + X-Upstream-Status 200 -> bytes returned  == okhttp NOT used
 *   (b) gateway 502 (X-Gateway-Error)       -> null            == falls back to okhttp
 *   (c) gateway 200 + upstream non-2xx      -> null            == falls back to okhttp
 * plus buildSocksString (d) and the request-forwarding contract (url/method/headers/socks).
 *
 * A second group drives the guarded entry point SourceCalls.tryImpersonateGateway (which reads the
 * pushed ImpersonateConfig + SOCKS INSIDE its runCatching), proving the fall-through is exception-safe:
 *   - a transport error (refused localhost port) is caught -> null == falls back to okhttp
 *   - a disabled config skips the gateway entirely         -> null == byte-identical okhttp path
 */

import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.fasterxml.jackson.module.kotlin.readValue
import com.sun.net.httpserver.HttpExchange
import com.sun.net.httpserver.HttpServer
import okhttp3.OkHttpClient
import okhttp3.Request
import java.net.InetSocketAddress
import java.net.ServerSocket
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** How the stub gateway should answer one `/fetch` call. */
private data class StubResponse(
    val status: Int,
    val headers: Map<String, String> = emptyMap(),
    val body: ByteArray = ByteArray(0),
)

class ImpersonateGatewayTest {
    private var server: HttpServer? = null
    private val client = OkHttpClient()
    private val mapper = jacksonObjectMapper()

    /** The last `/fetch` request body the stub received, decoded for forwarding assertions. */
    private var captured: Map<String, Any?>? = null

    @AfterTest
    fun tearDown() {
        server?.stop(0)
    }

    /** Start a stub gateway that answers every `/fetch` with [resp], capturing the request body. */
    private fun startGateway(resp: StubResponse): String {
        val srv = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
        srv.createContext("/fetch") { ex: HttpExchange ->
            captured = mapper.readValue(ex.requestBody.readBytes())
            resp.headers.forEach { (k, v) -> ex.responseHeaders.add(k, v) }
            ex.sendResponseHeaders(resp.status, resp.body.size.toLong())
            ex.responseBody.use { it.write(resp.body) }
        }
        srv.start()
        server = srv
        return "http://127.0.0.1:${srv.address.port}"
    }

    private fun upstreamRequest(): Request =
        Request.Builder()
            .url("https://cdn.example/img.jpg")
            .get()
            .header("Referer", "https://reader.example/")
            .header("User-Agent", "Mozilla/5.0")
            // Request-side caching headers a source's okhttp client may set — the gateway strips them
            // (they are a bot signal a real browser never sends on an image GET). See the stripped-
            // header assertions in `the forwarded request carries ...`.
            .header("Cache-Control", "max-age=600")
            .header("Pragma", "no-cache")
            .build()

    /** (a) Gateway 200 + upstream 200 → bytes returned; okhttp is never reached (non-null result). */
    @Test
    fun `gateway success returns the upstream bytes`() {
        val url = startGateway(
            StubResponse(
                status = 200,
                headers = mapOf("X-Upstream-Status" to "200", "Content-Type" to "image/jpeg"),
                body = "IMGBYTES".toByteArray(),
            ),
        )

        val result = SourceCalls.fetchViaGateway(client, url, socks = null, upstream = upstreamRequest())

        assertNotNull(result, "gateway success must return a non-null (bytes, contentType)")
        assertEquals("IMGBYTES", String(result.first))
        assertEquals("image/jpeg", result.second)
    }

    /**
     * The forwarded request carries url/method/headers verbatim and the SOCKS egress, EXCEPT the
     * request-side caching headers (Cache-Control/Pragma), which are stripped (GAP-111) — they are a
     * bot signal that defeats the impersonation on a fingerprint-gating CDN. A semantic header
     * (Referer/User-Agent) still forwards, proving the strip is minimal and not over-broad.
     */
    @Test
    fun `the forwarded request carries url, method, headers and socks`() {
        val url = startGateway(
            StubResponse(
                status = 200,
                headers = mapOf("X-Upstream-Status" to "200", "Content-Type" to "image/webp"),
                body = "X".toByteArray(),
            ),
        )

        SourceCalls.fetchViaGateway(client, url, socks = "socks5://10.0.0.1:1080", upstream = upstreamRequest())

        val body = assertNotNull(captured, "the gateway must have received a request body")
        assertEquals("https://cdn.example/img.jpg", body["url"])
        assertEquals("GET", body["method"])
        assertEquals("chrome", body["impersonate"])
        assertEquals("socks5://10.0.0.1:1080", body["socks"])
        @Suppress("UNCHECKED_CAST")
        val headers = body["headers"] as Map<String, String>
        // Semantic headers forward verbatim — the strip is minimal, not over-broad.
        assertEquals("https://reader.example/", headers["Referer"])
        assertEquals("Mozilla/5.0", headers["User-Agent"])
        // Request-side caching headers are stripped (GAP-111): a bot signal a real browser never
        // sends on an image GET, which 403s the impersonation on a fingerprint-gating CDN.
        assertNull(headers["Cache-Control"], "Cache-Control must be stripped, not forwarded")
        assertNull(headers["Pragma"], "Pragma must be stripped, not forwarded")
        // A GET has no body — body_b64 is null (not an empty string).
        assertNull(body["body_b64"])
    }

    /** (b) Gateway 502 (X-Gateway-Error) → null, so image() falls back to okhttp. */
    @Test
    fun `gateway 502 falls back (null)`() {
        val url = startGateway(
            StubResponse(status = 502, headers = mapOf("X-Gateway-Error" to "DNSError: unreachable")),
        )

        val result = SourceCalls.fetchViaGateway(client, url, socks = null, upstream = upstreamRequest())

        assertNull(result, "a gateway 502 must return null (fall back to okhttp)")
    }

    /** (c) Gateway 200 but a non-2xx upstream status → null, so image() falls back to okhttp. */
    @Test
    fun `non-2xx upstream status falls back (null)`() {
        val url = startGateway(
            StubResponse(
                status = 200,
                headers = mapOf("X-Upstream-Status" to "403", "Content-Type" to "text/html"),
                body = "blocked".toByteArray(),
            ),
        )

        val result = SourceCalls.fetchViaGateway(client, url, socks = null, upstream = upstreamRequest())

        assertNull(result, "an upstream 403 must return null (fall back to okhttp)")
    }

    /** A gateway 200 with a MISSING X-Upstream-Status header is treated as a failure → null. */
    @Test
    fun `missing upstream-status header falls back (null)`() {
        val url = startGateway(StubResponse(status = 200, headers = mapOf("Content-Type" to "image/png")))

        val result = SourceCalls.fetchViaGateway(client, url, socks = null, upstream = upstreamRequest())

        assertNull(result, "a missing X-Upstream-Status must return null (fall back to okhttp)")
    }

    /**
     * A transport error (a refused connection) on the enabled path is caught by the guarded
     * [SourceCalls.tryImpersonateGateway] and turned into null, so [SourceCalls.image] falls back to
     * okhttp. Drives the guarded call against a localhost port with nothing listening — no server, no
     * network — proving the `runCatching` catch actually fires (previously verified only by reading).
     */
    @Test
    fun `a transport error is caught and falls back (null)`() {
        // Bind then immediately release a localhost port, so nothing is listening on it.
        val closedPort = ServerSocket(0).use { it.localPort }
        ConfigPush.applyImpersonate(
            ImpersonateConfigRequest(enabled = true, url = "http://127.0.0.1:$closedPort"),
        )
        try {
            val result = SourceCalls.tryImpersonateGateway(upstreamRequest())
            assertNull(result, "a refused gateway connection must be caught and return null (fall back to okhttp)")
        } finally {
            // Reset the process-global holder so it does not leak into other tests.
            ConfigPush.applyImpersonate(ImpersonateConfigRequest(enabled = false, url = ""))
        }
    }

    /**
     * The default-off no-op: with the pushed config disabled, [SourceCalls.tryImpersonateGateway]
     * skips the gateway entirely and returns null (byte-identical fall-through to the okhttp path),
     * even when a url is present. Locks the byte-identity guarantee of the config-read-inside-guard.
     */
    @Test
    fun `disabled config skips the gateway (null)`() {
        ConfigPush.applyImpersonate(
            ImpersonateConfigRequest(enabled = false, url = "http://impersonate-gateway:8788"),
        )
        try {
            val result = SourceCalls.tryImpersonateGateway(upstreamRequest())
            assertNull(result, "a disabled config must skip the gateway and return null (okhttp fall-through)")
        } finally {
            ConfigPush.applyImpersonate(ImpersonateConfigRequest(enabled = false, url = ""))
        }
    }

    /** (d) buildSocksString covers enabled/auth/version/disabled/blank-host. */
    @Test
    fun `buildSocksString formats the proxy url`() {
        assertEquals(
            "socks5://user:pass@10.0.0.1:1080",
            SourceCalls.buildSocksString(enabled = true, version = 5, host = "10.0.0.1", port = "1080", username = "user", password = "pass"),
        )
        assertEquals(
            "socks5://10.0.0.1:1080",
            SourceCalls.buildSocksString(enabled = true, version = 5, host = "10.0.0.1", port = "1080", username = "", password = ""),
        )
        assertEquals(
            "socks4://host:1080",
            SourceCalls.buildSocksString(enabled = true, version = 4, host = "host", port = "1080", username = "", password = ""),
        )
        assertNull(
            SourceCalls.buildSocksString(enabled = false, version = 5, host = "10.0.0.1", port = "1080", username = "", password = ""),
            "disabled SOCKS must yield null",
        )
        assertNull(
            SourceCalls.buildSocksString(enabled = true, version = 5, host = "", port = "1080", username = "", password = ""),
            "a blank host must yield null",
        )
    }

    /** ConfigPush.applyImpersonate/readImpersonate round-trips the holder (partial, last-writer-wins). */
    @Test
    fun `applyImpersonate updates the holder and reads back`() {
        ConfigPush.applyImpersonate(ImpersonateConfigRequest(enabled = true, url = "http://impersonate-gateway:8788"))
        var read = ConfigPush.readImpersonate()
        assertEquals(true, read.enabled)
        assertEquals("http://impersonate-gateway:8788", read.url)

        // A partial patch leaves the untouched field intact.
        ConfigPush.applyImpersonate(ImpersonateConfigRequest(enabled = false))
        read = ConfigPush.readImpersonate()
        assertEquals(false, read.enabled)
        assertEquals("http://impersonate-gateway:8788", read.url)
        assertTrue(!ImpersonateConfig.snapshot().enabled)

        // Reset the process-global holder so it does not leak into other tests.
        ConfigPush.applyImpersonate(ImpersonateConfigRequest(enabled = false, url = ""))
    }
}
