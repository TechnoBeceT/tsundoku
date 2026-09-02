package enginehost

import androidx.preference.MultiSelectListPreference
import androidx.preference.PreferenceScreen
import androidx.preference.SwitchPreferenceCompat
import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.sun.net.httpserver.HttpServer
import eu.kanade.tachiyomi.source.ConfigurableSource
import eu.kanade.tachiyomi.source.Source
import eu.kanade.tachiyomi.source.SourceFactory
import eu.kanade.tachiyomi.source.model.FilterList
import eu.kanade.tachiyomi.source.model.MangasPage
import eu.kanade.tachiyomi.source.model.Page
import eu.kanade.tachiyomi.source.model.SChapter
import eu.kanade.tachiyomi.source.model.SManga
import eu.kanade.tachiyomi.source.model.SMangaUpdate
import eu.kanade.tachiyomi.source.sourcePreferences
import java.io.ByteArrayInputStream
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.file.Files
import java.nio.file.Path
import java.util.Properties
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream
import kotlin.io.path.readBytes
import kotlin.io.path.writeBytes
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertSame
import kotlin.test.assertTrue
import xyz.nulldev.ts.config.ApplicationRootDir

class PreferenceTransactionRpcTest {
    private val mapper = jacksonObjectMapper()

    @Test
    fun `preference PUT returns the committed source after an accepted ID change`() {
        EngineRuntimeIntegrationTestSetup.ensureReady()
        PreferenceTransactionFixture().use { fixture ->
            val response = fixture.put("""{"change_id":true}""")

            assertEquals(200, response.statusCode(), response.body())
            val titles = mapper.readTree(response.body())["preferences"].map { it["title"].textValue() }
            assertTrue("Change source ID (${PreferenceTransactionProbe.newSourceId})" in titles)
            assertEquals(null, fixture.loader.source(PreferenceTransactionProbe.oldSourceId))
            assertEquals(PreferenceTransactionProbe.newSourceId, fixture.loader.source(PreferenceTransactionProbe.newSourceId)?.id)
            assertEquals(
                listOf(PreferenceTransactionProbe.newSourceId),
                fixture.manager.recordForSource(PreferenceTransactionProbe.newSourceId)?.sourceIds,
            )
            assertEquals(
                listOf(PreferenceTransactionProbe.newSourceId),
                mapper.readTree(fixture.manifest.readBytes())[0]["sourceIds"].map { it.longValue() },
            )
            assertTrue(fixture.loader.hasCachedLoader(fixture.jar))
            assertTrue(fixture.preferences.getBoolean("change_id", false))
        }
    }

    @Test
    fun `accepted preference reload commits and describes a multi-select update`() {
        EngineRuntimeIntegrationTestSetup.ensureReady()
        PreferenceTransactionFixture().use { fixture ->
            val response = fixture.put("""{"selected_groups":["beta","gamma"]}""")

            assertEquals(200, response.statusCode(), response.body())
            assertEquals(setOf("beta", "gamma"), fixture.preferences.getStringSet("selected_groups", emptySet()))
            val described =
                mapper.readTree(response.body())["preferences"]
                    .single { it["key"].textValue() == "selected_groups" }
            assertEquals(setOf("beta", "gamma"), described["currentValue"].map { it.textValue() }.toSet())
            assertSetEncoding(fixture.preferencePropertyKeys())
            assertFalse(fixture.preferenceBytesBefore.contentEquals(fixture.preferenceFile.readBytes()))
            assertEquals(PreferenceTransactionProbe.oldSourceId, fixture.loader.source(PreferenceTransactionProbe.oldSourceId)?.id)
            assertTrue(fixture.loader.hasCachedLoader(fixture.jar))
        }
    }

    @Test
    fun `rejected preference reload restores multi-select bytes and the old generation`() {
        EngineRuntimeIntegrationTestSetup.ensureReady()
        PreferenceTransactionProbe.replacementSourceId = PreferenceTransactionProbe.foreignSourceId
        PreferenceTransactionFixture(includeForeignOwner = true).use { fixture ->
            assertSetEncoding(fixture.preferencePropertyKeysBefore)

            val response = fixture.put("""{"selected_groups":["gamma"],"change_id":true}""")

            assertEquals(400, response.statusCode(), response.body())
            assertFalse(fixture.preferences.getBoolean("change_id", true))
            assertEquals(setOf("alpha", "beta"), fixture.preferences.getStringSet("selected_groups", emptySet()))
            assertContentEquals(fixture.preferenceBytesBefore, fixture.preferenceFile.readBytes())
            assertSetEncoding(fixture.preferencePropertyKeys())
            assertContentEquals(fixture.manifestBytesBefore, fixture.manifest.readBytes())
            assertSame(fixture.oldSource, fixture.loader.source(PreferenceTransactionProbe.oldSourceId))
            assertSame(fixture.foreignSource, fixture.loader.source(PreferenceTransactionProbe.foreignSourceId))
            assertTrue(fixture.loader.hasCachedLoader(fixture.jar), "the previous generation's loader was evicted")
        }
    }

