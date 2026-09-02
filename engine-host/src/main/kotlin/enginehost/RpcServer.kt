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
import java.io.InputStream
import java.net.InetSocketAddress
import java.nio.charset.StandardCharsets.UTF_8
import java.security.MessageDigest
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executor
import java.util.concurrent.ExecutorService
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

/**
 * Exposes loaded sources, extension management, per-source preferences, and configuration over
 * HTTP/JSON. It owns no library state; source content is resolved per request by (sourceId, url).
 * When [executors] is omitted, the server owns and closes its default execution domains. Injected
 * executors remain caller-owned, while the server still completes every exchange it accepted.
 * Repository trust reads/writes, installed-APK export, and prepared updates additionally require
 * [controlToken] and fail closed when it is absent.
 */
class RpcServer(
    private val loader: ExtensionLoader,
    private val extensions: ExtensionManager,
    private val port: Int,
    executors: RpcExecutors? = null,
    private val kcefStatus: () -> KcefStatus = { KcefStatus(KcefState.DISABLED, null) },
    private val controlToken: String? = null,
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
        server = HttpServer.create(InetSocketAddress("127.0.0.1", port), 0)
        server.executor = Executor(::dispatchFrontDoor)

        // Direct, bounded control handlers never wait on source or extension capacity.
        registerContext("/health") { _, response ->
            response.respondJson(200, mapOf("status" to "ok", "sources" to loader.loaded().size))
            false
        }
        registerContext("/status") { exchange, response ->
            if (exchange.requestMethod != "GET") {
                response.respondJson(405, ErrorResponse("GET only"))
                return@registerContext false
            }
            val ready = synchronized(lifecycleLock) { !stopping }
            response.respondJson(
                200,
                EngineStatus.from(
                    ready = ready,
                    source = rpcExecutors.sourceScheduler.snapshot(),
                    extension = rpcExecutors.extensionSnapshot(),
                    kcef = kcefStatus(),
                ),
            )
            false
        }
        registerContext("/config") { exchange, response ->
            handleConfig(exchange, response)
            false
        }

        // Source calls are submitted and the callback returns without executing extension code.
        registerContext("/search") { exchange, response ->
            submitSource(exchange, response, SearchRequest::sourceId) { request, cancellation ->
                response.respondJson(200, SourceCalls.search(request.source(), request.query, request.page, cancellation))
            }
        }
        registerContext("/popular") { exchange, response ->
            submitSource(exchange, response, BrowseRequest::sourceId) { request, cancellation ->
                response.respondJson(200, SourceCalls.popular(request.source(), request.page, cancellation))
            }
        }
        registerContext("/latest") { exchange, response ->
            submitSource(exchange, response, BrowseRequest::sourceId) { request, cancellation ->
                response.respondJson(200, SourceCalls.latest(request.source(), request.page, cancellation))
            }
        }
        registerContext("/manga") { exchange, response ->
            submitSource(exchange, response, MangaRequest::sourceId) { request, cancellation ->
                response.respondJson(200, SourceCalls.mangaDetails(request.source(), request.url, request.addressMode, request.webUrl, cancellation))
            }
        }
        registerContext("/chapters") { exchange, response ->
            submitSource(exchange, response, ChaptersRequest::sourceId) { request, cancellation ->
                response.respondJson(200, SourceCalls.chapters(request.source(), request.url, request.mangaTitle, request.addressMode, request.webUrl, cancellation))
            }
        }
        registerContext("/pages") { exchange, response ->
            submitSource(exchange, response, PagesRequest::sourceId) { request, cancellation ->
                response.respondJson(200, SourceCalls.pages(request.source(), request.chapterUrl, request.mangaUrl, request.addressMode, request.webUrl, cancellation))
            }
        }
        registerContext("/image") { exchange, response ->
            submitSource(exchange, response, ImageRequest::sourceId, "image request") { request, cancellation ->
                val (bytes, contentType) = SourceCalls.image(request.source(), request.pageUrl, request.imageUrl, cancellation)
                response.respondBytes(200, bytes, contentType)
            }
        }

        // Registry and preference mutations use the local single-writer lane.
        registerContext("/sources") { exchange, response ->
            submit(rpcExecutors.extensionExecutor, response, "sources request") {
                handleSources(exchange, response)
            }
            true
        }
        // Repository/APK preparation has its own bounded lane, so a slow transfer cannot occupy
        // the preference/registry lane. ExtensionManager still serializes only the apply phase.
        registerContext("/extensions") { exchange, response ->
            submit(extensionExecutorFor(exchange), response, "extensions request") {
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

    private fun extensionExecutorFor(exchange: HttpExchange): ExecutorService {
        val path = exchange.requestURI.path
        val needsNetwork =
            (path == "/extensions" && exchange.requestMethod == "GET") ||
                (path == "/extensions/install" && exchange.requestMethod == "POST") ||
                (path == "/extensions/refresh" && exchange.requestMethod == "POST") ||
                (path.endsWith("/prepare-update") && exchange.requestMethod == "POST") ||
                (path.endsWith("/update") && exchange.requestMethod == "POST") ||
                exchange.requestMethod == "DELETE"
        return if (needsNetwork) rpcExecutors.extensionNetworkExecutor else rpcExecutors.extensionExecutor
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
                        val source = resolve(sourceId)
                        Preferences.applyRecoverably(source, changes) {
                            extensions.reloadForSource(sourceId) { refreshedSource ->
                                PreferencesResponse(Preferences.describe(refreshedSource))
                            } ?: PreferencesResponse(Preferences.describe(source))
                        }
                    }
                response.respondJson(200, refreshed)
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
            if (
                (path.endsWith("/prepare-update") ||
                    path.endsWith("/prepare-reinstall") ||
                    path.endsWith("/activate-prepared-update") ||
                    path.endsWith("/prepared-update-outcome") ||
                    path.endsWith("/prepared-update")) &&
                !isControlAuthorized(exchange)
            ) {
                response.respondJson(401, ErrorResponse("unauthorized"))
                return
            }
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

                path.endsWith("/installed-apk") ->
                    handleInstalledApk(exchange, response, pkgNameFromPath(path, "/installed-apk"))

                path.endsWith("/prepare-update") && exchange.requestMethod == "POST" ->
                    response.respondJson(200, extensions.prepareUpdate(pkgNameFromPath(path, "/prepare-update")))

                path.endsWith("/prepare-reinstall") && exchange.requestMethod == "POST" -> {
                    val request: PrepareReinstallRequest = mapper.readValue(exchange.requestBody.readBytes())
                    val pkgName = pkgNameFromPath(path, "/prepare-reinstall")
                    require(request.pkgName == pkgName) { "prepared reinstall package does not match request path" }
                    response.respondJson(200, extensions.prepareReinstall(request))
                }

                path.endsWith("/activate-prepared-update") && exchange.requestMethod == "POST" -> {
                    val request: ActivatePreparedUpdateRequest = mapper.readValue(exchange.requestBody.readBytes())
                    val pkgName = pkgNameFromPath(path, "/activate-prepared-update")
                    require(request.pkgName == pkgName) { "prepared update package does not match request path" }
                    response.respondJson(200, extensions.activatePreparedUpdate(request))
                }

                path.endsWith("/prepared-update-outcome") && exchange.requestMethod == "POST" -> {
                    val request: PreparedUpdateOutcomeRequest = mapper.readValue(exchange.requestBody.readBytes())
                    response.respondJson(200, extensions.preparedUpdateOutcome(pkgNameFromPath(path, "/prepared-update-outcome"), request.token))
                }

                path.endsWith("/prepared-update") && exchange.requestMethod == "DELETE" -> {
                    val request: DiscardPreparedUpdateRequest = mapper.readValue(exchange.requestBody.readBytes())
                    extensions.discardPreparedUpdate(request.token, pkgNameFromPath(path, "/prepared-update"))
                    response.respondJson(200, OkResponse())
                }

                path.endsWith("/update") && exchange.requestMethod == "POST" ->
                    response.respondJson(410, ErrorResponse("direct extension update is retired; use prepared activation"))

                exchange.requestMethod == "DELETE" ->
                    response.respondJson(200, extensions.uninstall(pkgNameFromPath(path, null)))

                else -> response.respondJson(404, ErrorResponse("no route for ${exchange.requestMethod} $path"))
            }
        } catch (e: JacksonException) {
            response.respondJson(400, ErrorResponse("invalid request body: ${e.originalMessage}"))
        } catch (e: SourceRetirementConflict) {
            response.respondJson(
                409,
                SourceRetirementConflictResponse(
                    error = e.message ?: "prepared update would retire protected sources",
                    pkgName = e.pkgName,
                    sourceIds = e.sourceIds,
                ),
            )
        } catch (e: IllegalArgumentException) {
            response.respondJson(400, ErrorResponse(e.message ?: "bad request"))
        } catch (e: BadRequest) {
            response.respondJson(400, ErrorResponse(e.message ?: "bad request"))
        } catch (e: InstalledApkUnavailableException) {
            logger.warn(e) { "installed APK export failed" }
            response.respondJson(502, ErrorResponse(e.message ?: "installed APK is unavailable"))
        } catch (e: Throwable) {
            logger.warn(e) { "extensions request failed" }
            response.respondJson(502, ErrorResponse("${e.javaClass.simpleName}: ${e.message}"))
        }
    }

    private fun handleInstalledApk(
        exchange: HttpExchange,
        response: ResponseGuard,
        pkgName: String,
    ) {
        if (!isControlAuthorized(exchange)) {
            response.respondJson(401, ErrorResponse("unauthorized"))
            return
        }
        if (exchange.requestMethod != "GET") {
            response.respondJson(405, ErrorResponse("GET only"))
            return
        }
        extensions.withInstalledApk(pkgName) { apk ->
            response.respondStream(
                status = 200,
                input = apk.input,
                contentLength = apk.contentLength,
                contentType = APK_CONTENT_TYPE,
                headers =
                    mapOf(
                        "X-Tsundoku-Extension-Package" to apk.pkgName,
                        "X-Tsundoku-Extension-Version-Code" to apk.versionCode.toString(),
                        "X-Tsundoku-Extension-Version-Name" to apk.versionName,
                    ),
            )
        }
    }

    private fun handleRepos(
        exchange: HttpExchange,
        response: ResponseGuard,
    ) {
        try {
            val path = exchange.requestURI.path
            if (path == "/repos/trust" && !isControlAuthorized(exchange)) {
                response.respondJson(401, ErrorResponse("unauthorized"))
                return
            }
            when {
                path == "/repos" && exchange.requestMethod == "GET" ->
                    response.respondJson(200, ReposDto(extensions.getRepos()))

                path == "/repos" && exchange.requestMethod == "PUT" -> {
                    val request: ReposDto = mapper.readValue(exchange.requestBody.readBytes())
                    extensions.setRepos(request.repos)
                    response.respondJson(200, ReposDto(extensions.getRepos()))
                }

                path == "/repos/trust" && exchange.requestMethod == "GET" ->
                    response.respondJson(200, RepoTrustDto(extensions.getRepoTrust()))

                path == "/repos/trust" && exchange.requestMethod == "PUT" -> {
                    val request: RepoTrustRequest = mapper.readValue(exchange.requestBody.readBytes())
                    extensions.setRepoTrust(request.repoUrl, request.signerFingerprint)
                    response.respondJson(200, RepoTrustDto(extensions.getRepoTrust()))
                }

                path == "/repos" || path == "/repos/trust" -> response.respondJson(405, ErrorResponse("method not allowed"))
                else -> response.respondJson(404, ErrorResponse("no route for ${exchange.requestMethod} $path"))
            }
        } catch (e: JacksonException) {
            response.respondJson(400, ErrorResponse("invalid request body: ${e.originalMessage}"))
        } catch (e: IllegalArgumentException) {
            response.respondJson(400, ErrorResponse(e.message ?: "bad request"))
        } catch (e: Throwable) {
            logger.warn(e) { "repos request failed" }
            response.respondJson(502, ErrorResponse("${e.javaClass.simpleName}: ${e.message}"))
        }
    }

    private fun isControlAuthorized(exchange: HttpExchange): Boolean {
        val expected = controlToken?.takeIf { it.isNotBlank() } ?: return false
        val authorization = exchange.requestHeaders.getFirst("Authorization") ?: return false
        val separator = authorization.indexOf(' ')
        if (separator <= 0 || !authorization.substring(0, separator).equals("Bearer", ignoreCase = true)) return false
        val provided = authorization.substring(separator + 1)
        return MessageDigest.isEqual(expected.toByteArray(UTF_8), provided.toByteArray(UTF_8))
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
                "/config/image-transport" -> {
                    val request: ImageTransportConfigRequest = mapper.readValue(exchange.requestBody.readBytes())
                    ConfigPush.applyImageTransport(request)
                    response.respondJson(200, ConfigPush.readImageTransport())
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

    // ================= submitted source handler =================

    private inline fun <reified T : Any> submitSource(
        exchange: HttpExchange,
        response: ResponseGuard,
        crossinline sourceId: (T) -> Long,
        operation: String = "source request",
        crossinline call: (T, SourceCallCancellation) -> Unit,
    ): Boolean {
        if (exchange.requestMethod != "POST") {
            response.respondJson(405, ErrorResponse("POST only"))
            return false
        }
        val request: T =
            try {
                mapper.readValue(exchange.requestBody.readBytes())
            } catch (e: BadRequest) {
                response.respondJson(400, ErrorResponse(e.message ?: "bad request"))
                return false
            } catch (e: JacksonException) {
                response.respondJson(400, ErrorResponse("invalid request body: ${e.originalMessage}"))
                return false
            } catch (e: IllegalArgumentException) {
                response.respondJson(400, ErrorResponse(e.message ?: "bad request"))
                return false
            }

        val cancellation = SourceCallCancellation()
        submitSourceWork(sourceId(request), response, operation, cancellation::cancel) {
            try {
                call(request, cancellation)
            } catch (e: BadRequest) {
                response.respondJson(400, ErrorResponse(e.message ?: "bad request"))
            } catch (e: IllegalArgumentException) {
                response.respondJson(400, ErrorResponse(e.message ?: "bad request"))
            } catch (e: UpstreamHttpFailure) {
                logger.warn(e) { "request failed" }
                response.respondJson(502, errorResponse(e))
            } catch (e: Throwable) {
                // Extension bytecode can throw Error subclasses, not only Exceptions. Containing
                // Throwable preserves a response instead of abandoning the exchange (GAP-100).
                logger.warn(e) { "request failed" }
                response.respondJson(502, ErrorResponse("${e.javaClass.simpleName}: ${e.message}"))
            }
        }
        return true
    }

    private fun submitSourceWork(
        sourceId: Long,
        response: ResponseGuard,
        operation: String,
        cancelUnderlying: () -> Unit,
        work: () -> Unit,
    ) {
        val task = SubmittedExchange(response, operation, work)
        val submission =
            synchronized(lifecycleLock) {
                if (stopping) {
                    null
                } else {
                    submissions.add(task)
                    rpcExecutors.sourceScheduler.submit(sourceId, cancelUnderlying, task::run)
                }
            }
        when (submission) {
            null -> task.reject(RpcRejection.SHUTDOWN)
            Submission.Rejected -> task.rejectSourceQueueFull()
            is Submission.Accepted -> task.bind(submission.future)
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
        private val cancellation = AtomicReference<(() -> Unit)?>(null)

        override fun run() {
            if (!state.compareAndSet(SUBMISSION_QUEUED, SUBMISSION_RUNNING)) return
            try {
                try {
                    work()
                } catch (failure: Throwable) {
                    logger.warn(failure) { "$operation failed outside its route containment" }
                    response.respondJson(502, errorResponse(failure))
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

        fun rejectSourceQueueFull() {
            if (state.compareAndSet(SUBMISSION_QUEUED, SUBMISSION_STOPPED)) {
                response.respondSourceQueueFull()
                submissions.remove(this)
                activeResponses.remove(response)
            }
            response.awaitCompletion()
        }

        fun bind(future: java.util.concurrent.CompletableFuture<Unit>) {
            cancellation.set { future.cancel(false) }
            response.cancelOnDisconnect(rpcExecutors.clientConnectionObserver) { future.cancel(false) }
            future.whenComplete { _, failure ->
                when {
                    future.isCancelled -> shutdown()
                    failure is java.util.concurrent.TimeoutException -> response.respondSourceTimeout()
                }
            }
            if (state.get() == SUBMISSION_STOPPED) future.cancel(false)
        }

        override fun shutdown() {
            state.compareAndSet(SUBMISSION_QUEUED, SUBMISSION_STOPPED)
            // Claim the exchange's terminal response before cancellation can interrupt running
            // source work. Its route containment may otherwise race in and publish a 502 for the
            // cancellation exception even though shutdown is the reason the call ended.
            response.respondShutdown()
            cancellation.get()?.invoke()
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
        private val writeFailed = AtomicBoolean(false)
        private val writeFinished = AtomicBoolean(false)
        private val disconnectCancellation = AtomicReference<(() -> Unit)?>(null)
        private val disconnectObservation = AtomicReference<AutoCloseable?>(null)

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
            disconnectObservation.getAndSet(null)?.close()
            try {
                exchange.responseHeaders.add("Content-Type", contentType)
                exchange.sendResponseHeaders(status, bytes.size.toLong())
                exchange.responseBody.use { it.write(bytes) }
            } catch (failure: Throwable) {
                writeFailed.set(true)
                disconnectCancellation.getAndSet(null)?.invoke()
                throw failure
            } finally {
                writeFinished.set(true)
                disconnectCancellation.set(null)
                disconnectObservation.getAndSet(null)?.close()
                completion.countDown()
            }
        }

        fun respondStream(
            status: Int,
            input: InputStream,
            contentLength: Long,
            contentType: String,
            headers: Map<String, String>,
        ) {
            if (!completed.compareAndSet(false, true)) return
            disconnectObservation.getAndSet(null)?.close()
            try {
                exchange.responseHeaders.add("Content-Type", contentType)
                headers.forEach { (name, value) -> exchange.responseHeaders.add(name, value) }
                exchange.sendResponseHeaders(status, contentLength)
                exchange.responseBody.use { output -> input.copyTo(output) }
            } catch (failure: Throwable) {
                writeFailed.set(true)
                disconnectCancellation.getAndSet(null)?.invoke()
                throw failure
            } finally {
                writeFinished.set(true)
                disconnectCancellation.set(null)
                disconnectObservation.getAndSet(null)?.close()
                completion.countDown()
            }
        }

        fun respondBusy() = respondLifecycle(BUSY_MESSAGE)

        fun respondShutdown() = respondLifecycle(SHUTDOWN_MESSAGE)

        fun respondSourceQueueFull() = respondMessage(503, SOURCE_QUEUE_FULL_MESSAGE)

        fun respondSourceTimeout() = respondMessage(504, SOURCE_TIMEOUT_MESSAGE)

        fun cancelOnDisconnect(
            observer: ClientConnectionObserver,
            cancel: () -> Unit,
        ) {
            disconnectCancellation.set(cancel)
            val observation =
                observer.observe(
                    local = exchange.localAddress,
                    remote = exchange.remoteAddress,
                ) {
                    if (!completed.get()) disconnectCancellation.getAndSet(null)?.invoke()
                }
            disconnectObservation.set(observation)
            when {
                writeFailed.get() -> disconnectCancellation.getAndSet(null)?.invoke()
                writeFinished.get() -> {
                    disconnectCancellation.compareAndSet(cancel, null)
                    disconnectObservation.getAndSet(null)?.close()
                }
            }
        }

        private fun respondMessage(
            status: Int,
            message: String,
        ) {
            try {
                respondJson(status, mapOf("message" to message))
            } catch (failure: Throwable) {
                logger.debug(failure) { "client disconnected before source response completed" }
            }
        }

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
        const val APK_CONTENT_TYPE = "application/vnd.android.package-archive"
        const val BUSY_MESSAGE = "server busy"
        const val SHUTDOWN_MESSAGE = "server shutting down"
        const val SOURCE_QUEUE_FULL_MESSAGE = "source queue full"
        const val SOURCE_TIMEOUT_MESSAGE = "source call timed out"
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
