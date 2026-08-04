package enginehost

/*
 * Thin HTTP/JSON RPC over the JDK's built-in com.sun.net.httpserver (zero extra deps).
 * Every content call keys on (sourceId, url); an unknown sourceId is a 400. This is the seam
 * Tsundoku-Go's `sourceengine.Client` drives in P2. The full endpoint surface is frozen in
 * engine-host/RPC-CONTRACT.md.
 */

import com.fasterxml.jackson.core.JacksonException
import com.fasterxml.jackson.databind.DeserializationFeature
import com.fasterxml.jackson.databind.ObjectMapper
import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.fasterxml.jackson.module.kotlin.readValue
import com.sun.net.httpserver.HttpExchange
import com.sun.net.httpserver.HttpServer
import eu.kanade.tachiyomi.source.Source
import io.github.oshai.kotlinlogging.KotlinLogging
import java.net.InetSocketAddress
import java.util.concurrent.Executors
import java.util.concurrent.ThreadFactory
import java.util.concurrent.atomic.AtomicInteger

/**
 * RpcServer exposes the loaded sources + extension management + per-source preferences + config
 * passthrough over HTTP/JSON. It owns no library state; everything is resolved per request by
 * (sourceId, url) against the [ExtensionLoader] registry and the [ExtensionManager] working-set.
 */
