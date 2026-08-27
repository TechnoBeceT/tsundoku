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
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executor
import java.util.concurrent.ExecutorService
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger

/**
 * Exposes loaded sources, extension management, per-source preferences, and configuration over
 * HTTP/JSON. It owns no library state; source content is resolved per request by (sourceId, url).
 * When [executors] is omitted, the server owns and closes its default execution domains. Injected
 * executors remain caller-owned, while the server still completes every exchange it accepted.
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
    private val lifecycleLock = Any()
    private val activeResponses = ConcurrentHashMap.newKeySet<ResponseGuard>()
    private val frontDoorDispatches = ConcurrentHashMap.newKeySet<FrontDoorDispatch>()
    private val submissions = ConcurrentHashMap.newKeySet<SubmittedExchange>()
    private val stopped = CountDownLatch(1)
    private var stopping = false

    // A malformed body or an unknown field is a client error (400), never an upstream 502. Ignoring
    // unknown properties also lets the contract carry forward-compatible request fields.
    private val mapper: ObjectMapper =
        jacksonObjectMapper().configure(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false)
    private lateinit var server: HttpServer

    fun start() {
        server = HttpServer.create(InetSocketAddress(port), 0)
        server.executor = Executor(::dispatchFrontDoor)

        // Direct, bounded control handlers never wait on source or extension capacity.
        registerContext("/health") { _, response ->
            response.respondJson(200, mapOf("status" to "ok", "sources" to loader.loaded().size))
            false
        }
        registerContext("/config") { exchange, response ->
            handleConfig(exchange, response)
            false
        }

        // Source calls are submitted and the callback returns without executing extension code.
        registerContext("/search") { exchange, response ->
            submitSource(exchange, response) { request: SearchRequest -> SourceCalls.search(request.source(), request.query, request.page) }
            true
        }
        registerContext("/popular") { exchange, response ->
            submitSource(exchange, response) { request: BrowseRequest -> SourceCalls.popular(request.source(), request.page) }
            true
        }
        registerContext("/latest") { exchange, response ->
            submitSource(exchange, response) { request: BrowseRequest -> SourceCalls.latest(request.source(), request.page) }
            true
        }
        registerContext("/manga") { exchange, response ->
            submitSource(exchange, response) { request: MangaRequest -> SourceCalls.mangaDetails(request.source(), request.url) }
            true
        }
        registerContext("/chapters") { exchange, response ->
            submitSource(exchange, response) { request: ChaptersRequest ->
                SourceCalls.chapters(request.source(), request.url, request.mangaTitle)
            }
            true
        }
        registerContext("/pages") { exchange, response ->
            submitSource(exchange, response) { request: PagesRequest ->
                SourceCalls.pages(request.source(), request.chapterUrl, request.mangaUrl)
            }
            true
        }
        registerContext("/image") { exchange, response ->
            submit(rpcExecutors.sourceExecutor, response, "image request") {
                handleImage(exchange, response)
            }
            true
        }

        // Registry, preferences, and extension mutations share the single-writer domain.
        registerContext("/sources") { exchange, response ->
            submit(rpcExecutors.extensionExecutor, response, "sources request") {
                handleSources(exchange, response)
            }
            true
        }
        registerContext("/extensions") { exchange, response ->
            submit(rpcExecutors.extensionExecutor, response, "extensions request") {
                handleExtensions(exchange, response)
            }
            true
        }
        registerContext("/repos") { exchange, response ->
            submit(rpcExecutors.extensionExecutor, response, "repos request") {
                handleRepos(exchange, response)
            }
            true
        }

        server.start()
        logger.info { "RPC server listening on http://localhost:$port" }
    }

    fun stop() {
        val owner: Boolean
        val acceptedFrontDoor: List<FrontDoorDispatch>
        val acceptedTasks: List<SubmittedExchange>
        val acceptedResponses: List<ResponseGuard>
        synchronized(lifecycleLock) {
            owner = !stopping
            if (owner) {
                stopping = true
                acceptedFrontDoor = frontDoorDispatches.toList()
                acceptedTasks = submissions.toList()
                acceptedResponses = activeResponses.toList()
            } else {
                acceptedFrontDoor = emptyList()
                acceptedTasks = emptyList()
                acceptedResponses = emptyList()
            }
        }
        if (!owner) {
            awaitStopped()
            return
        }

        try {
            acceptedFrontDoor.forEach(FrontDoorDispatch::shutdown)
            acceptedTasks.forEach(SubmittedExchange::shutdown)
            acceptedResponses.forEach(ResponseGuard::respondShutdown)
            if (ownsExecutors) rpcExecutors.close()
            acceptedFrontDoor.forEach(FrontDoorDispatch::awaitCompletion)
            acceptedResponses.forEach(ResponseGuard::awaitCompletion)
            if (::server.isInitialized) server.stop(0)
        } finally {
            stopped.countDown()
        }
    }

    private fun dispatchFrontDoor(command: Runnable) {
        val dispatch = FrontDoorDispatch(command)
        val rejection =
            synchronized(lifecycleLock) {
                if (stopping) {
                    RpcRejection.SHUTDOWN
                } else {
                    frontDoorDispatches.add(dispatch)
                    try {
                        rpcExecutors.frontDoorExecutor.execute(dispatch)
                        null
                    } catch (_: RejectedExecutionException) {
                        if (rpcExecutors.frontDoorExecutor.isShutdown) RpcRejection.SHUTDOWN else RpcRejection.CAPACITY
                    }
                }
            }
        if (rejection != null) dispatch.reject(rejection)
    }

    /** Tracks accepted JDK dispatches separately from ownership of the executor that runs them. */
    private inner class FrontDoorDispatch(
        private val command: Runnable,
    ) : Runnable {
        private val state = AtomicInteger(FRONT_DOOR_QUEUED)
        private val completion = CountDownLatch(1)

        override fun run() {
            if (!state.compareAndSet(FRONT_DOOR_QUEUED, FRONT_DOOR_RUNNING)) return
            try {
                command.run()
            } finally {
                complete()
            }
        }

        fun reject(rejection: RpcRejection) {
            if (!state.compareAndSet(FRONT_DOOR_QUEUED, FRONT_DOOR_STOPPED)) return
            try {
                rpcExecutors.runFrontDoorRejection(rejection, command)
            } finally {
                complete()
            }
        }

        fun shutdown() = reject(RpcRejection.SHUTDOWN)

        fun awaitCompletion() {
            try {
                completion.await(RESPONSE_WAIT_SECONDS, TimeUnit.SECONDS)
            } catch (_: InterruptedException) {
                Thread.currentThread().interrupt()
            }
        }

        private fun complete() {
            state.set(FRONT_DOOR_COMPLETED)
            frontDoorDispatches.remove(this)
            completion.countDown()
        }
    }

    private fun registerContext(
        path: String,
        handler: (HttpExchange, ResponseGuard) -> Boolean,
    ) {
        server.createContext(path) { exchange ->
            val response = ResponseGuard(exchange)
            val accepted =
                synchronized(lifecycleLock) {
                    if (stopping) false else activeResponses.add(response)
                }
            if (!accepted) return@createContext response.respondShutdown()

            var transferred = false
            try {
                when (rpcExecutors.currentFrontDoorRejection()) {
                    RpcRejection.CAPACITY -> response.respondBusy()
                    RpcRejection.SHUTDOWN -> response.respondShutdown()
                    null -> transferred = handler(exchange, response)
                }
            } catch (failure: Throwable) {
                logger.warn(failure) { "front-door request failed" }
                response.respondJson(502, ErrorResponse("${failure.javaClass.simpleName}: ${failure.message}"))
            } finally {
                if (!transferred) activeResponses.remove(response)
            }
        }
    }

    private fun awaitStopped() {
        try {
            stopped.await(STOP_WAIT_SECONDS, TimeUnit.SECONDS)
        } catch (_: InterruptedException) {
            Thread.currentThread().interrupt()
        }
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

    private fun handleConfig(
        exchange: HttpExchange,
        response: ResponseGuard,
    ) {
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
        response: ResponseGuard,
        crossinline call: (T) -> Any,
    ) {
        submit(rpcExecutors.sourceExecutor, response, "source request") {
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
        response: ResponseGuard,
        operation: String,
        work: () -> Unit,
    ) {
        val task = SubmittedExchange(response, operation, work)
        val rejection =
            synchronized(lifecycleLock) {
                if (stopping) {
                    RpcRejection.SHUTDOWN
                } else {
                    submissions.add(task)
                    try {
                        executor.execute(task)
                        null
                    } catch (_: RejectedExecutionException) {
                        submissions.remove(task)
                        if (executor.isShutdown) RpcRejection.SHUTDOWN else RpcRejection.CAPACITY
                    }
                }
            }
        if (rejection != null) task.reject(rejection)
    }

    private inner class SubmittedExchange(
        private val response: ResponseGuard,
        private val operation: String,
        private val work: () -> Unit,
    ) : ShutdownAwareTask {
        private val state = AtomicInteger(SUBMISSION_QUEUED)

        override fun run() {
            if (!state.compareAndSet(SUBMISSION_QUEUED, SUBMISSION_RUNNING)) return
            try {
                try {
                    work()
                } catch (failure: Throwable) {
                    logger.warn(failure) { "$operation failed outside its route containment" }
                    response.respondJson(502, ErrorResponse("${failure.javaClass.simpleName}: ${failure.message}"))
                }
            } finally {
                state.set(SUBMISSION_COMPLETED)
                submissions.remove(this)
                activeResponses.remove(response)
            }
        }

        fun reject(rejection: RpcRejection) {
            if (state.compareAndSet(SUBMISSION_QUEUED, SUBMISSION_STOPPED)) {
                if (rejection == RpcRejection.CAPACITY) response.respondBusy() else response.respondShutdown()
                submissions.remove(this)
                activeResponses.remove(response)
            }
            response.awaitCompletion()
        }

        override fun shutdown() {
            state.compareAndSet(SUBMISSION_QUEUED, SUBMISSION_STOPPED)
            response.respondShutdown()
            activeResponses.remove(response)
            if (state.get() == SUBMISSION_STOPPED) submissions.remove(this)
            response.awaitCompletion()
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
        private val completion = CountDownLatch(1)

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
            try {
                exchange.responseHeaders.add("Content-Type", contentType)
                exchange.sendResponseHeaders(status, bytes.size.toLong())
                exchange.responseBody.use { it.write(bytes) }
            } finally {
                completion.countDown()
            }
        }

        fun respondBusy() = respondLifecycle(BUSY_MESSAGE)

        fun respondShutdown() = respondLifecycle(SHUTDOWN_MESSAGE)

        private fun respondLifecycle(message: String) {
            try {
                respondJson(503, ErrorResponse(message))
            } catch (failure: Throwable) {
                logger.debug(failure) { "client disconnected before lifecycle response completed" }
            }
        }

        fun awaitCompletion() {
            try {
                completion.await(RESPONSE_WAIT_SECONDS, TimeUnit.SECONDS)
            } catch (_: InterruptedException) {
                Thread.currentThread().interrupt()
            }
        }
    }

    private companion object {
        const val BUSY_MESSAGE = "server busy"
        const val SHUTDOWN_MESSAGE = "server shutting down"
        const val RESPONSE_WAIT_SECONDS = 5L
        const val STOP_WAIT_SECONDS = 5L
        const val FRONT_DOOR_QUEUED = 0
        const val FRONT_DOOR_RUNNING = 1
        const val FRONT_DOOR_STOPPED = 2
        const val FRONT_DOOR_COMPLETED = 3
        const val SUBMISSION_QUEUED = 0
        const val SUBMISSION_RUNNING = 1
        const val SUBMISSION_STOPPED = 2
        const val SUBMISSION_COMPLETED = 3
    }
}
