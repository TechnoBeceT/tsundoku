package enginehost

/*
 * Portions adapted in spirit from Suwayomi-Server (Mozilla Public License 2.0):
 *   suwayomi.tachidesk.manga.impl.extension.ExtensionsList / github.NetworkExtensionStore
 * The Exposed-DB coupling of the originals is removed entirely — this is a stateless,
 * volume-backed extension manager: it fetches a repo index in any format [RepoIndexParser]
 * supports (`index.json` wrapper / legacy flat array / `index.pb`), merges it with the installed
 * working-set (APKs on the volume + a JSON manifest), and drives install / update / uninstall via
 * [ExtensionLoader].
 *
 * This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at https://mozilla.org/MPL/2.0/.
 */

import com.fasterxml.jackson.annotation.JsonIgnoreProperties
import com.fasterxml.jackson.databind.ObjectMapper
import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.fasterxml.jackson.module.kotlin.readValue
import eu.kanade.tachiyomi.source.Source
import io.github.oshai.kotlinlogging.KotlinLogging
import java.io.File
import java.io.IOException
import java.io.InputStream
import java.net.URI
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock

/** The persisted record of an installed extension (the volume working-set manifest). */
@JsonIgnoreProperties(ignoreUnknown = true)
data class InstalledExtension(
    val pkgName: String,
    val name: String,
    val versionName: String,
    val versionCode: Long,
    val lang: String,
    val apkFileName: String,
    val mainClass: String,
    val isNsfw: Boolean,
    val iconUrl: String?,
    val repoUrl: String?,
    val sourceIds: List<Long>,
    val sources: List<ExtensionSourceDto>,
    val signerFingerprints: Set<String> = emptySet(),
    val signingCertificateLineage: List<String> = emptyList(),
)

/** One validated, lock-pinned view of the APK backing an installed extension. */
data class InstalledApkExport(
    val pkgName: String,
    val versionCode: Long,
    val versionName: String,
    val contentLength: Long,
    val input: InputStream,
)

internal class InstalledApkUnavailableException(cause: Throwable) : IOException("installed APK is unavailable", cause)

class SourceRetirementConflict(
    val pkgName: String,
    val sourceIds: List<Long>,
) : IllegalStateException("update for '$pkgName' would retire protected source IDs: ${sourceIds.joinToString()}")

/** Repository identity that a prepared APK must match exactly before it can replace active state. */
private data class ExpectedArtifact(
    val pkgName: String,
    val versionCode: Long,
    val trustedSignerFingerprint: String,
) {
    companion object {
        fun from(
            entry: RepoIndexEntry,
            configuredSignerFingerprint: String?,
        ): ExpectedArtifact {
            val trustedSigner =
                requireNotNull(configuredSignerFingerprint) {
                    "repository for '${entry.pkg}' has no configured signer"
                }
            entry.signingKeyFingerprint?.let { advertisedSigner ->
                require(normalizeSignerFingerprint(advertisedSigner) == trustedSigner) {
                    "repository signer for '${entry.pkg}' does not match the configured repository signer"
                }
            }
            return ExpectedArtifact(entry.pkg, entry.code, normalizeSignerFingerprint(trustedSigner))
        }
    }
}

/**
 * ExtensionManager owns the extension working-set on the mounted volume — the configured repo
 * URLs, the installed APKs, and a JSON install-manifest — and drives install/update/uninstall
 * through [ExtensionLoader]. It is DB-free and stateless re: library.
 */