class RpcServer(
    private val loader: ExtensionLoader,
    private val extensions: ExtensionManager,
    private val port: Int,
) {
    private val logger = KotlinLogging.logger {}

    // A malformed body or an unknown field is a client error (400), never an upstream 502. Ignoring
    // unknown properties also lets the contract carry forward-compatible fields (e.g. search filters,
    // documented as not-yet-applied) without a hard failure — see RPC-CONTRACT.md.
    private val mapper: ObjectMapper =
        jacksonObjectMapper().configure(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false)
    private lateinit var server: HttpServer

    fun start() {
        server = HttpServer.create(InetSocketAddress(port), 0)
        // The pool SIZE is unchanged; the FACTORY is what matters here — see [RpcThreadFactory],
        // whose thread names are a production safety anchor the container watchdog reads (GAP-137).
        server.executor = Executors.newFixedThreadPool(8, RpcThreadFactory())

        // ---- ops ----
        server.createContext("/health") { ex -> ex.respondJson(200, mapOf("status" to "ok", "sources" to loader.loaded().size)) }

        // ---- source calls (all url-addressed) ----
        server.createContext("/search") { ex -> ex.handle { req: SearchRequest -> SourceCalls.search(req.source(), req.query, req.page) } }
        server.createContext("/popular") { ex -> ex.handle { req: BrowseRequest -> SourceCalls.popular(req.source(), req.page) } }
        server.createContext("/latest") { ex -> ex.handle { req: BrowseRequest -> SourceCalls.latest(req.source(), req.page) } }
        server.createContext("/manga") { ex -> ex.handle { req: MangaRequest -> SourceCalls.mangaDetails(req.source(), req.url) } }
        server.createContext("/chapters") { ex -> ex.handle { req: ChaptersRequest -> SourceCalls.chapters(req.source(), req.url, req.mangaTitle) } }
        server.createContext("/pages") { ex -> ex.handle { req: PagesRequest -> SourceCalls.pages(req.source(), req.chapterUrl, req.mangaUrl) } }
        server.createContext("/image", ::handleImage)

        // ---- registry + per-source preferences (prefix context; path-routed) ----
        server.createContext("/sources", ::handleSources)

        // ---- extension management ----
        server.createContext("/extensions", ::handleExtensions)
        server.createContext("/repos", ::handleRepos)

        // ---- config passthrough ----
        server.createContext("/config", ::handleConfig)

        server.start()
        logger.info { "RPC server listening on http://localhost:$port" }
    }

    fun stop() = server.stop(0)

    // ================= source registry + preferences =================

    private fun handleSources(ex: HttpExchange) {
        val path = ex.requestURI.path
        try {
            when {
                path == "/sources" ->
                    ex.respondJson(200, loader.loaded().map { LoadedSourceDto(it.id, it.name, it.lang) })

                path.endsWith("/preferences") -> handlePreferences(ex, sourceIdFromPath(path))

                else -> ex.respondJson(404, ErrorResponse("no route for $path"))
            }
        } catch (e: BadRequest) {
            ex.respondJson(400, ErrorResponse(e.message ?: "bad request"))
        } catch (e: JacksonException) {
            ex.respondJson(400, ErrorResponse("invalid request body: ${e.originalMessage}"))
        } catch (e: IllegalArgumentException) {
            ex.respondJson(400, ErrorResponse(e.message ?: "bad request"))
        } catch (e: Throwable) {
            logger.warn(e) { "sources request failed" }
            ex.respondJson(502, ErrorResponse("${e.javaClass.simpleName}: ${e.message}"))
        }
    }

    private fun handlePreferences(
        ex: HttpExchange,
        sourceId: Long,
    ) {
        when (ex.requestMethod) {
            "GET" -> ex.respondJson(200, PreferencesResponse(Preferences.describe(resolve(sourceId))))
            "PUT" -> {
                val changes: Map<String, Any?> = mapper.readValue(ex.requestBody.readBytes())
                // The write + reload mutate shared state (SharedPreferences + the classloader-cached
                // source instance), so they run under the ExtensionManager mutation lock.
                val refreshed =
                    extensions.underLock {
                        Preferences.apply(resolve(sourceId), changes)
                        // Reload so a construction-time-cached preference is re-read.
                        extensions.reloadForSource(sourceId)
                        Preferences.describe(resolve(sourceId))
                    }
                ex.respondJson(200, PreferencesResponse(refreshed))
            }
            else -> ex.respondJson(405, ErrorResponse("GET or PUT only"))
        }
    }

    // ================= extension management =================

    private fun handleExtensions(ex: HttpExchange) {
        val path = ex.requestURI.path
        try {
            when {
                path == "/extensions" && ex.requestMethod == "GET" -> ex.respondJson(200, extensions.list())

                path == "/extensions/install" && ex.requestMethod == "POST" -> {
                    val req: InstallRequest = mapper.readValue(ex.requestBody.readBytes())
                    ex.respondJson(200, extensions.install(req.pkgName, req.apkUrl))
                }

                path == "/extensions/refresh" && ex.requestMethod == "POST" -> {
                    extensions.refresh()
                    ex.respondJson(200, extensions.list())
                }

                path.endsWith("/update") && ex.requestMethod == "POST" ->
                    ex.respondJson(200, extensions.update(pkgNameFromPath(path, "/update")))

                ex.requestMethod == "DELETE" ->
                    ex.respondJson(200, extensions.uninstall(pkgNameFromPath(path, null)))

                else -> ex.respondJson(404, ErrorResponse("no route for ${ex.requestMethod} $path"))
            }
        } catch (e: JacksonException) {
            ex.respondJson(400, ErrorResponse("invalid request body: ${e.originalMessage}"))
        } catch (e: IllegalArgumentException) {
            ex.respondJson(400, ErrorResponse(e.message ?: "bad request"))
        } catch (e: BadRequest) {
            ex.respondJson(400, ErrorResponse(e.message ?: "bad request"))
        } catch (e: Throwable) {
            logger.warn(e) { "extensions request failed" }
            ex.respondJson(502, ErrorResponse("${e.javaClass.simpleName}: ${e.message}"))
        }
    }

    private fun handleRepos(ex: HttpExchange) {
        try {
            when (ex.requestMethod) {
                "GET" -> ex.respondJson(200, ReposDto(extensions.getRepos()))
                "PUT" -> {
                    val req: ReposDto = mapper.readValue(ex.requestBody.readBytes())
                    extensions.setRepos(req.repos)
                    ex.respondJson(200, ReposDto(extensions.getRepos()))
                }
                else -> ex.respondJson(405, ErrorResponse("GET or PUT only"))
            }
        } catch (e: JacksonException) {
            ex.respondJson(400, ErrorResponse("invalid request body: ${e.originalMessage}"))
        } catch (e: Throwable) {
            logger.warn(e) { "repos request failed" }
            ex.respondJson(502, ErrorResponse("${e.javaClass.simpleName}: ${e.message}"))
        }
    }

    // ================= config passthrough =================

    private fun handleConfig(ex: HttpExchange) {
        val path = ex.requestURI.path
        try {
            if (ex.requestMethod != "PUT") return ex.respondJson(405, ErrorResponse("PUT only"))
            when (path) {
                "/config/flaresolverr" -> {
                    val req: FlareSolverrConfigRequest = mapper.readValue(ex.requestBody.readBytes())
                    ConfigPush.applyFlareSolverr(req)
                    ex.respondJson(200, ConfigPush.readFlareSolverr())
                }
                "/config/socks" -> {
                    val req: SocksConfigRequest = mapper.readValue(ex.requestBody.readBytes())
                    ConfigPush.applySocks(req)
                    ex.respondJson(200, ConfigPush.readSocks())
                }
                "/config/impersonate" -> {
                    val req: ImpersonateConfigRequest = mapper.readValue(ex.requestBody.readBytes())
                    ConfigPush.applyImpersonate(req)
                    ex.respondJson(200, ConfigPush.readImpersonate())
                }
                else -> ex.respondJson(404, ErrorResponse("no route for $path"))
            }
        } catch (e: JacksonException) {
            ex.respondJson(400, ErrorResponse("invalid request body: ${e.originalMessage}"))
        } catch (e: Throwable) {
            logger.warn(e) { "config request failed" }
            ex.respondJson(502, ErrorResponse("${e.javaClass.simpleName}: ${e.message}"))
        }
    }

    // ================= /image (raw bytes) =================

    private fun handleImage(ex: HttpExchange) {
        try {
            if (ex.requestMethod != "POST") return ex.respondJson(405, ErrorResponse("POST only"))
            val req: ImageRequest = mapper.readValue(ex.requestBody.readBytes())
            val (bytes, contentType) = SourceCalls.image(req.source(), req.pageUrl, req.imageUrl)
            ex.responseHeaders.add("Content-Type", contentType)
            ex.sendResponseHeaders(200, bytes.size.toLong())
            ex.responseBody.use { it.write(bytes) }
        } catch (e: BadRequest) {
            ex.respondJson(400, ErrorResponse(e.message ?: "bad request"))
        } catch (e: JacksonException) {
            ex.respondJson(400, ErrorResponse("invalid request body: ${e.originalMessage}"))
        } catch (e: Throwable) {
            logger.warn(e) { "image request failed" }
            ex.respondJson(502, ErrorResponse("${e.javaClass.simpleName}: ${e.message}"))
        }
    }

    // ================= generic JSON POST handler =================

    private inline fun <reified T : Any> HttpExchange.handle(crossinline call: (T) -> Any) {
        try {
            if (requestMethod != "POST") return respondJson(405, ErrorResponse("POST only"))
            val req: T = mapper.readValue(requestBody.readBytes())
            respondJson(200, call(req))
        } catch (e: BadRequest) {
            respondJson(400, ErrorResponse(e.message ?: "bad request"))
        } catch (e: JacksonException) {
            respondJson(400, ErrorResponse("invalid request body: ${e.originalMessage}"))
        } catch (e: IllegalArgumentException) {
            respondJson(400, ErrorResponse(e.message ?: "bad request"))
        } catch (e: Throwable) {
            // Throwable, not Exception: a broken/mistranslated extension throws Error subclasses
            // (VerifyError / LinkageError / NoClassDefFoundError / InstantiationError /
            // ExceptionInInitializerError), which are NOT Exceptions. Catching only Exception let them
            // escape the handler, so the exchange never got a response and the request HUNG until the
            // client timed out (the "can't install / everything hangs" symptom). Contain them as a
            // clean 502 so one bad source can never stall the engine (GAP-100).
            logger.warn(e) { "request failed" }
            respondJson(502, ErrorResponse("${e.javaClass.simpleName}: ${e.message}"))
        }
    }

    // ================= (sourceId, url) resolution + path params =================

    private class BadRequest(message: String) : RuntimeException(message)

    private fun SearchRequest.source() = resolve(sourceId)
    private fun BrowseRequest.source() = resolve(sourceId)
    private fun MangaRequest.source() = resolve(sourceId)
    private fun ChaptersRequest.source() = resolve(sourceId)
    private fun PagesRequest.source() = resolve(sourceId)
    private fun ImageRequest.source() = resolve(sourceId)

    private fun resolve(sourceId: Long): Source = loader.source(sourceId) ?: throw BadRequest("unknown sourceId $sourceId")

    /** /sources/{id}/preferences -> id. */
    private fun sourceIdFromPath(path: String): Long =
        path.removePrefix("/sources/").substringBefore('/').toLongOrNull()
            ?: throw BadRequest("invalid sourceId in path $path")

    /** /extensions/{pkgName}[suffix] -> pkgName. */
    private fun pkgNameFromPath(
        path: String,
        suffix: String?,
    ): String {
        val tail = path.removePrefix("/extensions/")
        val pkg = if (suffix != null) tail.removeSuffix(suffix) else tail
        require(pkg.isNotBlank() && !pkg.contains('/')) { "invalid pkgName in path $path" }
        return pkg
    }

    private fun HttpExchange.respondJson(
        status: Int,
        body: Any,
    ) {
        val bytes = mapper.writeValueAsBytes(body)
        responseHeaders.add("Content-Type", "application/json")
        sendResponseHeaders(status, bytes.size.toLong())
        responseBody.use { it.write(bytes) }
    }
}

