package enginehost

import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.fasterxml.jackson.module.kotlin.readValue
import com.sun.net.httpserver.HttpServer
import eu.kanade.tachiyomi.source.Source
import eu.kanade.tachiyomi.source.SourceFactory
import eu.kanade.tachiyomi.source.model.FilterList
import eu.kanade.tachiyomi.source.model.MangasPage
import eu.kanade.tachiyomi.source.model.Page
import eu.kanade.tachiyomi.source.model.SChapter
import eu.kanade.tachiyomi.source.model.SManga
import eu.kanade.tachiyomi.source.model.SMangaUpdate
import java.nio.file.Files
import java.nio.file.Path
import java.net.InetSocketAddress
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.file.attribute.PosixFilePermissions
import java.util.concurrent.CompletableFuture
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream
import suwayomi.tachidesk.manga.impl.util.PackageTools
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
    fun `prepared update reports source retirement without mutating active state`() {
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = CandidateSource::class.java,
                unrelatedRecordSourceIds = emptyList(),
                injectUnrelatedSource = false,
            )

        val prepared = fixture.manager.prepareUpdate(TARGET_PACKAGE)

        assertEquals(TARGET_PACKAGE, prepared.pkgName)
        assertEquals(1, prepared.installedVersionCode)
        assertEquals(2, prepared.candidateVersionCode)
        assertEquals(listOf(TARGET_SOURCE_ID), prepared.installedSourceIds)
        assertEquals(listOf(UNRELATED_SOURCE_ID), prepared.candidateSourceIds)
        assertEquals(listOf(TARGET_SOURCE_ID), prepared.removedSourceIds)
        assertSame(fixture.oldTargetSource(), fixture.targetSource())
        fixture.manager.discardPreparedUpdate(prepared.token, TARGET_PACKAGE)
        fixture.assertUnchanged()
    }

    @Test
    fun `activation rejects protected retired source with structured conflict and no mutation`() {
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = CandidateSource::class.java,
                unrelatedRecordSourceIds = emptyList(),
                injectUnrelatedSource = false,
            )
        val prepared = fixture.manager.prepareUpdate(TARGET_PACKAGE)

        val failure =
            assertFailsWith<SourceRetirementConflict> {
                fixture.manager.activatePreparedUpdate(
                    prepared.toActivationRequest(protectedSourceIds = listOf(TARGET_SOURCE_ID)),
                )
            }

        assertEquals(TARGET_PACKAGE, failure.pkgName)
        assertEquals(listOf(TARGET_SOURCE_ID), failure.sourceIds)
        fixture.assertUnchanged()
    }

    @Test
    fun `activation accepts exact prepared witness and publishes candidate`() {
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = CandidateOwnedSource::class.java,
            )
        val prepared = fixture.manager.prepareUpdate(TARGET_PACKAGE)

        fixture.manager.activatePreparedUpdate(prepared.toActivationRequest(emptyList()))

        assertEquals("Owned Candidate Source", fixture.targetSource()?.name)
        fixture.assertUnrelatedPreserved()
    }

    @Test
    fun `prepared activation rejects runtime source ID drift and releases candidate`() {
        ActivationDuplicateProbe.reset()
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = ActivationDuplicateFactory::class.java,
            )
        val prepared = fixture.manager.prepareUpdate(TARGET_PACKAGE)

        val failure = assertFailsWith<IllegalArgumentException> {
            fixture.manager.activatePreparedUpdate(prepared.toActivationRequest(emptyList()))
        }

        assertTrue(failure.message.orEmpty().contains("duplicate source IDs"))
        fixture.assertUnchanged()
    }

    @Test
    fun `rejected prepared witness is released and cannot be retried`() {
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = CandidateOwnedSource::class.java,
            )
        val prepared = fixture.manager.prepareUpdate(TARGET_PACKAGE)
        val mismatched = prepared.toActivationRequest(emptyList()).copy(candidateVersionCode = 3)

        assertFailsWith<IllegalArgumentException> { fixture.manager.activatePreparedUpdate(mismatched) }
        assertFailsWith<IllegalArgumentException> {
            fixture.manager.activatePreparedUpdate(prepared.toActivationRequest(emptyList()))
        }
        fixture.assertUnchanged()
    }

    @Test
    fun `expired prepared update is rejected and cleaned`() {
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = CandidateOwnedSource::class.java,
                preparedUpdateTtlNanos = 0,
            )
        val prepared = fixture.manager.prepareUpdate(TARGET_PACKAGE)

        assertFailsWith<IllegalArgumentException> {
            fixture.manager.activatePreparedUpdate(prepared.toActivationRequest(emptyList()))
        }
        fixture.assertUnchanged()
    }

    @Test
    fun `prepared update RPC is private and returns structured retirement conflict`() {
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = CandidateSource::class.java,
                unrelatedRecordSourceIds = emptyList(),
                injectUnrelatedSource = false,
            )
        val server = fixture.rpcServer(CONTROL_TOKEN)
        server.start()
        try {
            val port = boundAddress(server).port
            val prepareUri = URI("http://127.0.0.1:$port/extensions/$TARGET_PACKAGE/prepare-update")
            val unauthorized = HttpRequest.newBuilder(prepareUri).POST(HttpRequest.BodyPublishers.noBody()).build()
            assertEquals(401, HttpClient.newHttpClient().send(unauthorized, HttpResponse.BodyHandlers.ofString()).statusCode())

            val prepare =
                HttpRequest.newBuilder(prepareUri)
                    .header("Authorization", "Bearer $CONTROL_TOKEN")
                    .POST(HttpRequest.BodyPublishers.noBody())
                    .build()
            val preparedResponse = HttpClient.newHttpClient().send(prepare, HttpResponse.BodyHandlers.ofString())
            assertEquals(200, preparedResponse.statusCode())
            val prepared: PreparedUpdateDto = jacksonObjectMapper().readValue(preparedResponse.body())
            val activation = prepared.toActivationRequest(listOf(TARGET_SOURCE_ID))
            val activateRequest =
                HttpRequest.newBuilder(URI("http://127.0.0.1:$port/extensions/$TARGET_PACKAGE/activate-prepared-update"))
                    .header("Authorization", "Bearer $CONTROL_TOKEN")
                    .header("Content-Type", "application/json")
                    .POST(HttpRequest.BodyPublishers.ofByteArray(jacksonObjectMapper().writeValueAsBytes(activation)))
                    .build()
            val conflict = HttpClient.newHttpClient().send(activateRequest, HttpResponse.BodyHandlers.ofString())

            assertEquals(409, conflict.statusCode())
            val body: SourceRetirementConflictResponse = jacksonObjectMapper().readValue(conflict.body())
            assertEquals("source_retirement_conflict", body.code)
            assertEquals(TARGET_PACKAGE, body.pkgName)
            assertEquals(listOf(TARGET_SOURCE_ID), body.sourceIds)
            fixture.assertUnchanged()
        } finally {
            server.stop()
            fixture.manager.close()
        }
    }

    private fun PreparedUpdateDto.toActivationRequest(protectedSourceIds: List<Long>) =
        ActivatePreparedUpdateRequest(
            token = token,
            pkgName = pkgName,
            installedVersionCode = installedVersionCode,
            candidateVersionCode = candidateVersionCode,
            installedSourceIds = installedSourceIds,
            candidateSourceIds = candidateSourceIds,
            removedSourceIds = removedSourceIds,
            mutationSequence = mutationSequence,
            protectedSourceIds = protectedSourceIds,
        )

    @Test
    fun `failed trust persistence leaves authorization cache and mutation state unchanged`() {
        val approvedSigner = SignedApkFixture()
        val proposedSigner = SignedApkFixture()
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = CandidateOwnedSource::class.java,
                oldApkBytes = proposedSigner.signedApk.readBytes(),
                candidateApkBytes = proposedSigner.signedApk.readBytes(),
                repositorySigningKey = proposedSigner.fingerprint,
                candidateSignerFingerprints = setOf(proposedSigner.fingerprint),
                signatureVerifier = ApkSignerVerifier,
                configuredSignerFingerprint = approvedSigner.fingerprint,
            )
        fixture.manager.list()
        val stateBefore = trustMutationState(fixture.manager)
        val trustFileBefore = fixture.trustFileBytes()

        val rotation = fixture.withTrustPersistenceBlocked { fixture.manager.setRepoTrust(REPO_URL, proposedSigner.fingerprint) }
        val stateAfterFailedRotation = trustMutationState(fixture.manager)
        val trustFileAfter = fixture.trustFileBytes()
        val update = runCatching { fixture.manager.update(TARGET_PACKAGE) }

        assertTrue(rotation.isFailure, "trust rotation unexpectedly reported success")
        assertEquals(stateBefore, stateAfterFailedRotation)
        assertContentEquals(trustFileBefore, trustFileAfter)
        assertTrue(update.isFailure, "an unpersisted signer authorized a subsequent update")
        assertTrue(update.exceptionOrNull()?.message.orEmpty().contains("does not match the configured repository signer"))
        fixture.assertUnchanged()
    }

    @Test
    fun `catalogue and candidate signer change reject until explicit persisted trust rotation`() {
        val approvedSigner = SignedApkFixture()
        val changedSigner = SignedApkFixture()
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = CandidateOwnedSource::class.java,
                oldApkBytes = changedSigner.signedApk.readBytes(),
                candidateApkBytes = changedSigner.signedApk.readBytes(),
                repositorySigningKey = changedSigner.fingerprint,
                candidateSignerFingerprints = setOf(changedSigner.fingerprint),
                signatureVerifier = ApkSignerVerifier,
                configuredSignerFingerprint = approvedSigner.fingerprint,
            )

        fixture.assertTrustPersisted(approvedSigner.fingerprint)
        val failure = assertFailsWith<IllegalArgumentException> { fixture.manager.update(TARGET_PACKAGE) }

        assertTrue(failure.message.orEmpty().contains("does not match the configured repository signer"))
        fixture.assertUnchanged()

        fixture.manager.setRepoTrust(REPO_URL, changedSigner.fingerprint)
        val updated = fixture.manager.update(TARGET_PACKAGE).single { it.pkgName == TARGET_PACKAGE }

        assertEquals(2, updated.versionCode)
    }

    @Test
    fun `repository update accepts a verified descendant signer after trust rotation`() {
        val rotation = SigningRotationApkFixture()
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = CandidateOwnedSource::class.java,
                oldApkBytes = rotation.originalApk.signedApk.readBytes(),
                candidateApkBytes = rotation.descendantApk.signedApk.readBytes(),
                repositorySigningKey = rotation.descendantFingerprint,
                candidateSignerFingerprints = setOf(rotation.descendantFingerprint),
                signatureVerifier = ApkSignerVerifier,
                configuredSignerFingerprint = rotation.descendantFingerprint,
                verifyCandidateSignature = true,
            )

        val updated = fixture.manager.update(TARGET_PACKAGE).single { it.pkgName == TARGET_PACKAGE }

        assertEquals(2, updated.versionCode)
        fixture.assertUnrelatedPreserved()
    }

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
        val rotation = SigningRotationApkFixture()
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = CandidateOwnedSource::class.java,
                oldApkBytes = rotation.originalApk.signedApk.readBytes(),
                candidateApkBytes = rotation.unrelatedApk.signedApk.readBytes(),
                repositorySigningKey = rotation.descendantFingerprint,
                candidateSignerFingerprints = setOf(rotation.descendantFingerprint),
                signatureVerifier = ApkSignerVerifier,
                verifyCandidateSignature = true,
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
    fun `repository update rejects a source ID also claimed by a foreign manifest record`() {
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = CandidateOwnedSource::class.java,
                unrelatedPackage = "foreign0.extension",
                unrelatedRecordSourceIds = listOf(TARGET_SOURCE_ID, UNRELATED_SOURCE_ID),
            )

        val failure = assertFailsWith<IllegalArgumentException> { fixture.manager.update(TARGET_PACKAGE) }

        assertTrue(failure.message.orEmpty().contains("foreign0.extension"))
        fixture.assertUnchanged()
    }

    @Test
    fun `repository update restores every source when replacement registration drops an unrelated source`() {
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = CandidateOwnedSource::class.java,
                targetRecordSourceIds = listOf(TARGET_SOURCE_ID, UNRELATED_SOURCE_ID),
            )

        val failure = assertFailsWith<IllegalArgumentException> { fixture.manager.update(TARGET_PACKAGE) }

        assertTrue(failure.message.orEmpty().contains("installed unrelated source ID $UNRELATED_SOURCE_ID"))
        fixture.assertUnchanged()
    }

    @Test
    fun `successful repository update preserves the unrelated source and owning package`() {
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = CandidateOwnedSource::class.java,
            )

        val updated = fixture.manager.update(TARGET_PACKAGE).single { it.pkgName == TARGET_PACKAGE }

        assertEquals(2, updated.versionCode)
        fixture.assertUnrelatedPreserved()
    }

    @Test
    fun `manifest failure restores the complete installed and runtime state`() {
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = CandidateOwnedSource::class.java,
            )
        fixture.blockManifestPersistence()

        assertFails { fixture.manager.update(TARGET_PACKAGE) }

        fixture.assertBlockedManifestRollback()
    }

    @Test
    fun `manifest failure never publishes its candidate to concurrent source readers`() {
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = PublicationObservedSource::class.java,
            )
        fixture.blockManifestPersistence()
        val publication = fixture.armPublicationProbe()
        val executor = Executors.newSingleThreadExecutor()
        val update =
            CompletableFuture.supplyAsync(
                { runCatching { fixture.manager.update(TARGET_PACKAGE) }.exceptionOrNull() },
                executor,
            )

        try {
            CompletableFuture.anyOf(publication.published, update).get(5, TimeUnit.SECONDS)
            val observedDuringCommit = fixture.targetSource()
            publication.release.countDown()
            val failure = update.get(5, TimeUnit.SECONDS)

            assertSame(fixture.oldTargetSource(), observedDuringCommit)
            assertTrue(failure != null, "manifest failure unexpectedly committed")
            fixture.assertBlockedManifestRollback()
        } finally {
            publication.release.countDown()
            executor.shutdownNow()
        }
    }

    @Test
    fun `update rejects an unrelated manifest source missing from the boot registry`() {
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = CandidateOwnedSource::class.java,
                injectUnrelatedSource = false,
            )

        val failure = assertFailsWith<IllegalArgumentException> { fixture.manager.update(TARGET_PACKAGE) }

        assertTrue(failure.message.orEmpty().contains("installed unrelated source ID $UNRELATED_SOURCE_ID is missing"))
        fixture.assertUnchanged()
    }

    @Test
    fun `boot reload rejects a missing installed file without replacing the previous generation`() {
        val root = Files.createTempDirectory("extension-manager-boot-missing")
        val diskRecord = installedRecord(TARGET_PACKAGE, "missing.apk", listOf(TARGET_SOURCE_ID))
        root.resolve("installed.json").writeBytes(jacksonObjectMapper().writeValueAsBytes(listOf(diskRecord)))
        val loader = ExtensionLoader(root.toFile(), ApkSignatureVerifier { setOf(TEST_SIGNER) })
        val previousSource = TestSource(UNRELATED_SOURCE_ID)
        val previousRecord = installedRecord(UNRELATED_PACKAGE, "previous.apk", listOf(UNRELATED_SOURCE_ID))
        loader.publishRegistry(loader.prepareRegistry(mapOf(UNRELATED_SOURCE_ID to previousSource), mapOf(UNRELATED_PACKAGE to previousRecord)))
        val manager = ExtensionManager(loader, root.toFile())

        val failure = assertFailsWith<IllegalArgumentException> { manager.reloadInstalled() }

        assertTrue(failure.message.orEmpty().contains("missing"))
        assertSame(previousSource, loader.source(UNRELATED_SOURCE_ID))
        assertEquals(mapOf(UNRELATED_PACKAGE to previousRecord), installedSnapshot(loader))
    }

    @Test
    fun `boot reload rejects mismatched APK identity and signer without replacing the previous generation`() {
        val failures =
            listOf(
                assertBootRejected(inspectedPackage = "substituted.extension"),
                assertBootRejected(inspectedVersionName = "1.2.2"),
                assertBootRejected(inspectedVersionCode = 2),
                assertBootRejected(inspectedMainClass = TestSource::class.java.name),
                assertBootRejected(inspectedSignature = VerifiedApkSignature(setOf(TEST_SIGNER_B), emptyList())),
            )

        assertTrue(failures[0].message.orEmpty().contains("does not match manifest package"))
        assertTrue(failures[1].message.orEmpty().contains("does not match manifest version"))
        assertTrue(failures[2].message.orEmpty().contains("does not match manifest version"))
        assertTrue(failures[3].message.orEmpty().contains("does not match manifest main class"))
        assertTrue(failures[4].message.orEmpty().contains("does not match its manifest identity"))
    }

    @Test
    fun `boot reload rejects a source set that differs from its manifest record`() {
        PreferenceReloadProbe.sourceIds = listOf(PREFERENCE_SOURCE_ID)
        try {
            val failure = assertBootRejected(sourceClass = PreferenceReloadFactory::class.java)

            assertTrue(failure.message.orEmpty().contains("source IDs do not match its manifest record"))
        } finally {
            PreferenceReloadProbe.sourceIds = emptyList()
        }
    }

    @Test
    fun `boot reload rejects a cross-package source collision without replacing the previous generation`() {
        val root = Files.createTempDirectory("extension-manager-boot-collision")
        val firstRecord =
            installedRecord(TARGET_PACKAGE, "target.apk", listOf(TARGET_SOURCE_ID)).copy(
                mainClass = CandidateOwnedSource::class.java.name,
                signerFingerprints = setOf(TEST_SIGNER),
            )
        val secondRecord =
            installedRecord(UNRELATED_PACKAGE, "foreign.apk", listOf(TARGET_SOURCE_ID)).copy(
                mainClass = CandidateOwnedSource::class.java.name,
                signerFingerprints = setOf(TEST_SIGNER),
            )
        listOf(firstRecord, secondRecord).forEach { record ->
            root.resolve(record.apkFileName).writeBytes("apk".toByteArray())
            writeClassJar(root.resolve(record.apkFileName.replaceAfterLast('.', "jar")), CandidateOwnedSource::class.java)
        }
        val manifest = root.resolve("installed.json")
        manifest.writeBytes(jacksonObjectMapper().writeValueAsBytes(listOf(firstRecord, secondRecord)))
        val recordsByFile = listOf(firstRecord, secondRecord).associateBy { it.apkFileName }
        val loader = ExtensionLoader(root.toFile(), ApkSignatureVerifier { setOf(TEST_SIGNER) })
        val previousSource = TestSource(PREFERENCE_SOURCE_ID)
        val previousRecord = installedRecord("previous.extension", "previous.apk", listOf(PREFERENCE_SOURCE_ID))
        loader.publishRegistry(
            loader.prepareRegistry(
                mapOf(PREFERENCE_SOURCE_ID to previousSource),
                mapOf(previousRecord.pkgName to previousRecord),
            ),
        )
        val manager =
            ExtensionManager(
                loader,
                root.toFile(),
                localApkDownloader(ByteArray(0)),
                ExtensionPreparer { error("unused") },
                ExtensionArtifactInspector { apk ->
                    val record = recordsByFile.getValue(apk.fileName.toString())
                    InspectedExtensionArtifact(
                        record.pkgName,
                        record.versionName,
                        record.versionCode,
                        record.mainClass,
                        apk,
                        VerifiedApkSignature(setOf(TEST_SIGNER), emptyList()),
                    )
                },
            )

        val failure = assertFailsWith<IllegalArgumentException> { manager.reloadInstalled() }

        assertTrue(failure.message.orEmpty().contains("source ID $TARGET_SOURCE_ID"))
        assertSame(previousSource, loader.source(PREFERENCE_SOURCE_ID))
        assertEquals(mapOf(previousRecord.pkgName to previousRecord), installedSnapshot(loader))
    }

    @Test
    fun `boot reload rejects a missing installed jar without replacing the previous generation`() {
        val root = Files.createTempDirectory("extension-manager-boot-missing-jar")
        val diskRecord =
            installedRecord(TARGET_PACKAGE, "target.apk", listOf(TARGET_SOURCE_ID)).copy(
                mainClass = CandidateOwnedSource::class.java.name,
                signerFingerprints = setOf(TEST_SIGNER),
            )
        root.resolve(diskRecord.apkFileName).writeBytes("apk".toByteArray())
        root.resolve("installed.json").writeBytes(jacksonObjectMapper().writeValueAsBytes(listOf(diskRecord)))
        val loader = ExtensionLoader(root.toFile(), ApkSignatureVerifier { setOf(TEST_SIGNER) })
        val previousSource = TestSource(UNRELATED_SOURCE_ID)
        val previousRecord = installedRecord(UNRELATED_PACKAGE, "previous.apk", listOf(UNRELATED_SOURCE_ID))
        loader.publishRegistry(loader.prepareRegistry(mapOf(UNRELATED_SOURCE_ID to previousSource), mapOf(UNRELATED_PACKAGE to previousRecord)))
        val manager = ExtensionManager(loader, root.toFile())

        val failure = assertFailsWith<IllegalArgumentException> { manager.reloadInstalled() }

        assertTrue(failure.message.orEmpty().contains("jar missing"))
        assertSame(previousSource, loader.source(UNRELATED_SOURCE_ID))
        assertEquals(mapOf(UNRELATED_PACKAGE to previousRecord), installedSnapshot(loader))
    }

    @Test
    fun `corrupt boot manifest fails closed without replacing the previous generation`() {
        val root = Files.createTempDirectory("extension-manager-boot-corrupt")
        root.resolve("installed.json").writeBytes("{".toByteArray())
        val loader = ExtensionLoader(root.toFile(), ApkSignatureVerifier { setOf(TEST_SIGNER) })
        val previousSource = TestSource(UNRELATED_SOURCE_ID)
        val previousRecord = installedRecord(UNRELATED_PACKAGE, "previous.apk", listOf(UNRELATED_SOURCE_ID))
        loader.publishRegistry(loader.prepareRegistry(mapOf(UNRELATED_SOURCE_ID to previousSource), mapOf(UNRELATED_PACKAGE to previousRecord)))

        val failure = assertFailsWith<IllegalArgumentException> { ExtensionManager(loader, root.toFile()) }

        assertTrue(failure.message.orEmpty().contains("manifest is corrupt"))
        assertSame(previousSource, loader.source(UNRELATED_SOURCE_ID))
        assertEquals(mapOf(UNRELATED_PACKAGE to previousRecord), installedSnapshot(loader))
    }

    @Test
    fun `boot reload migrates a legacy empty signer record after verified materialization`() {
        val root = Files.createTempDirectory("extension-manager-boot-legacy-signer")
        val record =
            installedRecord(TARGET_PACKAGE, "target.apk", listOf(TARGET_SOURCE_ID)).copy(
                mainClass = CandidateOwnedSource::class.java.name,
            )
        root.resolve(record.apkFileName).writeBytes("apk".toByteArray())
        writeClassJar(root.resolve("target.jar"), CandidateOwnedSource::class.java)
        root.resolve("installed.json").writeBytes(jacksonObjectMapper().writeValueAsBytes(listOf(record)))
        val loader = ExtensionLoader(root.toFile(), ApkSignatureVerifier { setOf(TEST_SIGNER) })
        val manager = bootManager(loader, root, record, VerifiedApkSignature(setOf(TEST_SIGNER), emptyList()))

        manager.reloadInstalled()

        assertEquals(TARGET_SOURCE_ID, loader.source(TARGET_SOURCE_ID)?.id)
        assertEquals(setOf(TEST_SIGNER), manager.recordForSource(TARGET_SOURCE_ID)?.signerFingerprints)
        val persisted = jacksonObjectMapper().readValue(root.resolve("installed.json").toFile(), Array<InstalledExtension>::class.java).single()
        assertEquals(setOf(TEST_SIGNER), persisted.signerFingerprints)
    }

    @Test
    fun `boot reload accepts an owner-approved descendant signer with verified lineage`() {
        val root = Files.createTempDirectory("extension-manager-boot-lineage")
        val record =
            installedRecord(TARGET_PACKAGE, "target.apk", listOf(TARGET_SOURCE_ID)).copy(
                mainClass = CandidateOwnedSource::class.java.name,
                repoUrl = REPO_URL,
                signerFingerprints = setOf(TEST_SIGNER),
                signingCertificateLineage = listOf(TEST_SIGNER),
            )
        root.resolve(record.apkFileName).writeBytes("apk".toByteArray())
        writeClassJar(root.resolve("target.jar"), CandidateOwnedSource::class.java)
        root.resolve("installed.json").writeBytes(jacksonObjectMapper().writeValueAsBytes(listOf(record)))
        val loader = ExtensionLoader(root.toFile(), ApkSignatureVerifier { setOf(TEST_SIGNER_B) })
        val signature = VerifiedApkSignature(setOf(TEST_SIGNER_B), listOf(TEST_SIGNER, TEST_SIGNER_B))
        val manager = bootManager(loader, root, record, signature)
        manager.setRepos(listOf(REPO_URL))
        manager.setRepoTrust(REPO_URL, TEST_SIGNER_B)

        manager.reloadInstalled()

        val migrated = manager.recordForSource(TARGET_SOURCE_ID)
        assertEquals(setOf(TEST_SIGNER_B), migrated?.signerFingerprints)
        assertEquals(listOf(TEST_SIGNER, TEST_SIGNER_B), migrated?.signingCertificateLineage)
    }

    @Test
    fun `preference reload removes stale IDs and atomically updates manifest ownership`() {
        val root = Files.createTempDirectory("extension-manager-preference-ids")
        val apk = root.resolve("target.apk").also { it.writeBytes("target apk".toByteArray()) }
        val jar = root.resolve("target.jar").also { writeClassJar(it, PreferenceReloadFactory::class.java) }
        val record =
            installedRecord(TARGET_PACKAGE, apk.fileName.toString(), listOf(TARGET_SOURCE_ID)).copy(
                mainClass = PreferenceReloadFactory::class.java.name,
            )
        root.resolve("installed.json").writeBytes(jacksonObjectMapper().writeValueAsBytes(listOf(record)))
        val loader = ExtensionLoader(root.toFile(), ApkSignatureVerifier { setOf(TEST_SIGNER) })
        val oldSource = TestSource(TARGET_SOURCE_ID)
        injectSource(loader, oldSource)
        publishInstalled(loader, mapOf(TARGET_PACKAGE to record))
        val manager = ExtensionManager(loader, root.toFile())
        PreferenceReloadProbe.sourceIds = listOf(PREFERENCE_SOURCE_ID)

        val reloaded = manager.underLock { manager.reloadForSource(TARGET_SOURCE_ID) }

        assertTrue(reloaded)
        assertEquals(null, loader.source(TARGET_SOURCE_ID), "stale source ID remained active")
        assertEquals(PREFERENCE_SOURCE_ID, loader.source(PREFERENCE_SOURCE_ID)?.id)
        assertEquals(listOf(PREFERENCE_SOURCE_ID), manager.recordForSource(PREFERENCE_SOURCE_ID)?.sourceIds)
        val persisted = jacksonObjectMapper().readValue(root.resolve("installed.json").toFile(), Array<InstalledExtension>::class.java).single()
        assertEquals(listOf(PREFERENCE_SOURCE_ID), persisted.sourceIds)
        assertTrue(Files.exists(jar))
    }

    @Test
    fun `preference reload rejects a foreign source ID and preserves the previous generation`() {
        val root = Files.createTempDirectory("extension-manager-preference-collision")
        val targetApk = root.resolve("target.apk").also { it.writeBytes("target apk".toByteArray()) }
        root.resolve("target.jar").also { writeClassJar(it, PreferenceReloadFactory::class.java) }
        val foreignApk = root.resolve("foreign.apk").also { it.writeBytes("foreign apk".toByteArray()) }
        val targetRecord =
            installedRecord(TARGET_PACKAGE, targetApk.fileName.toString(), listOf(TARGET_SOURCE_ID)).copy(
                mainClass = PreferenceReloadFactory::class.java.name,
            )
        val foreignRecord = installedRecord(UNRELATED_PACKAGE, foreignApk.fileName.toString(), listOf(UNRELATED_SOURCE_ID))
        val manifest = root.resolve("installed.json")
        manifest.writeBytes(jacksonObjectMapper().writeValueAsBytes(listOf(targetRecord, foreignRecord)))
        val manifestBefore = manifest.readBytes()
        val loader = ExtensionLoader(root.toFile(), ApkSignatureVerifier { setOf(TEST_SIGNER) })
        PreferenceReloadProbe.sourceIds = listOf(TARGET_SOURCE_ID)
        val targetJar = root.resolve("target.jar")
        val oldTarget = loader.reinstantiate(targetJar.toString(), PreferenceReloadFactory::class.java.name).single()
        val foreignSource = TestSource(UNRELATED_SOURCE_ID)
        loader.publishTestSources(listOf(oldTarget))
        injectSource(loader, foreignSource)
        publishInstalled(loader, mapOf(TARGET_PACKAGE to targetRecord, UNRELATED_PACKAGE to foreignRecord))
        val manager = ExtensionManager(loader, root.toFile())
        PreferenceReloadProbe.sourceIds = listOf(UNRELATED_SOURCE_ID)

        val failure = assertFailsWith<IllegalArgumentException> {
            manager.underLock { manager.reloadForSource(TARGET_SOURCE_ID) }
        }

        assertTrue(failure.message.orEmpty().contains("source ID $UNRELATED_SOURCE_ID"))
        assertSame(oldTarget, loader.source(TARGET_SOURCE_ID))
        assertSame(foreignSource, loader.source(UNRELATED_SOURCE_ID))
        assertTrue(loader.hasCachedLoader(targetJar), "the previous generation's loader was evicted")
        assertContentEquals(manifestBefore, manifest.readBytes())
    }

    @Test
    fun `final activation rejects duplicate source multiplicity without mutation`() {
        ActivationDuplicateProbe.reset()
        val fixture =
            UpdateFixture(
                candidatePackage = TARGET_PACKAGE,
                candidateVersionCode = 2,
                candidateJarSource = ActivationDuplicateFactory::class.java,
            )

        val failure = assertFailsWith<IllegalArgumentException> { fixture.manager.update(TARGET_PACKAGE) }

        assertTrue(failure.message.orEmpty().contains("duplicate source IDs"))
        fixture.assertUnchanged()
    }

    @Test
    fun `failed final activation evicts its loader before a same-name retry`() {
        val root = Files.createTempDirectory("extension-manager-loader-retry")
        val manifest = root.resolve("installed.json")
        val prepareCount = AtomicInteger()
        val loader = ExtensionLoader(root.toFile(), ApkSignatureVerifier { setOf(TEST_SIGNER) })
        val downloader = localApkDownloader("candidate apk".toByteArray())
        val preparer =
            ExtensionPreparer { apk ->
                val jar = root.resolve(".prepared-retry.jar")
                if (prepareCount.getAndIncrement() == 0) {
                    writeRenamedClassJar(jar, RetrySourceOne::class.java, RETRY_CLASS_NAME)
                } else {
                    writeRenamedClassJar(jar, RetrySourceTwo::class.java, RETRY_CLASS_NAME)
                }
                PreparedExtension(
                    pkgName = "retry.extension",
                    versionName = "1.2.1",
                    versionCode = 1,
                    mainClass = RETRY_CLASS_NAME,
                    apkFile = apk,
                    jarFile = jar,
                    signerFingerprints = setOf(TEST_SIGNER),
                )
            }
        val manager = ExtensionManager(loader, root.toFile(), downloader, preparer)
        val finalJar = root.resolve("retry.jar")
        Files.createDirectory(manifest)

        assertFails { manager.install(apkUrl = "https://cdn.example.test/retry.apk") }

        assertTrue(finalJar.toString() !in PackageTools.jarLoaderMap, "failed final loader remained cached")
        Files.delete(manifest)
        manager.install(apkUrl = "https://cdn.example.test/retry.apk")
        assertEquals("Second Retry Source", loader.source(RETRY_SOURCE_ID)?.name)
    }

    @Test
    fun `successful replacement retires the old loader without closing an in-flight source`() {
        val root = Files.createTempDirectory("extension-manager-loader-retirement")
        val oldApk = root.resolve("old.apk").also { it.writeBytes("old apk".toByteArray()) }
        val oldJar = root.resolve("old.jar").also { writeRenamedClassJar(it, RetrySourceOne::class.java, RETRY_CLASS_NAME) }
        val oldRecord =
            installedRecord(TARGET_PACKAGE, oldApk.fileName.toString(), listOf(RETRY_SOURCE_ID)).copy(
                mainClass = RETRY_CLASS_NAME,
            )
        root.resolve("installed.json").writeBytes(jacksonObjectMapper().writeValueAsBytes(listOf(oldRecord)))
        val loader = ExtensionLoader(root.toFile(), ApkSignatureVerifier { setOf(TEST_SIGNER) })
        val oldSource = loader.reinstantiate(oldJar.toString(), RETRY_CLASS_NAME).single()
        loader.publishRegistry(
            loader.prepareRegistry(
                mapOf(RETRY_SOURCE_ID to oldSource),
                mapOf(TARGET_PACKAGE to oldRecord),
            ),
        )
        val downloader =
            object : ExtensionDownloadClient {
                override fun downloadRepoIndex(url: String): ByteArray =
                    """{"signingKey":"$TEST_SIGNER","extensionList":{"extensions":[{"name":"Target","packageName":"$TARGET_PACKAGE","resources":{"apkUrl":"https://cdn.example.test/replacement.apk"},"versionCode":"2","versionName":"1.2.2","sources":[]}]}}""".toByteArray()

                override fun downloadApk(
                    url: String,
                    targetDir: Path,
                ): Path = Files.createTempFile(targetDir, ".test-download-", ".apk.tmp").also { it.writeBytes("new apk".toByteArray()) }
            }
        val preparer =
            ExtensionPreparer { apk ->
                val jar = root.resolve(".prepared-replacement.jar")
                writeRenamedClassJar(jar, RetrySourceTwo::class.java, RETRY_CLASS_NAME)
                PreparedExtension(
                    pkgName = TARGET_PACKAGE,
                    versionName = "1.2.2",
                    versionCode = 2,
                    mainClass = RETRY_CLASS_NAME,
                    apkFile = apk,
                    jarFile = jar,
                    signerFingerprints = setOf(TEST_SIGNER),
                )
            }
        val manager = ExtensionManager(loader, root.toFile(), downloader, preparer)
        manager.setRepos(listOf(REPO_URL))
        manager.setRepoTrust(REPO_URL, TEST_SIGNER)

        manager.update(TARGET_PACKAGE)

        assertTrue(oldJar.toString() !in PackageTools.jarLoaderMap, "superseded loader remained cached")
        assertTrue(Files.exists(oldJar), "superseded jar was deleted while an old source remained reachable")
        assertEquals("First Retry Source", oldSource.name)
        assertEquals("Second Retry Source", loader.source(RETRY_SOURCE_ID)?.name)
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
        publishInstalled(loader, mapOf(installed.pkgName to installed))
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
        candidateJarSource: Class<*> = CandidateSource::class.java,
        oldApkBytes: ByteArray = "old target apk".toByteArray(),
        candidateApkBytes: ByteArray = "candidate apk".toByteArray(),
        repositorySigningKey: String? = TEST_SIGNER,
        candidateSignerFingerprints: Set<String> = setOf(TEST_SIGNER),
        private val signatureVerifier: ApkSignatureVerifier = ApkSignatureVerifier { setOf(TEST_SIGNER) },
        useRealPreparer: Boolean = false,
        verifyCandidateSignature: Boolean = false,
        configuredSignerFingerprint: String? = repositorySigningKey,
        unrelatedPackage: String = UNRELATED_PACKAGE,
        unrelatedRecordSourceIds: List<Long> = listOf(UNRELATED_SOURCE_ID),
        targetRecordSourceIds: List<Long> = listOf(TARGET_SOURCE_ID),
        injectUnrelatedSource: Boolean = true,
        preparedUpdateTtlNanos: Long? = null,
    ) {
        private val root = Files.createTempDirectory("extension-manager-identity")
        private val oldApk = root.resolve("target.apk")
        private val oldJar = root.resolve("target.jar")
        private val unrelatedApk = root.resolve("unrelated.apk")
        private val unrelatedJar = root.resolve("unrelated.jar")
        private val manifest = root.resolve("installed.json")
        private val targetRecord = installedRecord(TARGET_PACKAGE, "target.apk", targetRecordSourceIds)
        private val unrelatedRecord = installedRecord(unrelatedPackage, "unrelated.apk", unrelatedRecordSourceIds)
        private val oldSource = TestSource(TARGET_SOURCE_ID)
        private val unrelatedSource = TestSource(UNRELATED_SOURCE_ID)
        private val expectedUnrelatedSource = unrelatedSource.takeIf { injectUnrelatedSource }
        private val loader = ExtensionLoader(root.toFile(), signatureVerifier)
        private val directoryBefore: Map<String, ByteArray>
        private val activeFilesBefore: Map<String, ByteArray>
        private val installedBefore: Map<String, InstalledExtension>
        val manager: ExtensionManager

        init {
            oldApk.writeBytes(oldApkBytes)
            oldJar.writeBytes("old target jar".toByteArray())
            unrelatedApk.writeBytes("unrelated apk".toByteArray())
            unrelatedJar.writeBytes("unrelated jar".toByteArray())
            manifest.writeBytes(jacksonObjectMapper().writeValueAsBytes(listOf(targetRecord, unrelatedRecord)))
            injectSource(loader, oldSource)
            expectedUnrelatedSource?.let { injectSource(loader, it) }
            publishInstalled(loader, mapOf(targetRecord.pkgName to targetRecord, unrelatedRecord.pkgName to unrelatedRecord))

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
                    val verifiedSignature =
                        if (verifyCandidateSignature) {
                            signatureVerifier.verifyIdentity(apk)
                        } else {
                            VerifiedApkSignature(candidateSignerFingerprints, emptyList())
                        }
                    PreparedExtension(
                        pkgName = candidatePackage,
                        versionName = "1.2.$candidateVersionCode",
                        versionCode = candidateVersionCode,
                        mainClass = candidateJarSource.name,
                        apkFile = apk,
                        jarFile = jar,
                        signerFingerprints = verifiedSignature.currentSignerFingerprints,
                        signingCertificateLineage = verifiedSignature.signingCertificateLineage,
                    )
                }
            manager =
                if (useRealPreparer) {
                    ExtensionManager(loader, root.toFile(), downloader)
                } else {
                    if (preparedUpdateTtlNanos == null) {
                        ExtensionManager(loader, root.toFile(), downloader, preparer)
                    } else {
                        ExtensionManager(loader, root.toFile(), downloader, preparer, preparedUpdateTtlNanos)
                    }
                }
            manager.setRepos(listOf(REPO_URL))
            configuredSignerFingerprint?.let { manager.setRepoTrust(REPO_URL, it) }
            installedBefore = installedSnapshot(loader)
            directoryBefore = snapshotDirectory(root)
            activeFilesBefore =
                listOf(oldApk, oldJar, unrelatedApk, unrelatedJar).associate { it.fileName.toString() to it.readBytes() }
        }

        fun assertTrustPersisted(expected: String) {
            val reloaded = ExtensionManager(ExtensionLoader(root.toFile(), signatureVerifier), root.toFile())
            assertEquals(expected, reloaded.getRepoTrust().getValue(REPO_URL))
            reloaded.close()
        }

        fun trustFileBytes(): ByteArray = root.resolve("repo-trust.json").readBytes()

        fun withTrustPersistenceBlocked(block: () -> Unit): Result<Unit> {
            val permissions = Files.getPosixFilePermissions(root)
            return try {
                Files.setPosixFilePermissions(root, PosixFilePermissions.fromString("r-x------"))
                runCatching(block)
            } finally {
                Files.setPosixFilePermissions(root, permissions)
            }
        }

        fun assertUnchanged() {
            assertDirectoryEquals(directoryBefore, snapshotDirectory(root))
            assertSame(oldSource, loader.source(TARGET_SOURCE_ID))
            assertSame(expectedUnrelatedSource, loader.source(UNRELATED_SOURCE_ID))
            assertEquals(installedBefore, installedSnapshot(loader))
        }

        fun assertUnrelatedPreserved() {
            assertSame(unrelatedSource, loader.source(UNRELATED_SOURCE_ID))
            assertEquals(unrelatedRecord, manager.recordForSource(UNRELATED_SOURCE_ID))
        }

        fun blockManifestPersistence() {
            Files.delete(manifest)
            Files.createDirectory(manifest)
        }

        fun assertBlockedManifestRollback() {
            activeFilesBefore.forEach { (name, bytes) ->
                assertContentEquals(bytes, root.resolve(name).readBytes(), "$name bytes changed")
            }
            assertSame(oldSource, loader.source(TARGET_SOURCE_ID))
            assertUnrelatedPreserved()
            assertEquals(installedBefore, installedSnapshot(loader))
            assertEquals(
                directoryBefore.keys,
                root.listDirectoryEntries().mapTo(sortedSetOf()) { it.fileName.toString() },
                "manifest failure leaked a staged or replacement file",
            )
        }

        fun armPublicationProbe(): RegistryPublicationProbe.Observation = RegistryPublicationProbe.arm(loader)

        fun targetSource(): Source? = loader.source(TARGET_SOURCE_ID)

        fun oldTargetSource(): Source = oldSource

        fun rpcServer(controlToken: String): RpcServer = RpcServer(loader, manager, port = 0, controlToken = controlToken)
    }

    companion object {
        private const val TARGET_PACKAGE = "example.extension"
        private const val UNRELATED_PACKAGE = "unrelated.extension"
        private const val TARGET_SOURCE_ID = 7L
        private const val UNRELATED_SOURCE_ID = 9L
        private const val RETRY_SOURCE_ID = 11L
        private const val PREFERENCE_SOURCE_ID = 13L
        private const val RETRY_CLASS_NAME = "enginehost.RetrySourceNew"
        private const val TEST_SIGNER = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
        private const val TEST_SIGNER_B = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
        private const val REPO_URL = "https://repo.example.test"
        private const val CONTROL_TOKEN = "prepared-update-control-token"

        private fun boundAddress(rpc: RpcServer): InetSocketAddress {
            val field = RpcServer::class.java.getDeclaredField("server").apply { isAccessible = true }
            return (field.get(rpc) as HttpServer).address
        }

        private data class TrustMutationState(
            val trust: Map<String, String>,
            val repoCache: Map<String, List<RepoIndexEntry>>,
            val repoCacheGeneration: Long,
            val mutationSequence: Long,
        )

        @Suppress("UNCHECKED_CAST")
        private fun trustMutationState(manager: ExtensionManager): TrustMutationState =
            TrustMutationState(
                trust = HashMap(manager.getRepoTrust()),
                repoCache = HashMap(privateField(manager, "repoCache") as Map<String, List<RepoIndexEntry>>),
                repoCacheGeneration = privateField(manager, "repoCacheGeneration") as Long,
                mutationSequence = privateField(manager, "mutationSequence") as Long,
            )

        private fun privateField(
            manager: ExtensionManager,
            name: String,
        ): Any = ExtensionManager::class.java.getDeclaredField(name).apply { isAccessible = true }.get(manager)

        private fun installedSnapshot(loader: ExtensionLoader): Map<String, InstalledExtension> =
            HashMap(loader.snapshotRegistry().installed)

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
        ) = loader.publishTestSources(listOf(source))

        private fun publishInstalled(
            loader: ExtensionLoader,
            installed: Map<String, InstalledExtension>,
        ) {
            val current = loader.snapshotRegistry()
            loader.publishRegistry(loader.prepareRegistry(current.sources, installed))
        }

        private fun assertBootRejected(
            sourceClass: Class<*> = CandidateOwnedSource::class.java,
            inspectedPackage: String = TARGET_PACKAGE,
            inspectedVersionName: String = "1.2.1",
            inspectedVersionCode: Long = 1,
            inspectedMainClass: String = sourceClass.name,
            inspectedSignature: VerifiedApkSignature = VerifiedApkSignature(setOf(TEST_SIGNER), emptyList()),
        ): IllegalArgumentException {
            val root = Files.createTempDirectory("extension-manager-boot-identity")
            val diskRecord =
                installedRecord(TARGET_PACKAGE, "target.apk", listOf(TARGET_SOURCE_ID)).copy(
                    mainClass = sourceClass.name,
                    signerFingerprints = setOf(TEST_SIGNER),
                )
            root.resolve(diskRecord.apkFileName).writeBytes("apk".toByteArray())
            writeClassJar(root.resolve("target.jar"), sourceClass)
            val manifest = root.resolve("installed.json")
            manifest.writeBytes(jacksonObjectMapper().writeValueAsBytes(listOf(diskRecord)))
            val manifestBefore = manifest.readBytes()
            val loader = ExtensionLoader(root.toFile(), ApkSignatureVerifier { setOf(TEST_SIGNER) })
            val previousSource = TestSource(UNRELATED_SOURCE_ID)
            val previousRecord = installedRecord(UNRELATED_PACKAGE, "previous.apk", listOf(UNRELATED_SOURCE_ID))
            loader.publishRegistry(
                loader.prepareRegistry(
                    mapOf(UNRELATED_SOURCE_ID to previousSource),
                    mapOf(UNRELATED_PACKAGE to previousRecord),
                ),
            )
            val manager =
                ExtensionManager(
                    loader,
                    root.toFile(),
                    localApkDownloader(ByteArray(0)),
                    ExtensionPreparer { error("unused") },
                    ExtensionArtifactInspector { apk ->
                        InspectedExtensionArtifact(
                            inspectedPackage,
                            inspectedVersionName,
                            inspectedVersionCode,
                            inspectedMainClass,
                            apk,
                            inspectedSignature,
                        )
                    },
                )

            val failure = assertFailsWith<IllegalArgumentException> { manager.reloadInstalled() }

            assertSame(previousSource, loader.source(UNRELATED_SOURCE_ID))
            assertEquals(mapOf(UNRELATED_PACKAGE to previousRecord), installedSnapshot(loader))
            assertContentEquals(manifestBefore, manifest.readBytes())
            assertTrue(!loader.hasCachedLoader(root.resolve("target.jar")), "rejected boot loader remained cached")
            return failure
        }

        private fun bootManager(
            loader: ExtensionLoader,
            root: Path,
            record: InstalledExtension,
            signature: VerifiedApkSignature,
        ): ExtensionManager =
            ExtensionManager(
                loader,
                root.toFile(),
                localApkDownloader(ByteArray(0)),
                ExtensionPreparer { error("unused") },
                ExtensionArtifactInspector { apk ->
                    InspectedExtensionArtifact(
                        record.pkgName,
                        record.versionName,
                        record.versionCode,
                        record.mainClass,
                        apk,
                        signature,
                    )
                },
            )

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

        private fun writeRenamedClassJar(
            target: Path,
            sourceClass: Class<*>,
            targetClassName: String,
        ) {
            val sourceName = sourceClass.name.replace('.', '/').toByteArray()
            val targetName = targetClassName.replace('.', '/').toByteArray()
            require(sourceName.size == targetName.size)
            val entryName = sourceClass.name.replace('.', '/') + ".class"
            val classBytes = requireNotNull(sourceClass.classLoader.getResourceAsStream(entryName)) { "missing $entryName" }.use { it.readBytes() }
            var replacementCount = 0
            for (index in 0..classBytes.size - sourceName.size) {
                if (classBytes.copyOfRange(index, index + sourceName.size).contentEquals(sourceName)) {
                    targetName.copyInto(classBytes, index)
                    replacementCount++
                }
            }
            require(replacementCount > 0) { "class name was not found in $entryName" }
            ZipOutputStream(Files.newOutputStream(target)).use { jar ->
                jar.putNextEntry(ZipEntry(targetClassName.replace('.', '/') + ".class"))
                jar.write(classBytes)
                jar.closeEntry()
            }
        }

        private fun localApkDownloader(bytes: ByteArray): ExtensionDownloadClient =
            object : ExtensionDownloadClient {
                override fun downloadRepoIndex(url: String): ByteArray = error("unused")

                override fun downloadApk(
                    url: String,
                    targetDir: Path,
                ): Path = Files.createTempFile(targetDir, ".test-download-", ".apk.tmp").also { it.writeBytes(bytes) }
            }
    }

}