class ExtensionManager internal constructor(
    private val loader: ExtensionLoader,
    private val extensionsRoot: File,
    private val downloadClient: ExtensionDownloadClient,
    private val preparer: ExtensionPreparer,
    private val artifactInspector: ExtensionArtifactInspector,
    private val preparedUpdateTtlNanos: Long = PREPARED_UPDATE_TTL_NANOS,
) : AutoCloseable {
    constructor(loader: ExtensionLoader, extensionsRoot: File) :
        this(
            loader,
            extensionsRoot,
            BoundedDownloadClient(),
            ExtensionPreparer { loader.prepareFromApk(it) },
            ExtensionArtifactInspector { loader.inspectApk(it) },
        )

    internal constructor(
        loader: ExtensionLoader,
        extensionsRoot: File,
        downloadClient: ExtensionDownloadClient,
    ) : this(
        loader,
        extensionsRoot,
        downloadClient,
        ExtensionPreparer { loader.prepareFromApk(it) },
        ExtensionArtifactInspector { loader.inspectApk(it) },
    )

    internal constructor(
        loader: ExtensionLoader,
        extensionsRoot: File,
        downloadClient: ExtensionDownloadClient,
        preparer: ExtensionPreparer,
        preparedUpdateTtlNanos: Long = PREPARED_UPDATE_TTL_NANOS,
    ) : this(
        loader,
        extensionsRoot,
        downloadClient,
        preparer,
        ExtensionArtifactInspector { loader.inspectApk(it) },
        preparedUpdateTtlNanos,
    )

    private val logger = KotlinLogging.logger {}
    private val mapper: ObjectMapper = jacksonObjectMapper()

    private val reposFile = File(extensionsRoot, "repos.json")
    private val repoTrustFile = File(extensionsRoot, "repo-trust.json")
    private val manifestFile = File(extensionsRoot, "installed.json")
    private var manifestRecords: List<InstalledExtension> = emptyList()

    // Per-source SharedPreferences files live at <dataRoot>/settings/source_<id>.xml, a sibling of
    // <dataRoot>/extensions (extensionsRoot). Deleted on uninstall so orphans don't accumulate.
    private val settingsRoot = File(extensionsRoot.parentFile, "settings")

    /**
     * Serializes EVERY mutation (install/update/uninstall/reload/repos) — Suwayomi got this
     * serialization for free from its DB transactions, which we removed. The 8-thread RPC pool would
     * otherwise race the non-thread-safe classloader cache (PackageTools.jarLoaderMap is a plain
     * mutableMapOf) and the `installed`/`sources` maps. Read calls stay concurrent (they're stateless).
     */
    private val mutationLock = ReentrantLock()
    private var mutationSequence = 0L
    private val preparedUpdates = HashMap<String, PreparedUpdate>()
    private val preparedCleanup =
        Executors.newSingleThreadScheduledExecutor { task ->
            Thread(task, "extension-prepared-cleanup").apply { isDaemon = true }
        }
    @Volatile
    private var closed = false

    /** Run [block] under the mutation lock — used by the RPC layer for the preference write+reload path. */
    fun <T> underLock(block: () -> T): T =
        mutationLock.withLock {
            try {
                block()
            } finally {
                mutationSequence++
            }
        }

    /** The installed half of the same immutable generation source readers resolve against. */
    private val installed: Map<String, InstalledExtension>
        get() = loader.snapshotRegistry().installed

    @Volatile
    private var repos: List<String> = DEFAULT_REPOS
    @Volatile
    private var repoTrust: Map<String, String> = DEFAULT_REPO_TRUST
    @Volatile
    private var repoCacheGeneration = 0L

    /** repoUrl -> parsed index (cleared by [refresh]; fetched lazily). */
    private val repoCache = ConcurrentHashMap<String, List<RepoIndexEntry>>()

    init {
        extensionsRoot.mkdirs()
        loadReposFromDisk()
        loadRepoTrustFromDisk()
        manifestRecords = loadManifestFromDisk()
    }

    override fun close() {
        mutationLock.withLock {
            if (closed) return
            closed = true
            preparedUpdates.values.forEach(::cleanupPrepared)
            preparedUpdates.clear()
        }
        preparedCleanup.shutdownNow()
        downloadClient.close()
    }

    // ---- boot ----

    /** Validate and publish every installed extension as one complete boot generation. */
    fun reloadInstalled() = mutationLock.withLock {
        val previous = loader.snapshotRegistry()
        val newLoaderJars = ArrayList<Path>()
        try {
            require(manifestRecords.map { it.pkgName }.toSet().size == manifestRecords.size) {
                "install manifest contains duplicate package records"
            }
            val materialized =
                manifestRecords.map { record ->
                    val apk = installedApkPath(record)
                    val jar = installedJarPath(record)
                    require(Files.isRegularFile(apk)) { "installed APK missing on disk: ${record.apkFileName}" }
                    require(Files.isRegularFile(jar)) { "installed jar missing on disk: ${jar.fileName}" }
                    val inspected = artifactInspector.inspect(apk)
                    val verifiedRecord = verifyBootIdentity(record, inspected)
                    if (!loader.hasCachedLoader(jar)) newLoaderJars.add(jar)
                    val sources = loader.reinstantiate(jar.toString(), verifiedRecord.mainClass)
                    val actualIds = sources.map { it.id }
                    require(actualIds.size == actualIds.toSet().size) {
                        "installed extension '${record.pkgName}' declares duplicate source IDs"
                    }
                    require(record.sourceIds.size == record.sourceIds.toSet().size && actualIds.toSet() == record.sourceIds.toSet()) {
                        "installed extension '${record.pkgName}' source IDs do not match its manifest record"
                    }
                    MaterializedExtension(verifiedRecord.withSources(sources), sources)
                }
            val next = buildRegistryGeneration(previous, materialized, replaceAllInstalled = true)
            val diskInstalled = manifestRecords.associateBy { it.pkgName }
            if (next.installed != diskInstalled) persistManifest(next.installed.values)
            loader.publishRegistry(next)
            mutationSequence++
            materialized.forEach { logger.info { "Reloaded ${it.record.pkgName} (${it.sources.size} source(s))" } }
        } catch (failure: Throwable) {
            newLoaderJars.forEach { jar -> runCatching { loader.evictAndClose(jar) } }
            throw failure
        }
    }

    /** The installed record owning a source id (null if the source came from the CLI bootstrap arg). */
    fun recordForSource(sourceId: Long): InstalledExtension? = installed.values.firstOrNull { sourceId in it.sourceIds }

    /**
     * Validate and stream the exact APK backing [pkgName] while the extension mutation lock is held.
     * The callback must consume [InstalledApkExport.input] before returning.
     */
    internal fun <T> withInstalledApk(
        pkgName: String,
        block: (InstalledApkExport) -> T,
    ): T =
        mutationLock.withLock {
            val record = requireNotNull(installed[pkgName]) { "extension '$pkgName' is not installed" }
            val apk = installedApkPath(record)
            require(Files.isRegularFile(apk, java.nio.file.LinkOption.NOFOLLOW_LINKS)) {
                "installed APK for '$pkgName' is not a regular file"
            }
            val size = filesystemAccess { Files.size(apk) }
            require(size in 1..MAX_EXPORTED_APK_BYTES) {
                "installed APK for '$pkgName' must be between 1 and $MAX_EXPORTED_APK_BYTES bytes"
            }
            filesystemAccess { Files.newInputStream(apk) }.use { input ->
                block(InstalledApkExport(record.pkgName, record.versionCode, record.versionName, size, input))
            }
        }

    private fun <T> filesystemAccess(block: () -> T): T =
        try {
            block()
        } catch (failure: IOException) {
            throw InstalledApkUnavailableException(failure)
        } catch (failure: SecurityException) {
            throw InstalledApkUnavailableException(failure)
        }

    /**
     * Reload the extension that provides [sourceId], so a just-written preference is re-read by a
     * freshly-constructed source instance. Re-instantiates from the EXISTING jar via
     * [ExtensionLoader.reinstantiate] — NO dex2jar / asset-rewrite (which would delete+replace the
     * jar a live classloader still references, and is pure waste). Returns false when the source
     * isn't owned by an installed extension (e.g. the bootstrap APK arg). MUST be called under the
     * mutation lock — the RPC preference handler wraps apply+reload in [underLock].
     */
    fun reloadForSource(sourceId: Long): Boolean =
        reloadForSourceGeneration(sourceId) { _, _ -> Unit } != null

    /**
     * Build and validate a refreshed generation, then derive [prepareResponse] from the refreshed
     * source before persisting or publishing it. The old source ID is preferred when it survives;
     * otherwise exactly one newly introduced source ID must identify the response source.
     */
    internal fun <T : Any> reloadForSource(
        sourceId: Long,
        prepareResponse: (Source) -> T,
    ): T? =
        reloadForSourceGeneration(sourceId) { record, sources ->
            prepareResponse(responseSource(record, sources, sourceId))
        }

    private fun <T : Any> reloadForSourceGeneration(
        sourceId: Long,
        prepareResponse: (InstalledExtension, List<Source>) -> T,
    ): T? {
        val record = recordForSource(sourceId) ?: return null
        val jar = installedJarPath(record)
        require(Files.isRegularFile(jar)) { "installed jar missing on disk: ${jar.fileName}" }
        val previous = loader.snapshotRegistry()
        val loaderWasCached = loader.hasCachedLoader(jar)
        try {
            val sources = loader.reinstantiate(jar.toString(), record.mainClass)
            val next = buildRegistryGeneration(previous, listOf(MaterializedExtension(record.withSources(sources), sources)))
            val response = prepareResponse(record, sources)
            persistManifest(next.installed.values)
            loader.publishRegistry(next)
            return response
        } catch (failure: Throwable) {
            if (!loaderWasCached) runCatching { loader.evictAndClose(jar) }
            throw failure
        }
    }

    private fun responseSource(
        previousRecord: InstalledExtension,
        refreshedSources: List<Source>,
        requestedSourceId: Long,
    ): Source {
        refreshedSources.singleOrNull { it.id == requestedSourceId }?.let { return it }
        val introducedIds = refreshedSources.mapTo(HashSet()) { it.id } - previousRecord.sourceIds.toSet()
        require(introducedIds.size == 1) {
            "refreshed extension does not provide an unambiguous replacement for source ID $requestedSourceId"
        }
        val introducedId = introducedIds.single()
        return refreshedSources.single { it.id == introducedId }
    }

    // ---- repos ----

    fun getRepos(): List<String> = repos

    /** Independently configured repository signer pins, keyed by configured repository URL. */
    fun getRepoTrust(): Map<String, String> = repoTrust

    /** Explicitly approve or rotate the signer pin for one configured repository. */
    fun setRepoTrust(
        repoUrl: String,
        signerFingerprint: String,
    ) = mutationLock.withLock {
        val normalizedUrl = repoUrl.trim()
        require(normalizedUrl in repos) { "repository '$normalizedUrl' is not configured" }
        val normalizedSigner = normalizeSignerFingerprint(signerFingerprint)
        val candidateTrust = repoTrust + (normalizedUrl to normalizedSigner)
        persistRepoTrust(candidateTrust)
        repoTrust = candidateTrust
        repoCache.clear()
        repoCacheGeneration++
        mutationSequence++
        logger.info { "Repository signer trust updated for $normalizedUrl" }
    }

    fun setRepos(newRepos: List<String>) = mutationLock.withLock {
        repos = newRepos.map { it.trim() }.filter { it.isNotBlank() }.distinct()
        repoCache.clear()
        repoCacheGeneration++
        mutationSequence++
        reposFile.writeText(mapper.writeValueAsString(repos))
        logger.info { "Repos updated: $repos" }
    }

    /** Drop the cached repo indexes so the next [list] re-fetches them. */
    fun refresh() = mutationLock.withLock {
        repoCache.clear()
        repoCacheGeneration++
        mutationSequence++
        logger.info { "Repo index cache cleared (will re-fetch on next list)" }
    }

    // ---- listing ----

    /** Merge the installed working-set with everything the repos advertise. */
    fun list(): List<ExtensionDto> {
        val available = availableByPkg()
        val installedSnapshot = installed
        val pkgs = (installedSnapshot.keys + available.keys).toSortedSet()
        return pkgs.map { pkg ->
            val inst = installedSnapshot[pkg]
            val (repoUrl, avail) = available[pkg] ?: (null to null)
            val availCode = avail?.code
            val installedCode = inst?.versionCode
            ExtensionDto(
                pkgName = pkg,
                name = inst?.name ?: avail?.name ?: pkg,
                versionName = inst?.versionName ?: avail?.version ?: "",
                versionCode = installedCode ?: availCode ?: 0,
                lang = inst?.lang ?: avail?.lang ?: "",
                isInstalled = inst != null,
                hasUpdate = inst != null && availCode != null && availCode > inst.versionCode,
                isNsfw = inst?.isNsfw ?: (avail?.nsfw == 1),
                iconUrl = inst?.iconUrl ?: avail?.iconUrl,
                repoUrl = inst?.repoUrl ?: repoUrl,
                sources = inst?.sources ?: avail?.sources?.map { ExtensionSourceDto(it.id, it.name, it.lang) } ?: emptyList(),
            )
        }
    }

    // ---- mutations ----

    /**
     * Install by [pkgName] (resolved from the configured repos) or by a direct [apkUrl].
     * Idempotent-ish: an already-installed pkg is uninstalled first (so this doubles as reinstall).
     */
    fun install(
        pkgName: String? = null,
        apkUrl: String? = null,
    ): List<ExtensionDto> {
        require(pkgName != null || apkUrl != null) { "install requires pkgName or apkUrl" }

        val stateSnapshot = mutationLock.withLock { InstallState(mutationSequence, repos, repoTrust, pkgName?.let { installed[it] }) }
        val (url, repoUrl, repoEntry) =
            if (apkUrl != null) {
                Triple(apkUrl, null, null)
            } else {
                val (rUrl, entry) =
                    findInRepos(pkgName!!, stateSnapshot.repos)
                        ?: throw IllegalArgumentException("pkgName '$pkgName' not found in configured repos")
                // The repo entry carries a fully-resolved absolute apk URL (see [RepoIndexEntry]).
                Triple(entry.apkUrl, rUrl, entry)
            }

        return prepareAndInstall(
            url = url,
            repoUrl = repoUrl,
            repoEntry = repoEntry,
            expectedArtifact = repoEntry?.let { ExpectedArtifact.from(it, stateSnapshot.repoTrust[repoUrl]) },
            expectedRepos = if (apkUrl == null) stateSnapshot.repos else null,
            expectedMutationSequence = stateSnapshot.mutationSequence,
        )
    }

    fun uninstall(pkgName: String): List<ExtensionDto> {
        mutationLock.withLock {
            val previous = loader.snapshotRegistry()
            val record = previous.installed[pkgName] ?: throw IllegalArgumentException("extension '$pkgName' is not installed")
            val nextInstalled = previous.installed - pkgName
            val nextSources = previous.sources - record.sourceIds.toSet()
            val next = loader.prepareRegistry(nextSources, nextInstalled).also(::requireCompleteRegistry)
            persistManifest(next.installed.values)
            loader.publishRegistry(next)
            mutationSequence++
            removeUninstalledFiles(record)
            logger.info { "Uninstalled $pkgName" }
        }
        return list()
    }

    /** Update = reinstall the latest repo version (fails if no newer version is advertised). */
    fun update(pkgName: String): List<ExtensionDto> {
        val stateSnapshot =
            mutationLock.withLock {
                InstallState(
                    mutationSequence = mutationSequence,
                    repos = repos,
                    repoTrust = repoTrust,
                    installed = installed[pkgName] ?: throw IllegalArgumentException("extension '$pkgName' is not installed"),
                )
            }
        val record = requireNotNull(stateSnapshot.installed)
        val (repoUrl, entry) =
            findInRepos(pkgName, stateSnapshot.repos) ?: throw IllegalArgumentException("no repo advertises '$pkgName'")
        require(entry.code > record.versionCode) { "'$pkgName' is already up to date (installed ${record.versionCode}, repo ${entry.code})" }
        logger.info { "Updating $pkgName ${record.versionCode} -> ${entry.code} from $repoUrl" }
        return prepareAndInstall(
            url = entry.apkUrl,
            repoUrl = repoUrl,
            repoEntry = entry,
            expectedArtifact = ExpectedArtifact.from(entry, stateSnapshot.repoTrust[repoUrl]),
            expectedRepos = stateSnapshot.repos,
            expectedMutationSequence = stateSnapshot.mutationSequence,
        )
    }

    /** Download, verify, and inspect the next repo candidate without changing active files or registry state. */
    fun prepareUpdate(pkgName: String): PreparedUpdateDto {
        val stateSnapshot =
            mutationLock.withLock {
                InstallState(
                    mutationSequence = mutationSequence,
                    repos = repos,
                    repoTrust = repoTrust,
                    installed = installed[pkgName] ?: throw IllegalArgumentException("extension '$pkgName' is not installed"),
                )
            }
        val record = requireNotNull(stateSnapshot.installed)
        val (repoUrl, entry) =
            findInRepos(pkgName, stateSnapshot.repos) ?: throw IllegalArgumentException("no repo advertises '$pkgName'")
        require(entry.code > record.versionCode) {
            "'$pkgName' is already up to date (installed ${record.versionCode}, repo ${entry.code})"
        }
        val expectedArtifact = ExpectedArtifact.from(entry, stateSnapshot.repoTrust[repoUrl])
        val stagedApk = stageApk(entry.apkUrl)
        var prepared: PreparedExtension? = null
        try {
            prepared = preparer.prepare(stagedApk)
            validatePreparedIdentity(prepared, expectedArtifact, record)
            val candidateSourceIds = loader.inspectPreparedSourceIds(prepared)
            require(candidateSourceIds.size == candidateSourceIds.toSet().size) {
                "prepared APK '${prepared.pkgName}' declares duplicate source IDs"
            }
            val candidateIds = candidateSourceIds.sorted()
            val installedIds = record.sourceIds.sorted()
            val token = UUID.randomUUID().toString()
            val held =
                PreparedUpdate(
                    token = token,
                    pkgName = pkgName,
                    installedVersionCode = record.versionCode,
                    installedSourceIds = installedIds,
                    candidateSourceIds = candidateIds,
                    removedSourceIds = (installedIds.toSet() - candidateIds.toSet()).sorted(),
                    mutationSequence = stateSnapshot.mutationSequence,
                    expectedRepos = stateSnapshot.repos,
                    repoUrl = repoUrl,
                    repoEntry = entry,
                    expectedArtifact = expectedArtifact,
                    requestedApkFileName = apkFileNameFor(entry.apkUrl),
                    prepared = prepared,
                    expiresAtNanos = System.nanoTime() + preparedUpdateTtlNanos,
                )
            mutationLock.withLock {
                require(!closed) { "extension manager is closed" }
                require(mutationSequence == stateSnapshot.mutationSequence && repos == stateSnapshot.repos) {
                    "extension state changed while preparing the update"
                }
                val current = installed[pkgName]
                require(current?.versionCode == record.versionCode && current.sourceIds.sorted() == installedIds) {
                    "installed extension changed while preparing the update"
                }
                preparedUpdates.put(pkgName, held)?.let(::cleanupPrepared)
                preparedCleanup.schedule(
                    { expirePreparedUpdate(pkgName, token) },
                    preparedUpdateTtlNanos,
                    TimeUnit.NANOSECONDS,
                )
            }
            return held.dto()
        } catch (failure: Throwable) {
            runCatching { Files.deleteIfExists(prepared?.jarFile) }
            runCatching { Files.deleteIfExists(stagedApk) }
            throw failure
        }
    }

    /** Activate only the exact candidate witness returned by [prepareUpdate]. */
    fun activatePreparedUpdate(request: ActivatePreparedUpdateRequest): List<ExtensionDto> {
        mutationLock.withLock {
            val held = preparedUpdates[request.pkgName] ?: throw IllegalArgumentException("prepared update not found")
            try {
                require(System.nanoTime() < held.expiresAtNanos) { "prepared update expired" }
                require(request.token == held.token) { "prepared update token does not match" }
                require(request.pkgName == held.pkgName) { "prepared update package does not match" }
                require(request.installedVersionCode == held.installedVersionCode) { "prepared installed version does not match" }
                require(request.candidateVersionCode == held.prepared.versionCode) { "prepared candidate version does not match" }
                require(request.installedSourceIds.sorted() == held.installedSourceIds) { "prepared installed source IDs do not match" }
                require(request.candidateSourceIds.sorted() == held.candidateSourceIds) { "prepared candidate source IDs do not match" }
                require(request.removedSourceIds.sorted() == held.removedSourceIds) { "prepared removed source IDs do not match" }
                require(request.mutationSequence == held.mutationSequence) { "prepared mutation sequence does not match" }
                require(mutationSequence == held.mutationSequence && repos == held.expectedRepos) {
                    "extension state changed after preparing the update"
                }
                val current = installed[held.pkgName]
                require(current?.versionCode == held.installedVersionCode && current.sourceIds.sorted() == held.installedSourceIds) {
                    "installed extension changed after preparing the update"
                }
                val conflicts = (held.removedSourceIds.toSet() intersect request.protectedSourceIds.toSet()).sorted()
                if (conflicts.isNotEmpty()) throw SourceRetirementConflict(held.pkgName, conflicts)
                applyPrepared(
                    held.prepared,
                    held.requestedApkFileName,
                    held.repoUrl,
                    held.repoEntry,
                    held.expectedArtifact,
                    held.candidateSourceIds,
                )
                mutationSequence++
            } finally {
                preparedUpdates.remove(request.pkgName)?.let(::cleanupPrepared)
            }
        }
        return list()
    }

    /** Explicitly release a prepared candidate without changing installed state. */
    fun discardPreparedUpdate(
        token: String,
        pkgName: String,
    ) = mutationLock.withLock {
        val held = preparedUpdates[pkgName] ?: return@withLock
        require(held.token == token) { "prepared update token does not match" }
        preparedUpdates.remove(pkgName)
        cleanupPrepared(held)
    }

    // ---- internals ----

    private fun removeUninstalledFiles(record: InstalledExtension) {
        // Remove the APK + its derived jar from the volume.
        File(extensionsRoot, record.apkFileName).delete()
        File(extensionsRoot, record.apkFileName.substringBefore(".apk") + ".jar").delete()
        // Remove each source's SharedPreferences file (<dataRoot>/settings/source_<id>.xml) so
        // repeated install/uninstall cycles don't accumulate orphan prefs (matches Suwayomi's key
        // `source_<id>` from ConfigurableSource.preferenceKey()).
        record.sourceIds.forEach { id -> File(settingsRoot, "source_$id.xml").delete() }
    }

    private fun prepareAndInstall(
        url: String,
        repoUrl: String?,
        repoEntry: RepoIndexEntry?,
        expectedArtifact: ExpectedArtifact?,
        expectedRepos: List<String>?,
        expectedMutationSequence: Long,
    ): List<ExtensionDto> {
        val stagedApk = stageApk(url)
        var prepared: PreparedExtension? = null
        try {
            prepared = preparer.prepare(stagedApk)
            mutationLock.withLock {
                require(mutationSequence == expectedMutationSequence) {
                    "extension state changed while preparing the install"
                }
                require(expectedRepos == null || repos == expectedRepos) {
                    "extension repositories changed while preparing the install"
                }
                applyPrepared(prepared, apkFileNameFor(url), repoUrl, repoEntry, expectedArtifact)
                mutationSequence++
            }
        } finally {
            runCatching { Files.deleteIfExists(prepared?.jarFile) }
            runCatching { Files.deleteIfExists(stagedApk) }
        }
        return list()
    }

    private fun stageApk(url: String): Path {
        if (url.startsWith("http://") || url.startsWith("https://")) {
            return downloadClient.downloadApk(url, extensionsRoot.toPath())
        }
        val source = Path.of(url)
        require(Files.exists(source)) { "APK not found: $url" }
        val temporary = Files.createTempFile(extensionsRoot.toPath(), ".extension-local-", ".apk.tmp")
        try {
            Files.copy(source, temporary, StandardCopyOption.REPLACE_EXISTING)
            return temporary
        } catch (failure: Throwable) {
            runCatching { Files.deleteIfExists(temporary) }
            throw failure
        }
    }

    private fun applyPrepared(
        prepared: PreparedExtension,
        requestedApkFileName: String,
        repoUrl: String?,
        repoEntry: RepoIndexEntry?,
        expectedArtifact: ExpectedArtifact?,
        expectedCandidateSourceIds: List<Long>? = null,
    ) {
        expectedArtifact?.let { expected ->
            require(prepared.pkgName == expected.pkgName) {
                "prepared APK package '${prepared.pkgName}' does not match requested package '${expected.pkgName}'"
            }
            require(prepared.versionCode == expected.versionCode) {
                "prepared APK version ${prepared.versionCode} does not match repository version ${expected.versionCode}"
            }
        }

        val replacementPackage = expectedArtifact?.pkgName ?: prepared.pkgName
        val previous = loader.snapshotRegistry()
        val old = previous.installed[replacementPackage]
        if (expectedArtifact != null && old != null) {
            require(prepared.versionCode > old.versionCode) {
                "prepared APK version ${prepared.versionCode} is not newer than installed version ${old.versionCode}"
            }
        }

        require(prepared.signerFingerprints.isNotEmpty()) {
            "prepared APK '${prepared.pkgName}' has no cryptographically verified signer"
        }
        expectedArtifact?.let { expected ->
            require(expected.trustedSignerFingerprint in prepared.signerFingerprints) {
                "prepared APK signer is not trusted by the repository"
            }
        }
        old?.let { installedRecord ->
            val installedSignature = loader.verifyApkSignature(installedApkPath(installedRecord))
            val candidateSignature =
                VerifiedApkSignature(prepared.signerFingerprints, prepared.signingCertificateLineage)
            require(candidateSignature.continuesFrom(installedSignature)) {
                "prepared APK signer does not preserve installed signer continuity"
            }
        }

        val candidateSourceIds = loader.inspectPreparedSourceIds(prepared)
        require(candidateSourceIds.size == candidateSourceIds.toSet().size) {
            "prepared APK '${prepared.pkgName}' declares duplicate source IDs"
        }
        require(expectedCandidateSourceIds == null || candidateSourceIds.sorted() == expectedCandidateSourceIds.sorted()) {
            "prepared APK '${prepared.pkgName}' source IDs changed after inspection"
        }
        candidateSourceIds.forEach { sourceId ->
            val owners = previous.installed.values.filter { sourceId in it.sourceIds }.map { it.pkgName }.toSet()
            require(owners.isEmpty() || owners == setOf(replacementPackage)) {
                "source ID $sourceId is already owned by ${owners.sorted().joinToString(prefix = "'", postfix = "'", separator = "', '")}"
            }
            require(previous.sources[sourceId] == null || owners == setOf(replacementPackage)) {
                "source ID $sourceId is already active without ownership by '$replacementPackage'"
            }
        }
        requireCompleteRegistry(previous)

        val apkTarget = unusedTarget(requestedApkFileName)
        val jarTarget = apkTarget.resolveSibling(apkTarget.fileName.toString().substringBeforeLast('.') + ".jar")
        var apkMoved = false
        var jarMoved = false
        var published = false
        try {
            moveIntoPlace(prepared.apkFile, apkTarget)
            apkMoved = true
            moveIntoPlace(prepared.jarFile, jarTarget)
            jarMoved = true
            val installedPrepared = prepared.copy(apkFile = apkTarget, jarFile = jarTarget)
            val ext = loader.instantiatePrepared(installedPrepared)
            val activatedSourceIds = ext.sources.map { it.id }
            require(activatedSourceIds.size == activatedSourceIds.toSet().size) {
                "prepared APK '${prepared.pkgName}' declares duplicate source IDs during activation"
            }
            require(activatedSourceIds.toSet() == candidateSourceIds.toSet()) {
                "prepared APK '${prepared.pkgName}' source IDs changed during activation"
            }
            val record =
                InstalledExtension(
                    pkgName = ext.pkgName,
                    name = repoEntry?.name ?: ext.sources.firstOrNull()?.name ?: ext.pkgName,
                    versionName = ext.versionName,
                    versionCode = ext.versionCode,
                    lang = repoEntry?.lang ?: (ext.sources.map { it.lang }.toSet().singleOrNull() ?: "all"),
                    apkFileName = apkTarget.fileName.toString(),
                    mainClass = installedPrepared.mainClass,
                    isNsfw = repoEntry?.nsfw == 1,
                    iconUrl = repoEntry?.iconUrl,
                    repoUrl = repoUrl,
                    sourceIds = ext.sources.map { it.id },
                    sources = ext.sources.map { ExtensionSourceDto(it.id, it.name, it.lang) },
                    signerFingerprints = prepared.signerFingerprints,
                    signingCertificateLineage = prepared.signingCertificateLineage,
                )
            val next = buildRegistryGeneration(previous, listOf(MaterializedExtension(record, ext.sources)))
            val replacedSources = old?.sourceIds.orEmpty().mapNotNull(previous.sources::get)
            persistManifest(next.installed.values)
            loader.publishRegistry(next)
            published = true
            old?.let { oldRecord ->
                runCatching { removeSupersededFiles(oldRecord, record, replacedSources) }
                    .onFailure { logger.warn(it) { "Failed to retire superseded files for ${oldRecord.pkgName}" } }
            }
            logger.info { "Installed ${ext.pkgName} v${ext.versionName} (${ext.sources.size} source(s))" }
        } catch (failure: Throwable) {
            if (!published && jarMoved) {
                runCatching { loader.evictAndClose(jarTarget) }
                runCatching { Files.deleteIfExists(jarTarget) }
            }
            if (!published && apkMoved) runCatching { Files.deleteIfExists(apkTarget) }
            throw failure
        }
    }

    private fun validatePreparedIdentity(
        prepared: PreparedExtension,
        expected: ExpectedArtifact,
        installedRecord: InstalledExtension,
    ) {
        require(prepared.pkgName == expected.pkgName) {
            "prepared APK package '${prepared.pkgName}' does not match requested package '${expected.pkgName}'"
        }
        require(prepared.versionCode == expected.versionCode) {
            "prepared APK version ${prepared.versionCode} does not match repository version ${expected.versionCode}"
        }
        require(prepared.versionCode > installedRecord.versionCode) {
            "prepared APK version ${prepared.versionCode} is not newer than installed version ${installedRecord.versionCode}"
        }
        require(prepared.signerFingerprints.isNotEmpty() && expected.trustedSignerFingerprint in prepared.signerFingerprints) {
            "prepared APK signer is not trusted by the repository"
        }
        val installedSignature = loader.verifyApkSignature(installedApkPath(installedRecord))
        require(VerifiedApkSignature(prepared.signerFingerprints, prepared.signingCertificateLineage).continuesFrom(installedSignature)) {
            "prepared APK signer does not preserve installed signer continuity"
        }
    }

    private fun expirePreparedUpdate(
        pkgName: String,
        token: String,
    ) = mutationLock.withLock {
        val held = preparedUpdates[pkgName] ?: return@withLock
        if (held.token == token && System.nanoTime() >= held.expiresAtNanos) {
            preparedUpdates.remove(pkgName)
            cleanupPrepared(held)
        }
    }

    private fun cleanupPrepared(held: PreparedUpdate) {
        runCatching { loader.evictAndClose(held.prepared.jarFile) }
        runCatching { Files.deleteIfExists(held.prepared.jarFile) }
        runCatching { Files.deleteIfExists(held.prepared.apkFile) }
    }

    private data class PreparedUpdate(
        val token: String,
        val pkgName: String,
        val installedVersionCode: Long,
        val installedSourceIds: List<Long>,
        val candidateSourceIds: List<Long>,
        val removedSourceIds: List<Long>,
        val mutationSequence: Long,
        val expectedRepos: List<String>,
        val repoUrl: String,
        val repoEntry: RepoIndexEntry,
        val expectedArtifact: ExpectedArtifact,
        val requestedApkFileName: String,
        val prepared: PreparedExtension,
        val expiresAtNanos: Long,
    ) {
        fun dto() =
            PreparedUpdateDto(
                token,
                pkgName,
                installedVersionCode,
                prepared.versionCode,
                installedSourceIds,
                candidateSourceIds,
                removedSourceIds,
                mutationSequence,
            )
    }

    private data class MaterializedExtension(
        val record: InstalledExtension,
        val sources: List<Source>,
    )

    private fun InstalledExtension.withSources(sources: List<Source>): InstalledExtension =
        copy(
            sourceIds = sources.map { it.id },
            sources = sources.map { ExtensionSourceDto(it.id, it.name, it.lang) },
        )

    /** Build and validate one complete immutable registry generation without publishing it. */
    private fun buildRegistryGeneration(
        previous: ExtensionRegistrySnapshot,
        materialized: Collection<MaterializedExtension>,
        replaceAllInstalled: Boolean = false,
    ): ExtensionRegistrySnapshot {
        val replacementPackages = materialized.map { it.record.pkgName }
        require(replacementPackages.size == replacementPackages.toSet().size) {
            "registry generation contains duplicate package replacements"
        }

        val removedSourceIds =
            if (replaceAllInstalled) {
                previous.installed.values.flatMapTo(HashSet()) { it.sourceIds }
            } else {
                replacementPackages.flatMapTo(HashSet()) { previous.installed[it]?.sourceIds.orEmpty() }
            }
        val nextSources = HashMap(previous.sources).apply { removedSourceIds.forEach(::remove) }
        val nextInstalled =
            if (replaceAllInstalled) {
                HashMap()
            } else {
                HashMap(previous.installed).apply { replacementPackages.forEach(::remove) }
            }

        materialized.forEach { extension ->
            val record = extension.record
            val sourceIds = extension.sources.map { it.id }
            require(sourceIds.size == sourceIds.toSet().size) {
                "installed extension '${record.pkgName}' declares duplicate source IDs"
            }
            require(record.sourceIds == sourceIds && record.sources.map { it.id } == sourceIds) {
                "installed extension '${record.pkgName}' record does not match its materialized source IDs"
            }
            require(record.pkgName !in nextInstalled) { "installed package '${record.pkgName}' is duplicated" }
            sourceIds.forEach { sourceId ->
                val owner = nextInstalled.values.firstOrNull { sourceId in it.sourceIds }?.pkgName
                require(nextSources[sourceId] == null) {
                    if (owner == null) {
                        "source ID $sourceId is already active without an installed owner"
                    } else {
                        "source ID $sourceId is already owned by '$owner'"
                    }
                }
            }
            extension.sources.forEach { source -> nextSources[source.id] = source }
            nextInstalled[record.pkgName] = record
        }

        return loader.prepareRegistry(nextSources, nextInstalled).also(::requireCompleteRegistry)
    }

    private fun requireCompleteRegistry(snapshot: ExtensionRegistrySnapshot) {
        snapshot.sources.forEach { (sourceId, source) ->
            require(source.id == sourceId) { "runtime source key $sourceId does not match source ID ${source.id}" }
        }
        snapshot.installed.forEach { (pkgName, record) ->
            require(record.pkgName == pkgName) { "installed package key '$pkgName' does not match its record" }
            require(record.sourceIds.size == record.sourceIds.toSet().size) {
                "installed package '$pkgName' declares duplicate source IDs"
            }
            require(record.sources.map { it.id } == record.sourceIds) {
                "installed package '$pkgName' source descriptors do not match its source IDs"
            }
        }
        val owners = sourceOwners(snapshot.installed.values)
        owners.forEach { (sourceId, packages) ->
            require(packages.size == 1) {
                "installed unrelated source ID $sourceId is not owned exclusively by one package: " +
                    packages.sorted().joinToString()
            }
            require(snapshot.sources[sourceId] != null) {
                "installed unrelated source ID $sourceId is missing from the runtime registry"
            }
        }
    }

    private fun sourceOwners(records: Collection<InstalledExtension>): Map<Long, Set<String>> =
        buildMap<Long, MutableSet<String>> {
            records.forEach { record ->
                record.sourceIds.forEach { sourceId -> getOrPut(sourceId) { mutableSetOf() }.add(record.pkgName) }
            }
        }

    private fun verifyBootIdentity(
        record: InstalledExtension,
        inspected: InspectedExtensionArtifact,
    ): InstalledExtension {
        require(inspected.apkFile.toAbsolutePath().normalize() == installedApkPath(record)) {
            "installed APK inspector returned the wrong file for '${record.pkgName}'"
        }
        require(inspected.pkgName == record.pkgName) {
            "installed APK package '${inspected.pkgName}' does not match manifest package '${record.pkgName}'"
        }
        require(inspected.versionCode == record.versionCode && inspected.versionName == record.versionName) {
            "installed APK version ${inspected.versionName} (${inspected.versionCode}) does not match manifest version " +
                "${record.versionName} (${record.versionCode})"
        }
        require(inspected.mainClass == record.mainClass) {
            "installed APK main class '${inspected.mainClass}' does not match manifest main class '${record.mainClass}'"
        }

        val currentSigners = inspected.signature.currentSignerFingerprints.mapTo(HashSet(), ::normalizeSignerFingerprint)
        val currentLineage = inspected.signature.signingCertificateLineage.map(::normalizeSignerFingerprint)
        require(currentSigners.isNotEmpty()) { "installed APK '${record.pkgName}' has no cryptographically verified signer" }
        val recordedSigners = record.signerFingerprints.mapTo(HashSet(), ::normalizeSignerFingerprint)
        val recordedLineage = record.signingCertificateLineage.map(::normalizeSignerFingerprint)
        val exactSigner = recordedSigners.isNotEmpty() && currentSigners == recordedSigners
        val approvedPin = record.repoUrl?.let(repoTrust::get)
        val approvedDescendant =
            recordedSigners.isNotEmpty() &&
                approvedPin != null &&
                approvedPin in currentSigners &&
                VerifiedApkSignature(currentSigners, currentLineage).continuesFrom(
                    VerifiedApkSignature(recordedSigners, recordedLineage),
                )
        require(recordedSigners.isEmpty() || exactSigner || approvedDescendant) {
            "installed APK signer for '${record.pkgName}' does not match its manifest identity or an approved lineage"
        }
        return record.copy(
            signerFingerprints = currentSigners,
            signingCertificateLineage = currentLineage,
        )
    }

    private fun installedApkPath(record: InstalledExtension): Path {
        val root = extensionsRoot.toPath().toAbsolutePath().normalize()
        val relative = Path.of(record.apkFileName)
        require(relative.fileName?.toString() == record.apkFileName) {
            "installed APK filename '${record.apkFileName}' is not a plain filename"
        }
        return root.resolve(relative).normalize().also { path ->
            require(path.parent == root) { "installed APK path escapes the extensions directory" }
        }
    }

    private fun installedJarPath(record: InstalledExtension): Path {
        require(record.apkFileName.endsWith(".apk", ignoreCase = true)) {
            "installed APK filename '${record.apkFileName}' does not end in .apk"
        }
        return installedApkPath(record).resolveSibling(record.apkFileName.dropLast(4) + ".jar")
    }

    private fun removeSupersededFiles(
        old: InstalledExtension,
        replacement: InstalledExtension,
        replacedSources: Collection<Source>,
    ) {
        if (old.apkFileName == replacement.apkFileName) return
        installedApkPath(old).toFile().delete()
        loader.retire(installedJarPath(old), replacedSources)
    }

    private fun apkFileNameFor(url: String): String {
        val raw =
            if (url.startsWith("http://") || url.startsWith("https://")) {
                URI(url).path.substringAfterLast('/')
            } else {
                Path.of(url).fileName.toString()
            }
        return raw.takeIf { it.endsWith(".apk", ignoreCase = true) } ?: "extension.apk"
    }

    private fun unusedTarget(requestedName: String): Path {
        val root = extensionsRoot.toPath()
        val requested = root.resolve(Path.of(requestedName).fileName.toString())
        if (!Files.exists(requested) && !Files.exists(requested.resolveSibling(requested.fileName.toString().substringBeforeLast('.') + ".jar"))) {
            return requested
        }
        val stem = requested.fileName.toString().substringBeforeLast('.')
        return root.resolve("$stem-${UUID.randomUUID()}.apk")
    }

    private fun moveIntoPlace(
        source: Path,
        target: Path,
    ) {
        try {
            Files.move(source, target, StandardCopyOption.ATOMIC_MOVE)
        } catch (_: AtomicMoveNotSupportedException) {
            Files.move(source, target)
        }
    }

    private data class InstallState(
        val mutationSequence: Long,
        val repos: List<String>,
        val repoTrust: Map<String, String>,
        val installed: InstalledExtension?,
    )

    private fun persistRepoTrust(trust: Map<String, String>) {
        val temporary = Files.createTempFile(extensionsRoot.toPath(), ".repo-trust-", ".json.tmp")
        try {
            Files.writeString(temporary, mapper.writeValueAsString(trust))
            try {
                Files.move(temporary, repoTrustFile.toPath(), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
            } catch (_: AtomicMoveNotSupportedException) {
                Files.move(temporary, repoTrustFile.toPath(), StandardCopyOption.REPLACE_EXISTING)
            }
        } finally {
            Files.deleteIfExists(temporary)
        }
    }

    private fun persistManifest(records: Collection<InstalledExtension> = installed.values) {
        val persistedRecords = records.toList()
        val temporary = Files.createTempFile(extensionsRoot.toPath(), ".installed-", ".json.tmp")
        try {
            Files.writeString(temporary, mapper.writeValueAsString(persistedRecords))
            try {
                Files.move(temporary, manifestFile.toPath(), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
            } catch (_: AtomicMoveNotSupportedException) {
                Files.move(temporary, manifestFile.toPath(), StandardCopyOption.REPLACE_EXISTING)
            }
            manifestRecords = persistedRecords
        } finally {
            Files.deleteIfExists(temporary)
        }
    }

    private fun loadManifestFromDisk(): List<InstalledExtension> {
        if (!manifestFile.exists()) return emptyList()
        return try {
            mapper.readValue(manifestFile.readText())
        } catch (failure: Exception) {
            throw IllegalArgumentException("install manifest is corrupt", failure)
        }
    }

    private fun loadReposFromDisk() {
        if (!reposFile.exists()) return
        runCatching { mapper.readValue<List<String>>(reposFile.readText()) }
            .onSuccess { if (it.isNotEmpty()) repos = it }
            .onFailure { logger.warn(it) { "Corrupt repos.json, using defaults" } }
    }

    private fun loadRepoTrustFromDisk() {
        if (!repoTrustFile.exists()) return
        runCatching {
            mapper.readValue<Map<String, String>>(repoTrustFile.readText()).mapKeys { it.key.trim() }.mapValues {
                normalizeSignerFingerprint(it.value)
            }
        }.onSuccess { repoTrust = DEFAULT_REPO_TRUST + it }
            .onFailure { logger.warn(it) { "Corrupt repo-trust.json, using default trust" } }
    }

    /** pkg -> (repoUrl, best repo entry across all repos). */
    private fun availableByPkg(repoUrls: List<String> = repos): Map<String, Pair<String, RepoIndexEntry>> {
        val best = HashMap<String, Pair<String, RepoIndexEntry>>()
        repoUrls.forEach { repoUrl ->
            fetchIndex(repoUrl).forEach { entry ->
                val existing = best[entry.pkg]
                if (existing == null || entry.code > existing.second.code) {
                    best[entry.pkg] = repoUrl to entry
                }
            }
        }
        return best
    }

    private fun findInRepos(
        pkgName: String,
        repoUrls: List<String> = repos,
    ): Pair<String, RepoIndexEntry>? = availableByPkg(repoUrls)[pkgName]

    private fun fetchIndex(repoUrl: String): List<RepoIndexEntry> {
        repoCache[repoUrl]?.let { return it }
        val generation = repoCacheGeneration
        val indexUrl = indexUrlFor(repoUrl)
        val parsed =
            runCatching {
                RepoIndexParser.parse(downloadClient.downloadRepoIndex(indexUrl), indexUrl, repoBaseFor(repoUrl), mapper)
            }.onFailure { logger.warn(it) { "Failed to fetch repo index $indexUrl" } }
                .getOrDefault(emptyList())
        mutationLock.withLock {
            if (generation == repoCacheGeneration && repoUrl in repos) repoCache.putIfAbsent(repoUrl, parsed)
        }
        return parsed
    }

    /** A repo URL that already names an index file (`.json`/`.pb`) is fetched verbatim; a bare base gets the default. */
    private fun indexUrlFor(repoUrl: String): String =
        if (namesIndexFile(repoUrl)) repoUrl else "${repoUrl.trimEnd('/')}/index.min.json"

    /** The repo's base (the parent of the index file) — used only to resolve the LEGACY schema's relative filenames. */
    private fun repoBaseFor(repoUrl: String): String =
        if (namesIndexFile(repoUrl)) repoUrl.substringBeforeLast('/') else repoUrl.trimEnd('/')

    private fun namesIndexFile(repoUrl: String): Boolean = repoUrl.endsWith(".json") || repoUrl.endsWith(".pb")

    companion object {
        private const val MAX_EXPORTED_APK_BYTES: Long = 256L * 1024 * 1024
        private const val PREPARED_UPDATE_TTL_SECONDS = 5 * 60L
        private val PREPARED_UPDATE_TTL_NANOS = TimeUnit.SECONDS.toNanos(PREPARED_UPDATE_TTL_SECONDS)

        /**
         * The standard community repo, pre-configured so a fresh host is usable immediately. Points at
         * the full `index.json` (the new wrapper schema): `index.min.json` was neutered to a 2-entry
         * deprecation stub, so a bare-base default would see no real extensions (GAP-145).
         */
        private const val DEFAULT_REPO = "https://raw.githubusercontent.com/keiyoushi/extensions/repo/index.json"
        private const val DEFAULT_REPO_SIGNER = "9add655a78e96c4ec7a53ef89dccb557cb5d767489fac5e785d671a5a75d4da2"
        val DEFAULT_REPOS = listOf(DEFAULT_REPO)
        val DEFAULT_REPO_TRUST = mapOf(DEFAULT_REPO to DEFAULT_REPO_SIGNER)
    }
}
