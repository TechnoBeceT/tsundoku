package enginehost

import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.sun.net.httpserver.HttpServer
import java.net.InetSocketAddress
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class RepoTrustRpcTest {
    @Test
    fun `repository trust RPC is loopback authenticated and persists rotation`() {
        val root = Files.createTempDirectory("repo-trust-rpc")
        val loader = ExtensionLoader(root.toFile())
        val manager = ExtensionManager(loader, root.toFile())
        val repoUrl = "https://repo.example.test/index.json"
        val fingerprint = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
        manager.setRepos(listOf(repoUrl))
        val server = RpcServer(loader, manager, port = 0, controlToken = CONTROL_TOKEN)
        server.start()
        try {
            val address = boundAddress(server)
            assertTrue(address.address.isLoopbackAddress)
            val body = """{"repoUrl":"$repoUrl","signerFingerprint":"$fingerprint"}"""

            val unauthorized = request(address.port, "PUT", body)
            assertEquals(401, unauthorized.statusCode())
            assertEquals("unauthorized", jacksonObjectMapper().readTree(unauthorized.body())["error"].textValue())
            assertFalse(repoUrl in manager.getRepoTrust())

            val wrongToken = request(address.port, "PUT", body, "wrong-token")
            assertEquals(401, wrongToken.statusCode())
            assertFalse(repoUrl in manager.getRepoTrust())

            val updated = request(address.port, "PUT", body, CONTROL_TOKEN)
            assertEquals(200, updated.statusCode())
            assertEquals(fingerprint, jacksonObjectMapper().readTree(updated.body())["trust"][repoUrl].textValue())

            val readBack = request(address.port, "GET", token = CONTROL_TOKEN)
            assertEquals(200, readBack.statusCode())
            assertEquals(fingerprint, jacksonObjectMapper().readTree(readBack.body())["trust"][repoUrl].textValue())
        } finally {
            server.stop()
            manager.close()
        }

        val reloaded = ExtensionManager(ExtensionLoader(root.toFile()), root.toFile())
        assertEquals(fingerprint, reloaded.getRepoTrust().getValue(repoUrl))
        reloaded.close()
    }

    private fun request(
        port: Int,
        method: String,
        body: String? = null,
        token: String? = null,
    ): HttpResponse<String> {
        val builder = HttpRequest.newBuilder(URI("http://127.0.0.1:$port/repos/trust"))
        token?.let { builder.header("Authorization", "Bearer $it") }
        if (body == null) {
            builder.method(method, HttpRequest.BodyPublishers.noBody())
        } else {
            builder.header("Content-Type", "application/json").method(method, HttpRequest.BodyPublishers.ofString(body))
        }
        return HttpClient.newHttpClient().send(builder.build(), HttpResponse.BodyHandlers.ofString())
    }

    private fun boundAddress(rpc: RpcServer): InetSocketAddress {
        val field = RpcServer::class.java.getDeclaredField("server").apply { isAccessible = true }
        return (field.get(rpc) as HttpServer).address
    }

    private companion object {
        const val CONTROL_TOKEN = "repo-trust-control-token"
    }
}
