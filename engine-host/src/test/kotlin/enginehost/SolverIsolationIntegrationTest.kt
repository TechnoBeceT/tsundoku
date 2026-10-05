package enginehost

import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import eu.kanade.tachiyomi.network.interceptor.CloudflareInterceptor
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.Call
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.HttpUrl.Companion.toHttpUrl
import suwayomi.tachidesk.server.serverConfig
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import uy.kohesive.injekt.api.get
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue
import kotlin.test.assertFailsWith
import java.util.concurrent.ExecutionException

/** Exercises the dependency interceptor, including the nested solver transport. */
class SolverIsolationIntegrationTest {
    @Test
    fun `a blocked disposable solve does not serialize another browser`() {
        EngineRuntimeIntegrationTestSetup.ensureReady()
        val mapper = jacksonObjectMapper()
        val enteredA = CountDownLatch(1)
        val enteredB = CountDownLatch(1)
        val releaseA = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(2)
        val oldEnabled = serverConfig.flareSolverrEnabled.value
        val oldUrl = serverConfig.flareSolverrUrl.value
        val oldSession = serverConfig.flareSolverrSessionName.value
        val oldTimeout = serverConfig.flareSolverrTimeout.value
        val oldFallback = serverConfig.flareSolverrAsResponseFallback.value
        val calls = mutableListOf<Call>()
        MockWebServer().use { source ->
            MockWebServer().use { solver ->
                source.dispatcher = object : Dispatcher() {
                    override fun dispatch(request: RecordedRequest): MockResponse =
                        if (request.headers["User-Agent"] == "Fixture solver UA") {
                            MockResponse.Builder().body("healthy result").build()
                        } else {
                            MockResponse.Builder().code(403).addHeader("Server", "cloudflare").build()
                        }
                }
                solver.dispatcher = object : Dispatcher() {
                    override fun dispatch(request: RecordedRequest): MockResponse {
                        if (request.method == "GET") return MockResponse.Builder().code(404).build()
                        val payload = mapper.readTree(request.body!!.utf8())
                        val url = payload["url"].asText()
                        if (url.endsWith("/a")) {
                            enteredA.countDown()
                            releaseA.await(5, TimeUnit.SECONDS)
                        } else {
                            enteredB.countDown()
                        }
                        return MockResponse.Builder().body(
                            mapper.writeValueAsString(mapOf(
                                "solution" to mapOf("url" to url, "status" to 200, "cookies" to emptyList<String>(), "userAgent" to "Fixture solver UA"),
                                "status" to "ok", "message" to "Challenge solved", "startTimestamp" to 1, "endTimestamp" to 2, "version" to "fixture",
                            )),
                        ).build()
                    }
                }
                source.start()
                solver.start()
                serverConfig.flareSolverrEnabled.value = true
                serverConfig.flareSolverrUrl.value = solver.url("/").toString()
                serverConfig.flareSolverrSessionName.value = ""
                serverConfig.flareSolverrTimeout.value = 3
                serverConfig.flareSolverrAsResponseFallback.value = false
                try {
                    val client = OkHttpClient.Builder().addInterceptor(CloudflareInterceptor {}).build()
                    fun start(path: String) = client.newCall(Request.Builder().url(source.url(path)).build()).also { calls += it }
                    val a = start("/a")
                    val resultA = pool.submit<String> { a.execute().use { it.body.string() } }
                    assertTrue(enteredA.await(2, TimeUnit.SECONDS), "first request never reached nested solver")
                    val b = start("/b")
                    val resultB = pool.submit<String> { b.execute().use { it.body.string() } }
                    assertTrue(enteredB.await(1, TimeUnit.SECONDS), "independent disposable browser waited behind the blocked solve")
                    assertEquals("healthy result", resultB.get(1, TimeUnit.SECONDS))
                    a.cancel()
                    assertFailsWith<ExecutionException>("cancelled outer call must physically return before the solver is released") {
                        resultA.get(1, TimeUnit.SECONDS)
                    }
                    releaseA.countDown()
                } finally {
                    releaseA.countDown()
                    calls.forEach(Call::cancel)
                    pool.shutdownNow()
                    pool.awaitTermination(5, TimeUnit.SECONDS)
                    serverConfig.flareSolverrEnabled.value = oldEnabled
                    serverConfig.flareSolverrUrl.value = oldUrl
                    serverConfig.flareSolverrSessionName.value = oldSession
                    serverConfig.flareSolverrTimeout.value = oldTimeout
                    serverConfig.flareSolverrAsResponseFallback.value = oldFallback
                }
            }
        }
    }
    @Test
    fun `cancelled named solve drains before reuse and cancelled waiter never starts`() = listOf("exact shared session", " ").forEach { session -> fixture(session) { source, solver, client ->
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        val seen = java.util.concurrent.CopyOnWriteArrayList<String>()
        solver.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                        if (request.method == "GET") return MockResponse.Builder().code(404).build()
                val payload = jacksonObjectMapper().readTree(request.body!!.utf8())
                assertEquals(session, payload["session"].asText())
                assertEquals(3000, payload["maxTimeout"].asInt())
                assertEquals(true, payload["returnOnlyCookies"].asBoolean())
                val url = payload["url"].asText()
                seen += url
                if (url.endsWith("/a")) { entered.countDown(); release.await(5, TimeUnit.SECONDS) }
                return solution(url)
            }
        }
        val pool = Executors.newFixedThreadPool(3)
        val a = client.newCall(Request.Builder().url(source.url("/a")).build())
        val b = client.newCall(Request.Builder().url(source.url("/b")).build())
        val c = client.newCall(Request.Builder().url(source.url("/c")).build())
        try {
            val resultA = pool.submit<String> { a.execute().use { it.body.string() } }
            assertTrue(entered.await(2, TimeUnit.SECONDS))
            a.cancel()
            assertFailsWith<ExecutionException> { resultA.get(1, TimeUnit.SECONDS) }
            val resultB = pool.submit<String> { b.execute().use { it.body.string() } }
            Thread.sleep(150)
            assertEquals(1, seen.size, "named browser was reused before remote completion")
            b.cancel()
            assertFailsWith<ExecutionException> { resultB.get(1, TimeUnit.SECONDS) }
            val resultC = pool.submit<String> { c.execute().use { it.body.string() } }
            Thread.sleep(100)
            assertEquals(1, seen.size)
            release.countDown()
            assertEquals("healthy result", resultC.get(2, TimeUnit.SECONDS))
            assertEquals(listOf(source.url("/a").toString(), source.url("/c").toString()), seen.toList())
        } finally {
            release.countDown(); listOf(a, b, c).forEach(Call::cancel)
            pool.shutdownNow(); pool.awaitTermination(5, TimeUnit.SECONDS)
        }
    }

    }

    @Test
    fun `disposable cancellation after headers closes nested body transport`() = fixture("") { source, solver, client ->
        solver.enqueueSolve(solution(source.url("/body").toString()).newBuilder().bodyDelay(5, TimeUnit.SECONDS).build())
        val pool = Executors.newSingleThreadExecutor()
        val call = client.newCall(Request.Builder().url(source.url("/body")).build())
        try {
            val result = pool.submit<String> { call.execute().use { it.body.string() } }
            assertTrue(solver.takeSolveRequest(2, TimeUnit.SECONDS) != null)
            Thread.sleep(150)
            call.cancel()
            assertFailsWith<ExecutionException> { result.get(1, TimeUnit.SECONDS) }
            val network = uy.kohesive.injekt.Injekt.get<eu.kanade.tachiyomi.network.NetworkHelper>()
            val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(1)
            while (network.client.dispatcher.runningCallsCount() != 0 && System.nanoTime() < deadline) Thread.sleep(10)
            assertEquals(0, network.client.dispatcher.runningCallsCount(), "nested physical HTTP survived caller cancellation")
        } finally {
            call.cancel(); pool.shutdownNow(); pool.awaitTermination(5, TimeUnit.SECONDS)
        }
    }

    @Test
    fun `malformed terminal body cannot establish named browser completion`() = fixture("poisoned") { source, solver, client ->
        solver.enqueueSolve(MockResponse.Builder().body("not a solver response").build())
        solver.enqueueSolve(solution(source.url("/second").toString()))
        assertFailsWith<java.io.IOException> {
            client.newCall(Request.Builder().url(source.url("/first")).build()).execute().close()
        }
        assertTrue(solver.takeSolveRequest(1, TimeUnit.SECONDS) != null)
        assertFailsWith<java.io.IOException>("unresolved browser must not be reused") {
            client.newCall(Request.Builder().url(source.url("/second")).build()).execute().close()
        }
        assertEquals(null, solver.takeSolveRequest(100, TimeUnit.MILLISECONDS))
    }

    @Test
    fun `host deadline releases physical source slot while named transport drains`() = fixture("deadline drain") { source, solver, client ->
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        solver.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                        if (request.method == "GET") return MockResponse.Builder().code(404).build()
                entered.countDown(); release.await(5, TimeUnit.SECONDS)
                return solution(source.url("/deadline").toString())
            }
        }
        val call = client.newCall(Request.Builder().url(source.url("/deadline")).build())
        SourceCallDeadline(java.time.Duration.ofMillis(300)).use { deadline ->
            SourceScheduler(sourceCallDeadline = deadline).use { scheduler ->
                try {
                    val work = scheduler.submit(1L, call::cancel) { call.execute().use { it.body.string() } } as Submission.Accepted
                    assertTrue(entered.await(2, TimeUnit.SECONDS))
                    assertFailsWith<ExecutionException> { work.future.get(1, TimeUnit.SECONDS) }
                    val limit = System.nanoTime() + TimeUnit.SECONDS.toNanos(1)
                    while (scheduler.snapshot(java.time.Instant.now()).running != 0 && System.nanoTime() < limit) Thread.sleep(10)
                    val snapshot = scheduler.snapshot(java.time.Instant.now())
                    assertEquals(0, snapshot.running, "public timeout did not release physical source occupancy")
                    assertEquals(1, snapshot.completionSequence)
                    assertEquals(1, snapshot.timedOut)
                } finally { call.cancel(); release.countDown() }
            }
        }
    }

    @Test
    fun `solver preserves POST fields cookie scope headers and user agent`() = fixture("payload session") { source, solver, _ ->
        val network = uy.kohesive.injekt.Injekt.get<eu.kanade.tachiyomi.network.NetworkHelper>()
        val target = source.url("/post")
        val previous = network.cookieStore.get(target)
        val oldTtl = serverConfig.flareSolverrSessionTtl.value
        var userAgent = ""
        try {
            serverConfig.flareSolverrSessionTtl.value = 37
            network.cookieStore.addAll(target, listOf(
                okhttp3.Cookie.Builder().name("ordinary").value("keep").hostOnlyDomain(target.host).path("/").build(),
                okhttp3.Cookie.Builder().name("cf_clearance").value("stale").hostOnlyDomain(target.host).path("/").build(),
            ))
            val payload = jacksonObjectMapper().readTree(solutionBody(target.toString()))
            val solution = payload["solution"] as com.fasterxml.jackson.databind.node.ObjectNode
            solution.set<com.fasterxml.jackson.databind.JsonNode>("cookies", jacksonObjectMapper().valueToTree(listOf(
                mapOf("name" to "cf_clearance", "value" to "fresh", "domain" to target.host, "path" to "/", "httpOnly" to true),
                mapOf("name" to "other_path", "value" to "excluded", "domain" to target.host, "path" to "/different"),
            )))
            solver.enqueueSolve(MockResponse.Builder().body(payload.toString()).build())
            val client = OkHttpClient.Builder().addInterceptor { chain ->
                chain.proceed(chain.request().newBuilder().header("X-Extension", "preserved").build())
            }.addInterceptor(CloudflareInterceptor { userAgent = it }).build()
            val body = okhttp3.FormBody.Builder().add("title", "A B").build()
            client.newCall(Request.Builder().url(target).header("X-Original", "keep").post(body).build()).execute().use {
                assertEquals("healthy result", it.body.string())
            }
            val request = solver.takeSolveRequest(1, TimeUnit.SECONDS)!!
            assertEquals("/v1", request.url.encodedPath)
            val sent = jacksonObjectMapper().readTree(request.body!!.utf8())
            assertEquals("request.post", sent["cmd"].asText())
            assertEquals("title=A+B", sent["postData"].asText())
            assertEquals("payload session", sent["session"].asText())
            assertEquals(37, sent["session_ttl_minutes"].asInt())
            assertEquals(listOf("ordinary"), sent["cookies"].map { it["name"].asText() })
            val original = source.takeRequest(1, TimeUnit.SECONDS)!!
            assertEquals("title=A+B", original.body!!.utf8())
            val retry = source.takeRequest(1, TimeUnit.SECONDS)!!
            assertEquals("POST", retry.method)
            assertEquals("title=A+B", retry.body!!.utf8())
            assertEquals("preserved", retry.headers["X-Extension"])
            assertEquals("keep", retry.headers["X-Original"])
            assertEquals("Fixture solver UA", retry.headers["User-Agent"])
            assertTrue(retry.headers["Cookie"]!!.contains("cf_clearance=fresh"))
            assertTrue(!retry.headers["Cookie"]!!.contains("other_path="), "path-restricted cookie escaped onto retry")
            val stored = network.cookieStore.get(target)
            assertEquals("/different", stored.single { it.name == "other_path" }.path)
            assertTrue(stored.single { it.name == "cf_clearance" }.httpOnly)
            assertTrue(stored.single { it.name == "cf_clearance" }.hostOnly)
            assertEquals("Fixture solver UA", userAgent)
        } finally {
            serverConfig.flareSolverrSessionTtl.value = oldTtl
            network.cookieStore.remove(target.toUri())
            network.cookieStore.addAll(target, previous)
        }
    }

    @Test
    fun `solver payload and retry enforce native cookie scope`() = fixture("") { _, solver, client ->
        val network = uy.kohesive.injekt.Injekt.get<eu.kanade.tachiyomi.network.NetworkHelper>()
        val domains = listOf("example.test", "books.example.test", "ample.test")
        val previous = network.cookieStore.getStoredCookies().filter { it.domain in domains }
        try {
            domains.forEach { network.cookieStore.remove("http://$it/".toHttpUrl().toUri()) }
            val cookies = listOf(
                okhttp3.Cookie.Builder().name("domain_ok").value("yes").domain("example.test").path("/").build(),
                okhttp3.Cookie.Builder().name("host_ok").value("yes").hostOnlyDomain("books.example.test").path("/").build(),
                okhttp3.Cookie.Builder().name("path_ok").value("yes").domain("example.test").path("/scope").build(),
                okhttp3.Cookie.Builder().name("parent_host_only").value("no").hostOnlyDomain("example.test").path("/").build(),
                okhttp3.Cookie.Builder().name("suffix_only").value("no").domain("ample.test").path("/").build(),
                okhttp3.Cookie.Builder().name("path_other").value("no").domain("example.test").path("/other").build(),
                okhttp3.Cookie.Builder().name("path_prefix_only").value("no").domain("example.test").path("/scop").build(),
                okhttp3.Cookie.Builder().name("expired").value("no").domain("example.test").path("/").expiresAt(1).build(),
                okhttp3.Cookie.Builder().name("secure_only").value("yes").domain("example.test").path("/").secure().build(),
            )
            network.cookieStore.addAll("http://books.example.test/".toHttpUrl(), cookies)
            for (scheme in listOf("http", "https")) {
                val target = "$scheme://books.example.test/scope/chapter".toHttpUrl()
                val original = Request.Builder().url(target).build()
                val outer = client.newCall(original)
                solver.enqueueSolve(solution(target.toString()))
                val resolved = kotlinx.coroutines.runBlocking {
                    eu.kanade.tachiyomi.network.interceptor.CFClearance.resolveWithFlareSolver(original, true, outer)
                }
                val payload = jacksonObjectMapper().readTree(solver.takeSolveRequest(1, TimeUnit.SECONDS)!!.body!!.utf8())
                val expected = setOf("domain_ok", "host_ok", "path_ok") + if (scheme == "https") setOf("secure_only") else emptySet()
                assertEquals(expected, payload["cookies"].map { it["name"].asText() }.toSet(), "solver cookie scope for $scheme")
                val retry = eu.kanade.tachiyomi.network.interceptor.CFClearance.requestWithFlareSolverr(resolved, {}, original)
                val headerNames = retry.header("Cookie")!!.split(';').map { it.trim().substringBefore('=') }.filter { it.isNotEmpty() }.toSet()
                assertEquals(expected, headerNames, "retry cookie scope for $scheme")
            }
        } finally {
            domains.forEach { network.cookieStore.remove("http://$it/".toHttpUrl().toUri()) }
            network.cookieStore.addAll("http://books.example.test/".toHttpUrl(), previous)
        }
    }

    @Test
    fun `response fallback still returns solver HTML without retrying source`() = fixture("") { source, solver, _ ->
        serverConfig.flareSolverrAsResponseFallback.value = true
        val payload = jacksonObjectMapper().readTree(solutionBody(source.url("/fallback").toString())) as com.fasterxml.jackson.databind.node.ObjectNode
        payload.put("message", "Cloudflare not detected")
        (payload["solution"] as com.fasterxml.jackson.databind.node.ObjectNode).put("response", "<html>fallback content</html>")
        solver.enqueueSolve(MockResponse.Builder().body(payload.toString()).build())
        var userAgent = ""
        val client = OkHttpClient.Builder().addInterceptor(CloudflareInterceptor { userAgent = it }).build()
        client.newCall(Request.Builder().url(source.url("/fallback")).build()).execute().use {
            assertEquals(200, it.code)
            assertEquals("<html>fallback content</html>", it.body.string())
        }
        val request = solver.takeSolveRequest(1, TimeUnit.SECONDS)!!
        assertEquals(false, jacksonObjectMapper().readTree(request.body!!.utf8())["returnOnlyCookies"].asBoolean())
        assertEquals(1, source.requestCount)
        assertEquals("Fixture solver UA", userAgent)
    }

    @Test
    fun `physical disposable solves are bounded and admission wait cancels`() = fixture("") { source, solver, client ->
        val entered = CountDownLatch(8)
        val release = CountDownLatch(1)
        val count = java.util.concurrent.atomic.AtomicInteger()
        solver.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                        if (request.method == "GET") return MockResponse.Builder().code(404).build()
                count.incrementAndGet(); entered.countDown()
                release.await(5, TimeUnit.SECONDS)
                return solution(source.url("/bounded").toString())
            }
        }
        val pool = Executors.newFixedThreadPool(9)
        val calls = (1..9).map { client.newCall(Request.Builder().url(source.url("/bounded/$it")).build()) }
        try {
            val work = calls.take(8).map { call -> pool.submit<String> { call.execute().use { it.body.string() } } }
            assertTrue(entered.await(2, TimeUnit.SECONDS))
            val waiting = pool.submit<String> { calls.last().execute().use { it.body.string() } }
            Thread.sleep(150)
            assertEquals(8, count.get(), "solver transport capacity was exceeded")
            calls.last().cancel()
            assertFailsWith<ExecutionException> { waiting.get(1, TimeUnit.SECONDS) }
            assertEquals(8, count.get(), "cancelled admission waiter started a solve")
            calls.take(8).forEach(Call::cancel)
            work.forEach { result -> assertFailsWith<ExecutionException> { result.get(1, TimeUnit.SECONDS) } }
        } finally {
            calls.forEach(Call::cancel); release.countDown()
            pool.shutdownNow(); pool.awaitTermination(5, TimeUnit.SECONDS)
        }
    }

    @Test
    fun `truncated solver transport poisons named session without reuse`() = fixture("broken transport") { source, _, client ->
        java.net.ServerSocket(0).use { socket ->
            serverConfig.flareSolverrUrl.value = "http://127.0.0.1:${socket.localPort}"
            val pool = Executors.newFixedThreadPool(2)
            val response = pool.submit {
                repeat(2) { attempt ->
                    socket.accept().use { connection ->
                        val input = connection.getInputStream().bufferedReader()
                        while (!input.readLine().isNullOrEmpty()) { }
                        val response = if (attempt == 0) "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n" else
                            "HTTP/1.1 200 OK\r\nContent-Length: 1000\r\nConnection: close\r\n\r\n{}"
                        connection.getOutputStream().write(response.toByteArray())
                        connection.getOutputStream().flush()
                    }
                }
            }
            val first = client.newCall(Request.Builder().url(source.url("/broken")).build())
            val second = client.newCall(Request.Builder().url(source.url("/second")).build())
            try {
                assertFailsWith<java.io.IOException> { first.execute().close() }
                response.get(1, TimeUnit.SECONDS)
                val result = pool.submit { second.execute().close() }
                assertFailsWith<ExecutionException> { result.get(1, TimeUnit.SECONDS) }
                socket.soTimeout = 100
                assertFailsWith<java.net.SocketTimeoutException> { socket.accept().close() }
            } finally {
                first.cancel(); second.cancel(); pool.shutdownNow(); pool.awaitTermination(5, TimeUnit.SECONDS)
            }
        }
    }

    @Test
    fun `named transport gets full drain budget after waiting for previous solve`() = fixture("queued drain budget") { source, solver, client ->
        serverConfig.flareSolverrTimeout.value = 8
        val firstEntered = CountDownLatch(1)
        val secondEntered = CountDownLatch(1)
        val thirdEntered = CountDownLatch(1)
        solver.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                        if (request.method == "GET") return MockResponse.Builder().code(404).build()
                val url = jacksonObjectMapper().readTree(request.body!!.utf8())["url"].asText()
                if (url.endsWith("/first")) { firstEntered.countDown(); Thread.sleep(12000); return solution(url) }
                if (url.endsWith("/second")) {
                    secondEntered.countDown()
                    return solution(url).newBuilder().bodyDelay(7, TimeUnit.SECONDS).build()
                }
                thirdEntered.countDown()
                return solution(url)
            }
        }
        val pool = Executors.newFixedThreadPool(3)
        val calls = listOf("/first", "/second", "/third").map { client.newCall(Request.Builder().url(source.url(it)).build()) }
        try {
            val first = pool.submit<String> { calls[0].execute().use { it.body.string() } }
            assertTrue(firstEntered.await(2, TimeUnit.SECONDS))
            val started = System.nanoTime()
            val second = pool.submit<String> { calls[1].execute().use { it.body.string() } }
            assertEquals("healthy result", first.get(15, TimeUnit.SECONDS))
            assertTrue(secondEntered.await(1, TimeUnit.SECONDS))
            assertFailsWith<ExecutionException> { second.get(8, TimeUnit.SECONDS) }
            val elapsedMillis = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - started)
            assertTrue(elapsedMillis in 17000..19500, "caller must keep original 18-second admission budget: $elapsedMillis")
            val third = pool.submit<String> { calls[2].execute().use { it.body.string() } }
            assertTrue(!thirdEntered.await(200, TimeUnit.MILLISECONDS), "browser reused while second response body still draining")
            assertEquals("healthy result", third.get(3, TimeUnit.SECONDS))
            assertTrue(thirdEntered.await(1, TimeUnit.SECONDS))
            assertEquals(4, solver.requestCount)
        } finally {
            calls.forEach(Call::cancel)
            pool.shutdownNow(); pool.awaitTermination(5, TimeUnit.SECONDS)
        }
    }

    @Test
    fun `confirmed recovery retains ownership after caller cancellation and fences exact generation`() = fixture("managed name") { source, solver, client ->
        val old = "a".repeat(32)
        val replacement = "b".repeat(32)
        val confirmEntered = CountDownLatch(1)
        val allowConfirmation = CountDownLatch(1)
        val solves = java.util.concurrent.CopyOnWriteArrayList<String>()
        val confirms = java.util.concurrent.atomic.AtomicInteger()
        solver.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                if (request.method == "GET") return MockResponse.Builder().body("""{"protocol":"fenced-drain-close-v1"}""").build()
                val payload = jacksonObjectMapper().readTree(request.body!!.utf8())
                assertEquals("managed name", payload["session"].asText())
                return when (payload["cmd"].asText()) {
                    "sessions.recovery.prepare" -> MockResponse.Builder().body("""{"protocol":"fenced-drain-close-v1","session":"managed name","generation":"$old","outcome":"prepared"}""").build()
                    "sessions.recovery.confirm" -> {
                        confirms.incrementAndGet()
                        assertEquals(old, payload["sessionGeneration"].asText())
                        confirmEntered.countDown(); allowConfirmation.await(5, TimeUnit.SECONDS)
                        MockResponse.Builder().body("""{"protocol":"fenced-drain-close-v1","session":"managed name","generation":"$replacement","previousGeneration":"$old","outcome":"drained-closed"}""").build()
                    }
                    else -> {
                        val generation = request.headers["X-Byparr-Session-Generation"]!!
                        solves += generation
                        if (generation == old) MockResponse.Builder().code(500).body("ambiguous failure").build()
                        else { assertEquals(replacement,generation); solution(payload["url"].asText()) }
                    }
                }
            }
        }
        val pool = Executors.newFixedThreadPool(2)
        val recovering = client.newCall(Request.Builder().url(source.url("/recovering")).build())
        try {
            assertFailsWith<java.io.IOException> { client.newCall(Request.Builder().url(source.url("/first")).build()).execute().close() }
            val result = pool.submit<String> { recovering.execute().use { it.body.string() } }
            assertTrue(confirmEntered.await(2, TimeUnit.SECONDS), "supported unresolved session never attempted confirmed recovery")
            recovering.cancel()
            assertFailsWith<ExecutionException> { result.get(1, TimeUnit.SECONDS) }
            val next = pool.submit<String> { client.newCall(Request.Builder().url(source.url("/next")).build()).execute().use { it.body.string() } }
            Thread.sleep(100)
            assertEquals(listOf(old),solves.toList(), "replacement used before authoritative closure")
            allowConfirmation.countDown()
            assertEquals("healthy result",next.get(2,TimeUnit.SECONDS))
            assertEquals(listOf(old,replacement),solves.toList())
            assertEquals(1,confirms.get())
        } finally {
            recovering.cancel();allowConfirmation.countDown()
            pool.shutdownNow();pool.awaitTermination(5,TimeUnit.SECONDS)
        }
    }

    @Test
    fun `malformed confirmed acknowledgements cannot clear a managed session`() {
        for (failure in listOf("numeric-generation", "numeric-session", "numeric-previous", "wrong-session", "same-generation", "http-failure")) {
            fixture("123") { source, solver, client ->
                val old = "1".repeat(32)
                val replacement = "2".repeat(32)
                val solves = java.util.concurrent.atomic.AtomicInteger()
                solver.dispatcher = object : Dispatcher() {
                    override fun dispatch(request: RecordedRequest): MockResponse {
                        if (request.method == "GET") return MockResponse.Builder().body("""{"protocol":"fenced-drain-close-v1"}""").build()
                        val payload = jacksonObjectMapper().readTree(request.body!!.utf8())
                        return when (payload["cmd"].asText()) {
                            "sessions.recovery.prepare" -> MockResponse.Builder().body("""{"protocol":"fenced-drain-close-v1","session":"123","generation":"$old","outcome":"prepared"}""").build()
                            "sessions.recovery.confirm" -> {
                                val body = jacksonObjectMapper().readTree("""{"protocol":"fenced-drain-close-v1","session":"123","generation":"$replacement","previousGeneration":"$old","outcome":"drained-closed"}""") as com.fasterxml.jackson.databind.node.ObjectNode
                                when (failure) {
                                    "numeric-generation" -> body.put("generation", java.math.BigInteger(replacement))
                                    "numeric-session" -> body.put("session",123)
                                    "numeric-previous" -> body.put("previousGeneration", java.math.BigInteger(old))
                                    "wrong-session" -> body.put("session","another-name")
                                    "same-generation" -> body.put("generation",old)
                                }
                                MockResponse.Builder().code(if (failure == "http-failure") 500 else 200).body(body.toString()).build()
                            }
                            else -> { solves.incrementAndGet(); MockResponse.Builder().code(500).body("ambiguous failure").build() }
                        }
                    }
                }
                repeat(3) { attempt ->
                    assertFailsWith<java.io.IOException>("$failure attempt $attempt admitted invalid recovery") {
                        client.newCall(Request.Builder().url(source.url("/request-$attempt")).build()).execute().close()
                    }
                }
                assertEquals(1,solves.get(),"$failure allowed a new browser solve")
            }
        }
    }

    @Test
    fun `slow confirmed recovery does not consume admitted solve drain budget`() = fixture("slow confirmation") { source, solver, client ->
        serverConfig.flareSolverrTimeout.value = 8
        val old = "a".repeat(32)
        val replacement = "b".repeat(32)
        val solves = java.util.concurrent.atomic.AtomicInteger()
        val thirdEntered = CountDownLatch(1)
        solver.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                if (request.method == "GET") return MockResponse.Builder().body("""{"protocol":"fenced-drain-close-v1"}""").build()
                val payload = jacksonObjectMapper().readTree(request.body!!.utf8())
                return when(payload["cmd"].asText()) {
                    "sessions.recovery.prepare" -> MockResponse.Builder().body("""{"protocol":"fenced-drain-close-v1","session":"slow confirmation","generation":"$old","outcome":"prepared"}""").build()
                    "sessions.recovery.confirm" -> { Thread.sleep(12000); MockResponse.Builder().body("""{"protocol":"fenced-drain-close-v1","session":"slow confirmation","generation":"$replacement","previousGeneration":"$old","outcome":"drained-closed"}""").build() }
                    else -> {
                        val attempt = solves.incrementAndGet()
                        if (attempt == 1) MockResponse.Builder().code(500).body("ambiguous failure").build()
                        else {
                            assertEquals(replacement, request.headers["X-Byparr-Session-Generation"])
                            if (attempt == 3) thirdEntered.countDown()
                            solution(payload["url"].asText()).newBuilder().bodyDelay(if (attempt == 2) 7 else 0,TimeUnit.SECONDS).build()
                        }
                    }
                }
            }
        }
        val pool = Executors.newFixedThreadPool(2)
        try {
            assertFailsWith<java.io.IOException> { client.newCall(Request.Builder().url(source.url("/first")).build()).execute().close() }
            val started = System.nanoTime()
            val second = pool.submit<String> { client.newCall(Request.Builder().url(source.url("/second")).build()).execute().use { it.body.string() } }
            assertFailsWith<ExecutionException> { second.get(20,TimeUnit.SECONDS) }
            val elapsed = TimeUnit.NANOSECONDS.toMillis(System.nanoTime()-started)
            assertTrue(elapsed in 17000..19500,"caller budget changed: $elapsed")
            val third = pool.submit<String> { client.newCall(Request.Builder().url(source.url("/third")).build()).execute().use { it.body.string() } }
            assertTrue(!thirdEntered.await(200,TimeUnit.MILLISECONDS),"replacement reused before solve body drained")
            assertEquals("healthy result",third.get(3,TimeUnit.SECONDS))
            assertEquals(3,solves.get())
        } finally { pool.shutdownNow();pool.awaitTermination(5,TimeUnit.SECONDS) }
    }

    @Test
    fun `discovery-only failure remains retryable without resetting or admitting a solve`() = fixture("discovery retry") { source, solver, client ->
        val discoveries = java.util.concurrent.atomic.AtomicInteger()
        val prepares = java.util.concurrent.atomic.AtomicInteger()
        val solves = java.util.concurrent.atomic.AtomicInteger()
        val generation = "c".repeat(32)
        solver.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                if (request.method == "GET") return if (discoveries.incrementAndGet() == 1) MockResponse.Builder().code(500).body("temporary discovery outage").build()
                    else MockResponse.Builder().body("""{"protocol":"fenced-drain-close-v1"}""").build()
                val payload = jacksonObjectMapper().readTree(request.body!!.utf8())
                return when(payload["cmd"].asText()) {
                    "sessions.recovery.prepare" -> { prepares.incrementAndGet(); MockResponse.Builder().body("""{"protocol":"fenced-drain-close-v1","session":"discovery retry","generation":"$generation","outcome":"prepared"}""").build() }
                    "request.get" -> { solves.incrementAndGet(); assertEquals(generation,request.headers["X-Byparr-Session-Generation"]); solution(payload["url"].asText()) }
                    else -> throw AssertionError("discovery-only failure attempted session reset")
                }
            }
        }
        assertFailsWith<java.io.IOException> { client.newCall(Request.Builder().url(source.url("/first")).build()).execute().close() }
        assertEquals(0,solves.get())
        assertEquals(0,prepares.get())
        client.newCall(Request.Builder().url(source.url("/retry")).build()).execute().use { assertEquals("healthy result",it.body.string()) }
        assertEquals(2,discoveries.get())
        assertEquals(1,prepares.get())
        assertEquals(1,solves.get())
    }

    @Test
    fun `ambiguous prepare acknowledgement never permits untagged fallback`() = fixture("prepare failure") { source, solver, client ->
        val controls = java.util.concurrent.atomic.AtomicInteger()
        val solves = java.util.concurrent.atomic.AtomicInteger()
        solver.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                if (request.method == "GET") return MockResponse.Builder().body("""{"protocol":"fenced-drain-close-v1"}""").build()
                val payload = jacksonObjectMapper().readTree(request.body!!.utf8())
                if (payload["cmd"].asText() == "sessions.recovery.prepare") { controls.incrementAndGet(); return MockResponse.Builder().body("lost prepare acknowledgment").build() }
                solves.incrementAndGet()
                return solution(payload["url"].asText())
            }
        }
        repeat(2) { assertFailsWith<java.io.IOException> { client.newCall(Request.Builder().url(source.url("/attempt-$it")).build()).execute().close() } }
        assertEquals(1,controls.get(),"ambiguous mutating control was retried without a known generation")
        assertEquals(0,solves.get())
    }

    private fun MockWebServer.enqueueSolve(response: MockResponse) {
        if (serverConfig.flareSolverrSessionName.value.isNotEmpty()) enqueue(MockResponse.Builder().code(404).build())
        enqueue(response)
    }

    private fun MockWebServer.takeSolveRequest(timeout: Long, unit: TimeUnit): RecordedRequest? {
        val deadline = System.nanoTime() + unit.toNanos(timeout)
        while (true) {
            val remaining = deadline - System.nanoTime()
            if (remaining <= 0) return null
            val request = takeRequest(remaining, TimeUnit.NANOSECONDS) ?: return null
            if (request.method != "GET") return request
        }
    }

    private fun solution(url: String): MockResponse = MockResponse.Builder().body(solutionBody(url)).build()

    private fun solutionBody(url: String): String = jacksonObjectMapper().writeValueAsString(mapOf(
        "solution" to mapOf("url" to url, "status" to 200, "cookies" to emptyList<String>(), "userAgent" to "Fixture solver UA"),
        "status" to "ok", "message" to "Challenge solved", "startTimestamp" to 1, "endTimestamp" to 2, "version" to "fixture",
    ))

    private fun fixture(session: String, run: (MockWebServer, MockWebServer, OkHttpClient) -> Unit) {
        EngineRuntimeIntegrationTestSetup.ensureReady()
        val enabled = serverConfig.flareSolverrEnabled.value
        val url = serverConfig.flareSolverrUrl.value
        val oldSession = serverConfig.flareSolverrSessionName.value
        val timeout = serverConfig.flareSolverrTimeout.value
        val fallback = serverConfig.flareSolverrAsResponseFallback.value
        MockWebServer().use { source -> MockWebServer().use { solver ->
            source.dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse = if (request.headers["User-Agent"] == "Fixture solver UA") {
                    MockResponse.Builder().body("healthy result").build()
                } else MockResponse.Builder().code(403).addHeader("Server", "cloudflare").build()
            }
            source.start(); solver.start()
            serverConfig.flareSolverrEnabled.value = true
            serverConfig.flareSolverrUrl.value = solver.url("/").toString()
            serverConfig.flareSolverrSessionName.value = session
            serverConfig.flareSolverrTimeout.value = 3
            serverConfig.flareSolverrAsResponseFallback.value = false
            try { run(source, solver, OkHttpClient.Builder().addInterceptor(CloudflareInterceptor {}).build()) }
            finally {
                serverConfig.flareSolverrEnabled.value = enabled
                serverConfig.flareSolverrUrl.value = url
                serverConfig.flareSolverrSessionName.value = oldSession
                serverConfig.flareSolverrTimeout.value = timeout
                serverConfig.flareSolverrAsResponseFallback.value = fallback
            }
        } }
    }

}
