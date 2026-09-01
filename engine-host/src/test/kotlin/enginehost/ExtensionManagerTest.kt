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
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream
import kotlin.io.path.listDirectoryEntries
import kotlin.io.path.readBytes
import kotlin.io.path.writeBytes
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFails
import kotlin.test.assertFailsWith
import kotlin.test.assertSame
import kotlin.test.assertTrue

class ExtensionManagerTest {
    @Test
    fun `direct URL install rejects an invalid APK signature without mutation`() {
        val unsigned = SignedApkFixture()
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateApkBytes = unsigned.unsignedApk.readBytes(),
                signatureVerifier = ApkSignerVerifier,
                useRealPreparer = true,
            )

        val failure = assertFailsWith<IllegalArgumentException> {
            fixture.manager.install(apkUrl = "https://cdn.example.test/replacement.apk")
        }

        assertTrue(failure.message.orEmpty().contains("signature verification failed"))
        fixture.assertUnchanged()
    }

    @Test
    fun `repository update rejects a signer outside repository trust without mutation`() {
        val installedSigner = SignedApkFixture()
        val candidateSigner = SignedApkFixture()
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = CandidateOwnedSource::class.java,
                oldApkBytes = installedSigner.signedApk.readBytes(),
                candidateApkBytes = candidateSigner.signedApk.readBytes(),
                repositorySigningKey = installedSigner.fingerprint,
                candidateSignerFingerprints = setOf(candidateSigner.fingerprint),
                signatureVerifier = ApkSignerVerifier,
            )

        val failure = assertFailsWith<IllegalArgumentException> { fixture.manager.update(TARGET_PACKAGE) }

        assertTrue(failure.message.orEmpty().contains("is not trusted by the repository"))
        fixture.assertUnchanged()
    }

    @Test
    fun `repository update rejects installed signer continuity mismatch without mutation`() {
        val installedSigner = SignedApkFixture()
        val candidateSigner = SignedApkFixture()
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = CandidateOwnedSource::class.java,
                oldApkBytes = installedSigner.signedApk.readBytes(),
                candidateApkBytes = candidateSigner.signedApk.readBytes(),
                repositorySigningKey = candidateSigner.fingerprint,
                candidateSignerFingerprints = setOf(candidateSigner.fingerprint),
                signatureVerifier = ApkSignerVerifier,
            )

        val failure = assertFailsWith<IllegalArgumentException> { fixture.manager.update(TARGET_PACKAGE) }

        assertTrue(failure.message.orEmpty().contains("does not preserve installed signer continuity"))
        fixture.assertUnchanged()
    }

    @Test
    fun `repository update rejects an APK for a different requested package without mutation`() {
        val fixture = UpdateFixture(candidatePackage = "malicious.extension", candidateVersionCode = 2)

        val failure = assertFailsWith<IllegalArgumentException> { fixture.manager.update(TARGET_PACKAGE) }

        assertTrue(failure.message.orEmpty().contains("does not match requested package"))
        fixture.assertUnchanged()
    }

    @Test
    fun `repository update rejects a non-exact non-increasing APK version without mutation`() {
        val fixture = UpdateFixture(candidatePackage = TARGET_PACKAGE, candidateVersionCode = 1, repositoryVersionCode = 2)

        val failure = assertFailsWith<IllegalArgumentException> { fixture.manager.update(TARGET_PACKAGE) }

        assertTrue(failure.message.orEmpty().contains("does not match repository version"))
        fixture.assertUnchanged()
    }

    @Test
    fun `direct URL install rejects a source ID owned by another package without mutation`() {
        val fixture = UpdateFixture(candidatePackage = TARGET_PACKAGE, candidateVersionCode = 2, candidateJarSource = CandidateSource::class.java)

        val failure = assertFailsWith<IllegalArgumentException> {
            fixture.manager.install(apkUrl = "https://cdn.example.test/replacement.apk")
        }

        assertTrue(failure.message.orEmpty().contains("is already owned by '$UNRELATED_PACKAGE'"))
        fixture.assertUnchanged()
    }

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
        val loader = ExtensionLoader(root.toFile(), ApkSignatureVerifier { setOf(TEST_SIGNER) })
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
                    signerFingerprints = setOf(TEST_SIGNER),
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

    private class UpdateFixture(
        candidatePackage: String,
        candidateVersionCode: Long,
        repositoryVersionCode: Long = 2,
        candidateJarSource: Class<out Source> = CandidateSource::class.java,
        oldApkBytes: ByteArray = "old target apk".toByteArray(),
        candidateApkBytes: ByteArray = "candidate apk".toByteArray(),
        repositorySigningKey: String? = TEST_SIGNER,
        candidateSignerFingerprints: Set<String> = setOf(TEST_SIGNER),
        signatureVerifier: ApkSignatureVerifier = ApkSignatureVerifier { setOf(TEST_SIGNER) },
        useRealPreparer: Boolean = false,
    ) {
        private val root = Files.createTempDirectory("extension-manager-identity")
        private val oldApk = root.resolve("target.apk")
        private val oldJar = root.resolve("target.jar")
        private val unrelatedApk = root.resolve("unrelated.apk")
        private val unrelatedJar = root.resolve("unrelated.jar")
        private val manifest = root.resolve("installed.json")
        private val targetRecord = installedRecord(TARGET_PACKAGE, "target.apk", listOf(TARGET_SOURCE_ID))
        private val unrelatedRecord = installedRecord(UNRELATED_PACKAGE, "unrelated.apk", listOf(UNRELATED_SOURCE_ID))
        private val oldSource = TestSource(TARGET_SOURCE_ID)
        private val unrelatedSource = TestSource(UNRELATED_SOURCE_ID)
        private val loader = ExtensionLoader(root.toFile(), signatureVerifier)
        private val directoryBefore: Map<String, ByteArray>
        val manager: ExtensionManager

        init {
            oldApk.writeBytes(oldApkBytes)
            oldJar.writeBytes("old target jar".toByteArray())
            unrelatedApk.writeBytes("unrelated apk".toByteArray())
            unrelatedJar.writeBytes("unrelated jar".toByteArray())
            manifest.writeBytes(jacksonObjectMapper().writeValueAsBytes(listOf(targetRecord, unrelatedRecord)))
            injectSource(loader, oldSource)
            injectSource(loader, unrelatedSource)

            val downloader =
                object : ExtensionDownloadClient {
                    override fun downloadRepoIndex(url: String): ByteArray {
                        val entry =
                            """{"name":"Target","packageName":"$TARGET_PACKAGE","resources":{"apkUrl":"https://cdn.example.test/replacement.apk"},"versionCode":"$repositoryVersionCode","versionName":"1.2.$repositoryVersionCode","sources":[]}"""
                        return if (repositorySigningKey == null) {
                            """[{"name":"Target","pkg":"$TARGET_PACKAGE","apk":"replacement.apk","lang":"en","code":$repositoryVersionCode,"version":"1.2.$repositoryVersionCode","sources":[]}]""".toByteArray()
                        } else {
                            """{"signingKey":"$repositorySigningKey","extensionList":{"extensions":[$entry]}}""".toByteArray()
                        }
                    }

                    override fun downloadApk(
                        url: String,
                        targetDir: Path,
                    ): Path =
                        Files.createTempFile(targetDir, ".test-download-", ".apk.tmp").also {
                            it.writeBytes(candidateApkBytes)
                        }
                }
            val preparer =
                ExtensionPreparer { apk ->
                    val jar = root.resolve(".prepared-candidate.jar")
                    writeClassJar(jar, candidateJarSource)
                    PreparedExtension(
                        pkgName = candidatePackage,
                        versionName = "1.2.$candidateVersionCode",
                        versionCode = candidateVersionCode,
                        mainClass = candidateJarSource.name,
                        apkFile = apk,
                        jarFile = jar,
                        signerFingerprints = candidateSignerFingerprints,
                    )
                }
            manager =
                if (useRealPreparer) {
                    ExtensionManager(loader, root.toFile(), downloader)
                } else {
                    ExtensionManager(loader, root.toFile(), downloader, preparer)
                }
            manager.setRepos(listOf("https://repo.example.test"))
            directoryBefore = snapshotDirectory(root)
        }

        fun assertUnchanged() {
            assertDirectoryEquals(directoryBefore, snapshotDirectory(root))
            assertSame(oldSource, loader.source(TARGET_SOURCE_ID))
            assertSame(unrelatedSource, loader.source(UNRELATED_SOURCE_ID))
            assertEquals(targetRecord, manager.recordForSource(TARGET_SOURCE_ID))
            assertEquals(unrelatedRecord, manager.recordForSource(UNRELATED_SOURCE_ID))
        }
    }

    companion object {
        private const val TARGET_PACKAGE = "example.extension"
        private const val UNRELATED_PACKAGE = "unrelated.extension"
        private const val TARGET_SOURCE_ID = 7L
        private const val UNRELATED_SOURCE_ID = 9L
        private const val TEST_SIGNER = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

        private fun installedRecord(
            pkgName: String,
            apkFileName: String,
            sourceIds: List<Long>,
        ) =
            InstalledExtension(
                pkgName = pkgName,
                name = pkgName,
                versionName = "1.2.1",
                versionCode = 1,
                lang = "en",
                apkFileName = apkFileName,
                mainClass = "example.Extension",
                isNsfw = false,
                iconUrl = null,
                repoUrl = null,
                sourceIds = sourceIds,
                sources = sourceIds.map { ExtensionSourceDto(it, pkgName, "en") },
            )

        private fun snapshotDirectory(root: Path): Map<String, ByteArray> =
            root.listDirectoryEntries().associate { it.fileName.toString() to it.readBytes() }

        private fun assertDirectoryEquals(
            expected: Map<String, ByteArray>,
            actual: Map<String, ByteArray>,
        ) {
            assertEquals(expected.keys, actual.keys, "extension-directory listing changed")
            expected.forEach { (name, bytes) -> assertContentEquals(bytes, actual.getValue(name), "$name bytes changed") }
        }

        private fun injectSource(
            loader: ExtensionLoader,
            source: Source,
        ) {
            val field = ExtensionLoader::class.java.getDeclaredField("sources").apply { isAccessible = true }
            @Suppress("UNCHECKED_CAST")
            val registry = field.get(loader) as MutableMap<Long, Source>
            registry[source.id] = source
        }

        private fun writeClassJar(
            target: Path,
            sourceClass: Class<*>,
        ) {
            val entryName = sourceClass.name.replace('.', '/') + ".class"
            val classBytes = requireNotNull(sourceClass.classLoader.getResourceAsStream(entryName)) { "missing $entryName" }.use { it.readBytes() }
            ZipOutputStream(Files.newOutputStream(target)).use { jar ->
                jar.putNextEntry(ZipEntry(entryName))
                jar.write(classBytes)
                jar.closeEntry()
            }
        }
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

internal class CandidateSource : Source {
    override val id: Long = 9L
    override val name: String = "Candidate Source"
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

internal class CandidateOwnedSource : Source {
    override val id: Long = 7L
    override val name: String = "Owned Candidate Source"
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