    @Test
    fun `response description failure restores multi-select bytes before generation publication`() {
        EngineRuntimeIntegrationTestSetup.ensureReady()
        PreferenceTransactionFixture().use { fixture ->
            assertSetEncoding(fixture.preferencePropertyKeysBefore)

            val response = fixture.put("""{"selected_groups":["gamma"],"fail_description":true}""")

            assertEquals(502, response.statusCode(), response.body())
            assertFalse(fixture.preferences.getBoolean("fail_description", true))
            assertEquals(setOf("alpha", "beta"), fixture.preferences.getStringSet("selected_groups", emptySet()))
            assertContentEquals(fixture.preferenceBytesBefore, fixture.preferenceFile.readBytes())
            assertSetEncoding(fixture.preferencePropertyKeys())
            assertContentEquals(fixture.manifestBytesBefore, fixture.manifest.readBytes())
            assertSame(fixture.oldSource, fixture.loader.source(PreferenceTransactionProbe.oldSourceId))
            assertTrue(fixture.loader.hasCachedLoader(fixture.jar), "the previous generation's loader was evicted")
        }
    }

    private fun assertSetEncoding(keys: Set<String>) {
        assertTrue("selected_groups.0" in keys, keys.toString())
        assertTrue("selected_groups.1" in keys, keys.toString())
        assertTrue("selected_groups.size" in keys, keys.toString())
    }

    private class PreferenceTransactionFixture(
        includeForeignOwner: Boolean = false,
    ) : AutoCloseable {
        private val mapper = jacksonObjectMapper()
        private val root = Files.createTempDirectory("preference-transaction-rpc")
        val jar: Path = root.resolve("target.jar").also { writeClassJar(it, PreferenceTransactionFactory::class.java) }
        val manifest: Path = root.resolve("installed.json")
        val preferenceFile: Path =
            Path.of(ApplicationRootDir, "settings", "source_${PreferenceTransactionProbe.oldSourceId}.xml")
        val preferences = sourcePreferences("source_${PreferenceTransactionProbe.oldSourceId}")
        val loader = ExtensionLoader(root.toFile())
        val oldSource: Source
        val foreignSource: Source?
        val manager: ExtensionManager
        private val server: RpcServer
        private val client = HttpClient.newHttpClient()
        private val port: Int
        val preferenceBytesBefore: ByteArray
        val preferencePropertyKeysBefore: Set<String>
        val manifestBytesBefore: ByteArray

        init {
            preferences.edit().clear().commit()
            preferences.edit()
                .putBoolean("change_id", false)
                .putBoolean("fail_description", false)
                .putStringSet("selected_groups", mutableSetOf("alpha", "beta"))
                .putString("sentinel", "keep")
                .commit()
            preferenceBytesBefore = preferenceFile.readBytes()
            preferencePropertyKeysBefore = propertyKeys(preferenceBytesBefore)

            root.resolve("target.apk").writeBytes("apk".toByteArray())
            oldSource = loader.reinstantiate(jar.toString(), PreferenceTransactionFactory::class.java.name).single()
            val targetRecord = installedRecord("example.preference", "target.apk", oldSource, PreferenceTransactionFactory::class.java.name)
            foreignSource = PreferenceTransactionSource(PreferenceTransactionProbe.foreignSourceId, failOnDescription = false)
                .takeIf { includeForeignOwner }
            val installed = linkedMapOf(targetRecord.pkgName to targetRecord)
            val sources = linkedMapOf(oldSource.id to oldSource)
            foreignSource?.let { source ->
                root.resolve("foreign.apk").writeBytes("apk".toByteArray())
                val record = installedRecord("foreign.preference", "foreign.apk", source, "foreign.PreferenceSource")
                installed[record.pkgName] = record
                sources[source.id] = source
            }
            manifest.writeBytes(mapper.writeValueAsBytes(installed.values.toList()))
            manifestBytesBefore = manifest.readBytes()
            loader.publishRegistry(loader.prepareRegistry(sources, installed))
            manager = ExtensionManager(loader, root.toFile())
            server = RpcServer(loader, manager, port = 0)
            server.start()
            port = boundPort(server)
        }

        fun put(body: String): HttpResponse<String> =
            client.send(
                HttpRequest.newBuilder(URI("http://127.0.0.1:$port/sources/${PreferenceTransactionProbe.oldSourceId}/preferences"))
                    .header("Content-Type", "application/json")
                    .PUT(HttpRequest.BodyPublishers.ofString(body))
                    .build(),
                HttpResponse.BodyHandlers.ofString(),
            )

        fun preferencePropertyKeys(): Set<String> = propertyKeys(preferenceFile.readBytes())

        override fun close() {
            server.stop()
            manager.close()
            loader.evictAndClose(jar)
            preferences.edit().clear().commit()
            sourcePreferences("source_${PreferenceTransactionProbe.newSourceId}").edit().clear().commit()
            sourcePreferences("source_${PreferenceTransactionProbe.foreignSourceId}").edit().clear().commit()
            PreferenceTransactionProbe.replacementSourceId = null
        }

        private fun installedRecord(
            pkgName: String,
            apkFileName: String,
            source: Source,
            mainClass: String,
        ): InstalledExtension =
            InstalledExtension(
                pkgName = pkgName,
                name = source.name,
                versionName = "1.2.1",
                versionCode = 1,
                lang = source.lang,
                apkFileName = apkFileName,
                mainClass = mainClass,
                isNsfw = false,
                iconUrl = null,
                repoUrl = null,
                sourceIds = listOf(source.id),
                sources = listOf(ExtensionSourceDto(source.id, source.name, source.lang)),
            )

        private fun boundPort(rpc: RpcServer): Int {
            val field = RpcServer::class.java.getDeclaredField("server").apply { isAccessible = true }
            return (field.get(rpc) as HttpServer).address.port
        }

        private fun writeClassJar(
            target: Path,
            sourceClass: Class<*>,
        ) {
            val entryName = sourceClass.name.replace('.', '/') + ".class"
            val classBytes = requireNotNull(sourceClass.classLoader.getResourceAsStream(entryName)) { "missing $entryName" }
                .use { it.readBytes() }
            ZipOutputStream(Files.newOutputStream(target)).use { output ->
                output.putNextEntry(ZipEntry(entryName))
                output.write(classBytes)
                output.closeEntry()
            }
        }

        private fun propertyKeys(bytes: ByteArray): Set<String> =
            Properties().run {
                ByteArrayInputStream(bytes).use { loadFromXML(it) }
                stringPropertyNames()
            }
    }
}