// ================= RPC pool thread naming =================

/**
 * RpcThreadFactory names the RPC executor's threads `engine-rpc-<n>`, numbered from 1 PER POOL.
 *
 * 🔴 THE NAME IS A PRODUCTION SAFETY MECHANISM, NOT COSMETICS — do not rename or delete this
 * factory without reading watchdog.sh first (GAP-137). The container watchdog diagnoses a wedged
 * engine by sending SIGQUIT and identifying the RPC pool's threads IN THE THREAD DUMP BY NAME: it
 * restarts the engine only when every RPC pool thread it can see is `BLOCKED (on object monitor)`,
 * which is what separates a genuine deadlock (the monitor's owner sits OUTSIDE the pool, so all
 * threads can block) from a merely saturated pool (the owner IS a pool thread, so one slot is
 * always running). A thread name is the only handle a shell watchdog has on that distinction.
 *
 * The bare `Executors.newFixedThreadPool(n)` this replaced supplied no factory, so the JDK's
 * `Executors.DefaultThreadFactory` named the threads `pool-<N>-thread-<M>` where `N` comes from a
 * PROCESS-GLOBAL static counter shared with every library in the JVM. It is `pool-1` today only by
 * coincidence: any dependency that creates a thread pool before this one shifts the RPC pool to
 * `pool-2-*`, at which point the watchdog counts ZERO pool threads and silently declines to restart
 * a permanently deadlocked engine — reproduced end to end. Owning the name removes that coupling.
 *
 * `watchdog.sh` still accepts `pool-<n>-thread-<m>` as a FALLBACK so it keeps working against an
 * older image, but the fallback is ignored the moment any `engine-rpc-*` thread appears in the dump
 * (an unrelated library pool must never be folded into the count). This factory is the authoritative
 * half of that contract, and the fallback does NOT rescue a rename: renaming [THREAD_NAME_PREFIX]
 * here leaves the dump with no `engine-rpc-*` thread AND no `pool-*` thread from this pool, so the
 * watchdog sees zero either way.
 *
 * Behaviour is otherwise identical to `Executors.DefaultThreadFactory`: same thread group (captured
 * at factory construction), non-daemon, `NORM_PRIORITY`, default stack size. These threads serve
 * live HTTP requests, so this change is naming ONLY.
 */
internal class RpcThreadFactory : ThreadFactory {
    // Captured at construction, exactly as DefaultThreadFactory does — NOT per newThread(), which
    // would inherit the group of whichever thread happened to trigger the lazy pool growth.
    private val group: ThreadGroup? = Thread.currentThread().threadGroup

    // Per-POOL, not process-global: that global counter is the whole defect this factory fixes.
    private val nextThreadNumber = AtomicInteger(1)

    override fun newThread(r: Runnable): Thread =
        Thread(group, r, "$THREAD_NAME_PREFIX${nextThreadNumber.getAndIncrement()}", 0).apply {
            // DefaultThreadFactory normalises both of these rather than trusting the inherited
            // values; keep the parity so nothing about how requests are served changes.
            if (isDaemon) isDaemon = false
            if (priority != Thread.NORM_PRIORITY) priority = Thread.NORM_PRIORITY
        }

    internal companion object {
        /**
         * The prefix `watchdog.sh` anchors its RPC-pool predicate on (`^"engine-rpc-[0-9]+" `).
         * Changing it in one place only disables the container's deadlock recovery — change both,
         * or neither.
         */
        const val THREAD_NAME_PREFIX = "engine-rpc-"
    }
}