internal class TestSource(
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

internal object PreferenceReloadProbe {
    @Volatile
    var sourceIds: List<Long> = emptyList()
}

internal class PreferenceReloadFactory : SourceFactory {
    override fun createSources(): List<Source> = PreferenceReloadProbe.sourceIds.map(::TestSource)
}

internal object ActivationDuplicateProbe {
    private val calls = AtomicInteger()

    fun reset() = calls.set(0)

    fun createSources(): List<Source> =
        if (calls.getAndIncrement() == 0) {
            listOf(CandidateOwnedSource())
        } else {
            listOf(CandidateOwnedSource(), CandidateOwnedSource())
        }
}

internal class ActivationDuplicateFactory : SourceFactory {
    override fun createSources(): List<Source> = ActivationDuplicateProbe.createSources()
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

internal class PublicationObservedSource : Source {
    override val id: Long
        get() = RegistryPublicationProbe.observe(this)
    override val name: String = "Publication Observed Source"
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

internal object RegistryPublicationProbe {
    data class Observation(
        val published: CompletableFuture<Unit>,
        val release: CountDownLatch,
    )

    @Volatile
    private var loader: ExtensionLoader? = null

    @Volatile
    private var observation = Observation(CompletableFuture(), CountDownLatch(0))

    fun arm(loader: ExtensionLoader): Observation =
        Observation(CompletableFuture(), CountDownLatch(1)).also {
            this.loader = loader
            observation = it
        }

    fun observe(source: Source): Long {
        if (loader?.source(7L) === source) {
            observation.published.complete(Unit)
            observation.release.await()
        }
        return 7L
    }
}

internal abstract class RetrySourceBase : Source {
    override val id: Long = 11L
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

internal class RetrySourceOne : RetrySourceBase() {
    override val name: String = "First Retry Source"
}

internal class RetrySourceTwo : RetrySourceBase() {
    override val name: String = "Second Retry Source"
}
