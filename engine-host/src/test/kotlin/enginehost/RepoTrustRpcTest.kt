package enginehost

import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.sun.net.httpserver.HttpServer
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertEquals

class RepoTrustRpcTest {
    @Test
    fun `repository trust rotation is an explicit persisted RPC action`() {
        val root = Files.createTempDirectory("repo-trust-rpc")
        val loader = ExtensionLoader(root.toFile())
        val manager = ExtensionManager(loader, root.toFile())
        val repoUrl = "https://repo.example.test/index.json"
        val fingerprint = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
        manager.setRepos(listOf(repoUrl))
        val server = RpcServer(loader, manager, port = 0)
        server.start()
        try {
            val port = boundPort(server)
            val request =
                HttpRequest
                    .newBuilder(URI("http://127.0.0.1:$port/repos/trust"))
                    .header("Content-Type", "application/json")
                    .PUT(HttpRequest.BodyPublishers.ofString("""{"repoUrl":"$repoUrl","signerFingerprint":"$fingerprint"}"""))
                    .build()
            val response = HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.ofString())

            assertEquals(200, response.statusCode())
            assertEquals(fingerprint, jacksonObjectMapper().readTree(response.body())["signerFingerprint"].textValue())
        } finally {
            server.stop()
            manager.close()
        }

        val reloaded = ExtensionManager(ExtensionLoader(root.toFile()), root.toFile())
        assertEquals(fingerprint, reloaded.getRepoTrust().getValue(repoUrl))
        reloaded.close()
    }

    private fun boundPort(rpc: RpcServer): Int {
        val field = RpcServer::class.java.getDeclaredField("server").apply { isAccessible = true }
        return (field.get(rpc) as HttpServer).address.port
    }
}