internal object PreferenceTransactionProbe {
    const val oldSourceId = 83_101L
    const val newSourceId = 83_102L
    const val foreignSourceId = 83_103L

    @Volatile
    var replacementSourceId: Long? = null
}

internal class PreferenceTransactionFactory : SourceFactory {
    override fun createSources(): List<Source> {
        val preferences = sourcePreferences("source_${PreferenceTransactionProbe.oldSourceId}")
        val changeId = preferences.getBoolean("change_id", false)
        val sourceId =
            if (changeId) {
                PreferenceTransactionProbe.replacementSourceId ?: PreferenceTransactionProbe.newSourceId
            } else {
                PreferenceTransactionProbe.oldSourceId
            }
        return listOf(
            PreferenceTransactionSource(
                id = sourceId,
                failOnDescription = preferences.getBoolean("fail_description", false),
            ),
        )
    }
}

internal class PreferenceTransactionSource(
    override val id: Long,
    private val failOnDescription: Boolean,
) : Source, ConfigurableSource {
    override val name: String = "Preference transaction source $id"
    override val lang: String = "en"
    override val supportsLatest: Boolean = false

    override fun setupPreferenceScreen(screen: PreferenceScreen) {
        check(!failOnDescription) { "injected preference description failure" }
        screen.addPreference(
            SwitchPreferenceCompat(screen.context).apply {
                key = "change_id"
                title = "Change source ID ($id)"
                defaultValue = false
            },
        )
        screen.addPreference(
            MultiSelectListPreference(screen.context).apply {
                key = "selected_groups"
                title = "Selected groups ($id)"
                entries = arrayOf("Alpha", "Beta", "Gamma")
                entryValues = arrayOf("alpha", "beta", "gamma")
                defaultValue = emptySet<String>()
            },
        )
        screen.addPreference(
            SwitchPreferenceCompat(screen.context).apply {
                key = "fail_description"
                title = "Fail description ($id)"
                defaultValue = false
            },
        )
    }

    override suspend fun getMangaUpdate(
        manga: SManga,
        chapters: List<SChapter>,
        fetchDetails: Boolean,
        fetchChapters: Boolean,
    ): SMangaUpdate = error("unused")

    override suspend fun getPopularManga(page: Int): MangasPage = error("unused")

    override suspend fun getLatestUpdates(page: Int): MangasPage = error("unused")

    override suspend fun getSearchManga(
        page: Int,
        query: String,
        filters: FilterList,
    ): MangasPage = error("unused")

    override suspend fun getPageList(chapter: SChapter): List<Page> = error("unused")
}
