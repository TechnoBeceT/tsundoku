package enginehost

import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.sun.net.httpserver.HttpServer
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.file.Files
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.system.measureTimeMillis
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertTrue

class EngineStatusTest {
    private val client = HttpClient.newHttpClient()
    private val mapper = jacksonObjectMapper()

    @Test
    fun `status accepts GET only`() {
        val workDir = Files.createTempDirectory("engine-status-method").toFile()
        val loader = ExtensionLoader(workDir)
        val server = RpcServer(loader, ExtensionManager(loader, workDir), port = 0)
        try {
            server.start()
            val request =
                HttpRequest.newBuilder(URI("http://localhost:${boundPort(server)}/status"))
                    .POST(HttpRequest.BodyPublishers.noBody())
                    .build()

            val response = client.send(request, HttpResponse.BodyHandlers.ofString())

            assertEquals(405, response.statusCode())
        } finally {
            server.stop()
        }
    }

    @Test
    fun `status stays bounded and prompt while source and extension domains are saturated`() {
        val executors = RpcExecutors()
        val sourceRelease = CountDownLatch(1)
        val sourceEntered = CountDownLatch(8)
        val extensionRelease = CountDownLatch(1)
        val extensionEntered = CountDownLatch(1)
        val extensionNetworkRelease = CountDownLatch(1)
        val extensionNetworkEntered = CountDownLatch(1)
        val workDir = Files.createTempDirectory("engine-status").toFile()
        val loader = ExtensionLoader(workDir)
        val server = RpcServer(loader, ExtensionManager(loader, workDir), port = 0, executors = executors)
        try {
            repeat(8) { index ->
                assertIs<Submission.Accepted<Unit>>(
                    executors.sourceScheduler.submit(1L + index / 2) {
                        sourceEntered.countDown()
                        sourceRelease.await()
                    },
                )
            }
            assertTrue(sourceEntered.await(5, TimeUnit.SECONDS), "all physical source workers did not start")

            val fixtureUrl = "https://private.invalid/manga/secret"
            val fixtureHeader = "X-Private-Header"
            val fixtureToken = "status-must-not-leak-this-token"
            repeat(128) { index ->
                assertIs<Submission.Accepted<Unit>>(
                    executors.sourceScheduler.submit(1_000L + index) {
                        check(fixtureUrl.isNotEmpty() && fixtureHeader.isNotEmpty() && fixtureToken.isNotEmpty())
                    },
                )
            }
            assertIs<Submission.Rejected>(executors.sourceScheduler.submit(Long.MAX_VALUE) {})

            executors.extensionExecutor.execute {
                extensionEntered.countDown()
                extensionRelease.await()
            }
            assertTrue(extensionEntered.await(5, TimeUnit.SECONDS), "extension worker did not start")
            executors.extensionExecutor.execute { extensionRelease.await() }
            awaitExtensionQueue(executors.extensionExecutor, 1)
            executors.extensionNetworkExecutor.execute {
                extensionNetworkEntered.countDown()
                extensionNetworkRelease.await()
            }
            assertTrue(extensionNetworkEntered.await(5, TimeUnit.SECONDS), "extension network worker did not start")
            executors.extensionNetworkExecutor.execute { extensionNetworkRelease.await() }
            awaitExtensionQueue(executors.extensionNetworkExecutor, 1)

            server.start()
            val request =
                HttpRequest.newBuilder(URI("http://localhost:${boundPort(server)}/status"))
                    .timeout(java.time.Duration.ofMillis(500))
                    .GET()
                    .build()
            lateinit var response: HttpResponse<String>
            val elapsed = measureTimeMillis { response = client.send(request, HttpResponse.BodyHandlers.ofString()) }

            assertEquals(200, response.statusCode())
            assertTrue(elapsed < 500, "status took ${elapsed}ms behind saturated domain work")
            assertTrue(response.body().toByteArray().size < 32 * 1024, "status exceeded the 32 KiB evidence bound")
            assertFalse(response.body().contains(fixtureUrl))
            assertFalse(response.body().contains(fixtureHeader))
            assertFalse(response.body().contains(fixtureToken))

            val status = mapper.readTree(response.body())
            assertEquals(
                setOf(
                    "ready",
                    "source_workers",
                    "per_source_limit",
                    "queued",
                    "running",
                    "completion_sequence",
                    "oldest_running_millis",
                    "completed",
                    "cancelled",
                    "timed_out",
                    "rejected",
                    "busiest_sources",
                    "extension_running",
                    "extension_queued",
                ),
                status.fieldNames().asSequence().toSet(),
            )
            assertEquals(true, status["ready"].booleanValue())
            assertEquals(8, status["source_workers"].intValue())
            assertEquals(2, status["per_source_limit"].intValue())
            assertEquals(128, status["queued"].intValue())
            assertEquals(8, status["running"].intValue())
            assertEquals(0, status["completion_sequence"].longValue())
            assertEquals(0, status["completed"].longValue())
            assertEquals(0, status["cancelled"].longValue())
            assertEquals(0, status["timed_out"].longValue())
            assertEquals(1, status["rejected"].longValue())
            assertEquals(true, status["extension_running"].booleanValue())
            assertEquals(2, status["extension_queued"].intValue())
            assertEquals(10, status["busiest_sources"].size())
            assertEquals(
                listOf(1L, 2L, 3L, 4L, 1_000L, 1_001L, 1_002L, 1_003L, 1_004L, 1_005L),
                status["busiest_sources"].map { it["source_id"].longValue() },
            )

            val stopElapsed = measureTimeMillis { server.stop() }
            assertTrue(stopElapsed < 500, "shutdown waited ${stopElapsed}ms behind domain work")
        } finally {
            sourceRelease.countDown()
            extensionRelease.countDown()
            extensionNetworkRelease.countDown()
            server.stop()
            executors.close()
        }
    }

    private fun awaitExtensionQueue(
        executor: java.util.concurrent.ExecutorService,
        expected: Int,
    ) {
        val pool = executor as java.util.concurrent.ThreadPoolExecutor
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5)
        while (pool.queue.size != expected && System.nanoTime() < deadline) Thread.sleep(5)
        assertEquals(expected, pool.queue.size, "extension queue did not reach expected size")
    }

    private fun boundPort(rpc: RpcServer): Int {
        val field = RpcServer::class.java.getDeclaredField("server").apply { isAccessible = true }
        return (field.get(rpc) as HttpServer).address.port
    }
}
