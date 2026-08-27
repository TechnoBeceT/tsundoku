package enginehost

import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import eu.kanade.tachiyomi.source.Source
import eu.kanade.tachiyomi.source.model.FilterList
import eu.kanade.tachiyomi.source.model.MangasPage
import eu.kanade.tachiyomi.source.model.Page
import eu.kanade.tachiyomi.source.model.SChapter
import eu.kanade.tachiyomi.source.model.SManga
import eu.kanade.tachiyomi.source.model.SMangaUpdate
import java.nio.file.Files
import java.nio.file.Path
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.io.path.listDirectoryEntries
import kotlin.io.path.readBytes
import kotlin.io.path.writeBytes
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFails
import kotlin.test.assertSame
import kotlin.test.assertTrue

class ExtensionManagerTest {
    @Test
    fun `blocked repository transfer does not hold the extension mutation lock`() {
        val root = Files.createTempDirectory("extension-manager-repo-lock")
        val transferStarted = CountDownLatch(1)
        val releaseTransfer = CountDownLatch(1)
        val downloader =
            object : ExtensionDownloadClient {
                override fun downloadRepoIndex(url: String): ByteArray {
                    transferStarted.countDown()
                    releaseTransfer.await()
                    return "[]".toByteArray()
                }

                override fun downloadApk(
                    url: String,
                    targetDir: Path,
                ): Path = error("unused")
            }
        val loader = ExtensionLoader(root.toFile())
        val manager = ExtensionManager(loader, root.toFile(), downloader)
        manager.setRepos(listOf("https://repo.example.test/index.json"))
        val executor = Executors.newFixedThreadPool(2)
        try {
            val listing = executor.submit<List<ExtensionDto>> { manager.list() }
            assertTrue(transferStarted.await(5, TimeUnit.SECONDS), "repository transfer did not start")

            val preferenceMutation = executor.submit<String> { manager.underLock { "mutated" } }

            assertEquals("mutated", preferenceMutation.get(250, TimeUnit.MILLISECONDS))
            releaseTransfer.countDown()
            assertEquals(emptyList(), listing.get(5, TimeUnit.SECONDS))
        } finally {
            releaseTransfer.countDown()
            executor.shutdownNow()
        }
    }

    @Test
    fun `blocked APK transfer does not hold the extension mutation lock`() {
        val root = Files.createTempDirectory("extension-manager-apk-lock")
        val transferStarted = CountDownLatch(1)
        val releaseTransfer = CountDownLatch(1)
        val downloader =
            object : ExtensionDownloadClient {
                override fun downloadRepoIndex(url: String): ByteArray = error("unused")

                override fun downloadApk(
                    url: String,
                    targetDir: Path,
                ): Path {
                    transferStarted.countDown()
                    releaseTransfer.await()
                    return Files.createTempFile(targetDir, ".test-download-", ".apk.tmp").also {
                        it.writeBytes("not an apk".toByteArray())
                    }
                }
            }
        val loader = ExtensionLoader(root.toFile())
        val manager = ExtensionManager(loader, root.toFile(), downloader)
        val executor = Executors.newFixedThreadPool(2)
        try {
            val install = executor.submit<List<ExtensionDto>> { manager.install(apkUrl = "https://cdn.example.test/new.apk") }
            assertTrue(transferStarted.await(5, TimeUnit.SECONDS), "APK transfer did not start")

            val preferenceMutation = executor.submit<String> { manager.underLock { "mutated" } }

            assertEquals("mutated", preferenceMutation.get(250, TimeUnit.MILLISECONDS))
            releaseTransfer.countDown()
            assertFails { install.get(5, TimeUnit.SECONDS) }
        } finally {
            releaseTransfer.countDown()
            executor.shutdownNow()
        }
    }

    @Test
    fun `failed prepared-file apply preserves installed APK manifest and source registry`() {
        val root = Files.createTempDirectory("extension-manager-rollback")
        val oldApk = root.resolve("installed.apk")
        oldApk.writeBytes("working installed apk".toByteArray())
        val installed = installedRecord(apkFileName = oldApk.fileName.toString(), sourceIds = listOf(7L))
        val manifest = root.resolve("installed.json")
        manifest.writeBytes(jacksonObjectMapper().writeValueAsBytes(listOf(installed)))
        val manifestBefore = manifest.readBytes()
        val loader = ExtensionLoader(root.toFile())
        val source = TestSource(7L)
        injectSource(loader, source)
        val downloader =
            object : ExtensionDownloadClient {
                override fun downloadRepoIndex(url: String): ByteArray = error("unused")

                override fun downloadApk(
                    url: String,
                    targetDir: Path,
                ): Path =
                    Files.createTempFile(targetDir, ".test-download-", ".apk.tmp").also {
                        it.writeBytes("complete replacement apk".toByteArray())
                    }
            }
        val preparer =
            ExtensionPreparer { apk ->
                PreparedExtension(
                    pkgName = installed.pkgName,
                    versionName = "1.2.4",
                    versionCode = 2,
                    mainClass = "example.Extension",
                    apkFile = apk,
                    jarFile = root.resolve("missing-prepared.jar"),
                )
            }
        val manager = ExtensionManager(loader, root.toFile(), downloader, preparer)

        assertFails { manager.install(apkUrl = "https://cdn.example.test/installed.apk") }

        assertContentEquals("working installed apk".toByteArray(), oldApk.readBytes())
        assertContentEquals(manifestBefore, manifest.readBytes())
        assertSame(source, loader.source(7L))
        assertEquals(installed, manager.recordForSource(7L))
        assertEquals(
            listOf("installed.apk", "installed.json"),
            root.listDirectoryEntries().map { it.fileName.toString() }.sorted(),
            "failed apply leaked prepared or partially-installed files",
        )
    }

    private fun installedRecord(
        apkFileName: String,
        sourceIds: List<Long>,
    ) =
        InstalledExtension(
            pkgName = "example.extension",
            name = "Example",
            versionName = "1.2.3",
            versionCode = 1,
            lang = "en",
            apkFileName = apkFileName,
            mainClass = "example.Extension",
            isNsfw = false,
            iconUrl = null,
            repoUrl = null,
            sourceIds = sourceIds,
            sources = sourceIds.map { ExtensionSourceDto(it, "Example", "en") },
        )

    private fun injectSource(
        loader: ExtensionLoader,
        source: Source,
    ) {
        val field = ExtensionLoader::class.java.getDeclaredField("sources").apply { isAccessible = true }
        @Suppress("UNCHECKED_CAST")
        val registry = field.get(loader) as MutableMap<Long, Source>
        registry[source.id] = source
    }
}

private class TestSource(
    override val id: Long,
) : Source {
    override val name: String = "Test Source"
    override val lang: String = "en"
    override val supportsLatest: Boolean = false

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
