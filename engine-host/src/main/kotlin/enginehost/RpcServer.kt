package enginehost

/*
 * Thin HTTP/JSON RPC over the JDK's built-in com.sun.net.httpserver (zero extra deps).
 * Every content call keys on (sourceId, url); an unknown sourceId is a 400. Source and extension
 * work run outside the HTTP front door so a blocked extension cannot starve control requests.
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
import java.util.concurrent.ExecutorService
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Exposes loaded sources, extension management, per-source preferences, and configuration over
 * HTTP/JSON. It owns no library state; source content is resolved per request by (sourceId, url).
 * When [executors] is omitted, the server owns and closes its default execution domains.
 */
class RpcServer(
    private val loader: ExtensionLoader,
    private val extensions: ExtensionManager,
    private val port: Int,
    executors: RpcExecutors? = null,
) {
    private val logger = KotlinLogging.logger {}
    private val ownsExecutors = executors == null
    private val rpcExecutors = executors ?: RpcExecutors()

    // A malformed body or an unknown field is a client error (400), never an upstream 502. Ignoring
    // unknown properties also lets the contract carry forward-compatible request fields.
    private val mapper: ObjectMapper =
        jacksonObjectMapper().configure(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false)
    private lateinit var server: HttpServer

    fun start() {
        server = HttpServer.create(InetSocketAddress(port), 0)
        server.executor = rpcExecutors.frontDoorExecutor

        // Direct, bounded control handlers never wait on source or extension capacity.
        server.createContext("/health") { exchange ->
            ResponseGuard(exchange).respondJson(200, mapOf("status" to "ok", "sources" to loader.loaded().size))
        }
        server.createContext("/config", ::handleConfig)

        // Source calls are submitted and the callback returns without executing extension code.
        server.createContext("/search") { exchange ->
            submitSource(exchange) { request: SearchRequest -> SourceCalls.search(request.source(), request.query, request.page) }
        }
        server.createContext("/popular") { exchange ->
            submitSource(exchange) { request: BrowseRequest -> SourceCalls.popular(request.source(), request.page) }
        }
        server.createContext("/latest") { exchange ->
            submitSource(exchange) { request: BrowseRequest -> SourceCalls.latest(request.source(), request.page) }
        }
        server.createContext("/manga") { exchange ->
            submitSource(exchange) { request: MangaRequest -> SourceCalls.mangaDetails(request.source(), request.url) }
        }
        server.createContext("/chapters") { exchange ->
            submitSource(exchange) { request: ChaptersRequest ->
                SourceCalls.chapters(request.source(), request.url, request.mangaTitle)
            }
        }
        server.createContext("/pages") { exchange ->
            submitSource(exchange) { request: PagesRequest ->
                SourceCalls.pages(request.source(), request.chapterUrl, request.mangaUrl)
            }
        }
        server.createContext("/image") { exchange ->
            submit(rpcExecutors.sourceExecutor, exchange, "image request") { response ->
                handleImage(exchange, response)
            }
        }

        // Registry, preferences, and extension mutations share the single-writer domain.
        server.createContext("/sources") { exchange ->
            submit(rpcExecutors.extensionExecutor, exchange, "sources request") { response ->
                handleSources(exchange, response)
            }
        }
        server.createContext("/extensions") { exchange ->
            submit(rpcExecutors.extensionExecutor, exchange, "extensions request") { response ->
                handleExtensions(exchange, response)
            }
        }
        server.createContext("/repos") { exchange ->
            submit(rpcExecutors.extensionExecutor, exchange, "repos request") { response ->
                handleRepos(exchange, response)
            }
        }

        server.start()
        logger.info { "RPC server listening on http://localhost:$port" }
    }

    fun stop() {
        if (::server.isInitialized) server.stop(0)
        if (ownsExecutors) rpcExecutors.close()
    }

    // ================= source registry + preferences =================

    private fun handleSources(
        exchange: HttpExchange,
        response: ResponseGuard,
    ) {
        val path = exchange.requestURI.path
        try {
            when {
                path == "/sources" ->
                    response.respondJson(200, loader.loaded().map { LoadedSourceDto(it.id, it.name, it.lang) })

                path.endsWith("/preferences") -> handlePreferences(exchange, response, sourceIdFromPath(path))

                else -> response.respondJson(404, ErrorResponse("no route for $path"))
            }
        } catch (e: BadRequest) {
            response.respondJson(400, ErrorResponse(e.message ?: "bad request"))
        } catch (e: JacksonException) {
            response.respondJson(400, ErrorResponse("invalid request body: ${e.originalMessage}"))
        } catch (e: IllegalArgumentException) {
            response.respondJson(400, ErrorResponse(e.message ?: "bad request"))
        } catch (e: Throwable) {
            logger.warn(e) { "sources request failed" }
            response.respondJson(502, ErrorResponse("${e.javaClass.simpleName}: ${e.message}"))
        }
    }

    private fun handlePreferences(
        exchange: HttpExchange,
        response: ResponseGuard,
        sourceId: Long,
    ) {
        when (exchange.requestMethod) {
            "GET" -> response.respondJson(200, PreferencesResponse(Preferences.describe(resolve(sourceId))))
            "PUT" -> {
                val changes: Map<String, Any?> = mapper.readValue(exchange.requestBody.readBytes())
                val refreshed =
                    extensions.underLock {
                        Preferences.apply(resolve(sourceId), changes)
                        extensions.reloadForSource(sourceId)
                        Preferences.describe(resolve(sourceId))
                    }
                response.respondJson(200, PreferencesResponse(refreshed))
            }
            else -> response.respondJson(405, ErrorResponse("GET or PUT only"))
        }
    }

    // ================= extension management =================

    private fun handleExtensions(
        exchange: HttpExchange,
        response: ResponseGuard,
    ) {
        val path = exchange.requestURI.path
        try {
            when {
                path == "/extensions" && exchange.requestMethod == "GET" ->
                    response.respondJson(200, extensions.list())

                path == "/extensions/install" && exchange.requestMethod == "POST" -> {
                    val request: InstallRequest = mapper.readValue(exchange.requestBody.readBytes())
                    response.respondJson(200, extensions.install(request.pkgName, request.apkUrl))
                }

                path == "/extensions/refresh" && exchange.requestMethod == "POST" -> {
                    extensions.refresh()
                    response.respondJson(200, extensions.list())
                }

                path.endsWith("/update") && exchange.requestMethod == "POST" ->
                    response.respondJson(200, extensions.update(pkgNameFromPath(path, "/update")))

                exchange.requestMethod == "DELETE" ->
                    response.respondJson(200, extensions.uninstall(pkgNameFromPath(path, null)))

                else -> response.respondJson(404, ErrorResponse("no route for ${exchange.requestMethod} $path"))
            }
        } catch (e: JacksonException) {
            response.respondJson(400, ErrorResponse("invalid request body: ${e.originalMessage}"))
        } catch (e: IllegalArgumentException) {
            response.respondJson(400, ErrorResponse(e.message ?: "bad request"))
        } catch (e: BadRequest) {
            response.respondJson(400, ErrorResponse(e.message ?: "bad request"))
        } catch (e: Throwable) {
            logger.warn(e) { "extensions request failed" }
            response.respondJson(502, ErrorResponse("${e.javaClass.simpleName}: ${e.message}"))
        }
    }

    private fun handleRepos(
        exchange: HttpExchange,
        response: ResponseGuard,
    ) {
        try {
            when (exchange.requestMethod) {
                "GET" -> response.respondJson(200, ReposDto(extensions.getRepos()))
                "PUT" -> {
                    val request: ReposDto = mapper.readValue(exchange.requestBody.readBytes())
                    extensions.setRepos(request.repos)
                    response.respondJson(200, ReposDto(extensions.getRepos()))
                }
                else -> response.respondJson(405, ErrorResponse("GET or PUT only"))
            }
        } catch (e: JacksonException) {
            response.respondJson(400, ErrorResponse("invalid request body: ${e.originalMessage}"))
        } catch (e: Throwable) {
            logger.warn(e) { "repos request failed" }
            response.respondJson(502, ErrorResponse("${e.javaClass.simpleName}: ${e.message}"))
        }
    }

    // ================= config passthrough =================

    private fun handleConfig(exchange: HttpExchange) {
        val response = ResponseGuard(exchange)
        val path = exchange.requestURI.path
        try {
            if (exchange.requestMethod != "PUT") return response.respondJson(405, ErrorResponse("PUT only"))
            when (path) {
                "/config/flaresolverr" -> {
                    val request: FlareSolverrConfigRequest = mapper.readValue(exchange.requestBody.readBytes())
                    ConfigPush.applyFlareSolverr(request)
                    response.respondJson(200, ConfigPush.readFlareSolverr())
                }
                "/config/socks" -> {
                    val request: SocksConfigRequest = mapper.readValue(exchange.requestBody.readBytes())
                    ConfigPush.applySocks(request)
                    response.respondJson(200, ConfigPush.readSocks())
                }
                "/config/impersonate" -> {
                    val request: ImpersonateConfigRequest = mapper.readValue(exchange.requestBody.readBytes())
                    ConfigPush.applyImpersonate(request)
                    response.respondJson(200, ConfigPush.readImpersonate())
                }
                else -> response.respondJson(404, ErrorResponse("no route for $path"))
            }
        } catch (e: JacksonException) {
            response.respondJson(400, ErrorResponse("invalid request body: ${e.originalMessage}"))
        } catch (e: Throwable) {
            logger.warn(e) { "config request failed" }
            response.respondJson(502, ErrorResponse("${e.javaClass.simpleName}: ${e.message}"))
        }
    }

    // ================= /image (raw bytes) =================

    private fun handleImage(
        exchange: HttpExchange,
        response: ResponseGuard,
    ) {
        try {
            if (exchange.requestMethod != "POST") return response.respondJson(405, ErrorResponse("POST only"))
            val request: ImageRequest = mapper.readValue(exchange.requestBody.readBytes())
            val (bytes, contentType) = SourceCalls.image(request.source(), request.pageUrl, request.imageUrl)
            response.respondBytes(200, bytes, contentType)
        } catch (e: BadRequest) {
            response.respondJson(400, ErrorResponse(e.message ?: "bad request"))
        } catch (e: JacksonException) {
            response.respondJson(400, ErrorResponse("invalid request body: ${e.originalMessage}"))
        } catch (e: Throwable) {
            logger.warn(e) { "image request failed" }
            response.respondJson(502, ErrorResponse("${e.javaClass.simpleName}: ${e.message}"))
        }
    }

    // ================= submitted source handler =================

    private inline fun <reified T : Any> submitSource(
        exchange: HttpExchange,
        crossinline call: (T) -> Any,
    ) {
        submit(rpcExecutors.sourceExecutor, exchange, "source request") { response ->
            try {
                if (exchange.requestMethod != "POST") return@submit response.respondJson(405, ErrorResponse("POST only"))
                val request: T = mapper.readValue(exchange.requestBody.readBytes())
                response.respondJson(200, call(request))
            } catch (e: BadRequest) {
                response.respondJson(400, ErrorResponse(e.message ?: "bad request"))
            } catch (e: JacksonException) {
                response.respondJson(400, ErrorResponse("invalid request body: ${e.originalMessage}"))
            } catch (e: IllegalArgumentException) {
                response.respondJson(400, ErrorResponse(e.message ?: "bad request"))
            } catch (e: Throwable) {
                // Extension bytecode can throw Error subclasses, not only Exceptions. Containing
                // Throwable preserves a response instead of abandoning the exchange (GAP-100).
                logger.warn(e) { "request failed" }
                response.respondJson(502, ErrorResponse("${e.javaClass.simpleName}: ${e.message}"))
            }
        }
    }

    /** Submit without waiting; only the accepted task completes the guarded exchange. */
    private fun submit(
        executor: ExecutorService,
        exchange: HttpExchange,
        operation: String,
        work: (ResponseGuard) -> Unit,
    ) {
        val response = ResponseGuard(exchange)
        try {
            executor.execute {
                try {
                    work(response)
                } catch (failure: Throwable) {
                    logger.warn(failure) { "$operation failed outside its route containment" }
                    response.respondJson(502, ErrorResponse("${failure.javaClass.simpleName}: ${failure.message}"))
                }
            }
        } catch (rejected: RejectedExecutionException) {
            logger.warn(rejected) { "$operation rejected" }
            response.respondJson(503, ErrorResponse("engine is stopping"))
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

    private fun sourceIdFromPath(path: String): Long =
        path.removePrefix("/sources/").substringBefore('/').toLongOrNull()
            ?: throw BadRequest("invalid sourceId in path $path")

    private fun pkgNameFromPath(
        path: String,
        suffix: String?,
    ): String {
        val tail = path.removePrefix("/extensions/")
        val pkg = if (suffix != null) tail.removeSuffix(suffix) else tail
        require(pkg.isNotBlank() && !pkg.contains('/')) { "invalid pkgName in path $path" }
        return pkg
    }

    /** Allows only the first terminal response attempt to write an exchange. */
    private inner class ResponseGuard(
        private val exchange: HttpExchange,
    ) {
        private val completed = AtomicBoolean(false)

        fun respondJson(
            status: Int,
            body: Any,
        ) {
            val bytes = mapper.writeValueAsBytes(body)
            respondBytes(status, bytes, "application/json")
        }

        fun respondBytes(
            status: Int,
            bytes: ByteArray,
            contentType: String,
        ) {
            if (!completed.compareAndSet(false, true)) return
            exchange.responseHeaders.add("Content-Type", contentType)
            exchange.sendResponseHeaders(status, bytes.size.toLong())
            exchange.responseBody.use { it.write(bytes) }
        }
    }
}
