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
import io.github.oshai.kotlinlogging.KotlinLogging
import java.io.File
import java.net.URI
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
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
)

/** Repository identity that a prepared APK must match exactly before it can replace active state. */
private data class ExpectedArtifact(
    val pkgName: String,
    val versionCode: Long,
)

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
) : AutoCloseable {
    constructor(loader: ExtensionLoader, extensionsRoot: File) :
        this(loader, extensionsRoot, BoundedDownloadClient(), ExtensionPreparer { loader.prepareFromApk(it) })

    internal constructor(
        loader: ExtensionLoader,
        extensionsRoot: File,
        downloadClient: ExtensionDownloadClient,
    ) : this(loader, extensionsRoot, downloadClient, ExtensionPreparer { loader.prepareFromApk(it) })

    private val logger = KotlinLogging.logger {}
    private val mapper: ObjectMapper = jacksonObjectMapper()

    private val reposFile = File(extensionsRoot, "repos.json")
    private val manifestFile = File(extensionsRoot, "installed.json")

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

    /** Run [block] under the mutation lock — used by the RPC layer for the preference write+reload path. */
    fun <T> underLock(block: () -> T): T =
        mutationLock.withLock {
            try {
                block()
            } finally {
                mutationSequence++
            }
        }

    /** pkgName -> installed record. */
    private val installed = ConcurrentHashMap<String, InstalledExtension>()

    @Volatile
    private var repos: List<String> = DEFAULT_REPOS
    @Volatile
    private var repoCacheGeneration = 0L

    /** repoUrl -> parsed index (cleared by [refresh]; fetched lazily). */
    private val repoCache = ConcurrentHashMap<String, List<RepoIndexEntry>>()

    init {
        extensionsRoot.mkdirs()
        loadReposFromDisk()
        loadManifestFromDisk()
    }

    override fun close() = downloadClient.close()

    // ---- boot ----

    /** Re-instantiate every installed extension's sources from the volume APKs (called on boot). */
    fun reloadInstalled() = mutationLock.withLock {
        installed.values.forEach { record ->
            val apk = File(extensionsRoot, record.apkFileName)
            if (!apk.exists()) {
                logger.warn { "Installed APK missing on disk, skipping: ${record.apkFileName}" }
                return@forEach
            }
            runCatching { loader.loadFromApk(apk.absolutePath) }
                .onFailure { logger.error(it) { "Failed to reload installed extension ${record.pkgName}" } }
                .onSuccess { logger.info { "Reloaded ${record.pkgName} (${it.sources.size} source(s))" } }
        }
        mutationSequence++
    }

    /** The installed record owning a source id (null if the source came from the CLI bootstrap arg). */
    fun recordForSource(sourceId: Long): InstalledExtension? = installed.values.firstOrNull { sourceId in it.sourceIds }

    /**
     * Reload the extension that provides [sourceId], so a just-written preference is re-read by a
     * freshly-constructed source instance. Re-instantiates from the EXISTING jar via
     * [ExtensionLoader.reinstantiate] — NO dex2jar / asset-rewrite (which would delete+replace the
     * jar a live classloader still references, and is pure waste). Returns false when the source
     * isn't owned by an installed extension (e.g. the bootstrap APK arg). MUST be called under the
     * mutation lock — the RPC preference handler wraps apply+reload in [underLock].
     */
    fun reloadForSource(sourceId: Long): Boolean {
        val record = recordForSource(sourceId) ?: return false
        val jar = File(extensionsRoot, record.apkFileName.substringBefore(".apk") + ".jar")
        if (!jar.exists()) return false
        loader.reinstantiate(jar.absolutePath, record.mainClass)
        return true
    }

    // ---- repos ----

    fun getRepos(): List<String> = repos

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
        val pkgs = (installed.keys + available.keys).toSortedSet()
        return pkgs.map { pkg ->
            val inst = installed[pkg]
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

        val stateSnapshot = mutationLock.withLock { InstallState(mutationSequence, repos, pkgName?.let { installed[it] }) }
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
            expectedArtifact = repoEntry?.let { ExpectedArtifact(it.pkg, it.code) },
            expectedRepos = if (apkUrl == null) stateSnapshot.repos else null,
            expectedMutationSequence = stateSnapshot.mutationSequence,
        )
    }

    fun uninstall(pkgName: String): List<ExtensionDto> {
        mutationLock.withLock {
            val record = installed[pkgName] ?: throw IllegalArgumentException("extension '$pkgName' is not installed")
            uninstallRecord(record)
            mutationSequence++
            persistManifest()
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
            expectedArtifact = ExpectedArtifact(entry.pkg, entry.code),
            expectedRepos = stateSnapshot.repos,
            expectedMutationSequence = stateSnapshot.mutationSequence,
        )
    }

    // ---- internals ----

    private fun uninstallRecord(record: InstalledExtension) {
        loader.unload(record.sourceIds)
        installed.remove(record.pkgName)
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
        val old = installed[replacementPackage]
        if (expectedArtifact != null && old != null) {
            require(prepared.versionCode > old.versionCode) {
                "prepared APK version ${prepared.versionCode} is not newer than installed version ${old.versionCode}"
            }
        }

        val candidateSourceIds = loader.inspectPreparedSourceIds(prepared)
        require(candidateSourceIds.size == candidateSourceIds.toSet().size) {
            "prepared APK '${prepared.pkgName}' declares duplicate source IDs"
        }
        candidateSourceIds.forEach { sourceId ->
            val owner = installed.values.firstOrNull { sourceId in it.sourceIds }
            require(owner == null || owner.pkgName == replacementPackage) {
                "source ID $sourceId is already owned by '${owner?.pkgName}'"
            }
        }

        val apkTarget = unusedTarget(requestedApkFileName)
        val jarTarget = apkTarget.resolveSibling(apkTarget.fileName.toString().substringBeforeLast('.') + ".jar")
        var apkMoved = false
        var jarMoved = false
        try {
            moveIntoPlace(prepared.apkFile, apkTarget)
            apkMoved = true
            moveIntoPlace(prepared.jarFile, jarTarget)
            jarMoved = true
            val installedPrepared = prepared.copy(apkFile = apkTarget, jarFile = jarTarget)
            val ext = loader.instantiatePrepared(installedPrepared)
            require(ext.sources.map { it.id }.toSet() == candidateSourceIds.toSet()) {
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
                )
            val nextInstalled = installed.toMutableMap().apply { put(record.pkgName, record) }
            val affectedSourceIds = (old?.sourceIds.orEmpty() + ext.sources.map { it.id }).toSet()
            val sourceSnapshot = loader.snapshotSources(affectedSourceIds)
            try {
                loader.registerReplacement(old?.sourceIds.orEmpty(), ext.sources)
                installed[record.pkgName] = record
                persistManifest(nextInstalled.values)
            } catch (failure: Throwable) {
                loader.restoreSources(affectedSourceIds, sourceSnapshot)
                if (old == null) installed.remove(record.pkgName) else installed[record.pkgName] = old
                throw failure
            }
            old?.let { removeSupersededFiles(it, record) }
            logger.info { "Installed ${ext.pkgName} v${ext.versionName} (${ext.sources.size} source(s))" }
        } catch (failure: Throwable) {
            if (jarMoved) runCatching { Files.deleteIfExists(jarTarget) }
            if (apkMoved) runCatching { Files.deleteIfExists(apkTarget) }
            throw failure
        }
    }

    private fun removeSupersededFiles(
        old: InstalledExtension,
        replacement: InstalledExtension,
    ) {
        if (old.apkFileName == replacement.apkFileName) return
        File(extensionsRoot, old.apkFileName).delete()
        File(extensionsRoot, old.apkFileName.substringBefore(".apk") + ".jar").delete()
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
        val installed: InstalledExtension?,
    )

    private fun persistManifest(records: Collection<InstalledExtension> = installed.values) {
        val temporary = Files.createTempFile(extensionsRoot.toPath(), ".installed-", ".json.tmp")
        try {
            Files.writeString(temporary, mapper.writeValueAsString(records.toList()))
            try {
                Files.move(temporary, manifestFile.toPath(), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
            } catch (_: AtomicMoveNotSupportedException) {
                Files.move(temporary, manifestFile.toPath(), StandardCopyOption.REPLACE_EXISTING)
            }
        } finally {
            Files.deleteIfExists(temporary)
        }
    }

    private fun loadManifestFromDisk() {
        if (!manifestFile.exists()) return
        runCatching { mapper.readValue<List<InstalledExtension>>(manifestFile.readText()) }
            .onSuccess { it.forEach { rec -> installed[rec.pkgName] = rec } }
            .onFailure { logger.warn(it) { "Corrupt install manifest, starting empty" } }
    }

    private fun loadReposFromDisk() {
        if (!reposFile.exists()) return
        runCatching { mapper.readValue<List<String>>(reposFile.readText()) }
            .onSuccess { if (it.isNotEmpty()) repos = it }
            .onFailure { logger.warn(it) { "Corrupt repos.json, using defaults" } }
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
        /**
         * The standard community repo, pre-configured so a fresh host is usable immediately. Points at
         * the full `index.json` (the new wrapper schema): `index.min.json` was neutered to a 2-entry
         * deprecation stub, so a bare-base default would see no real extensions (GAP-145).
         */
        val DEFAULT_REPOS = listOf("https://raw.githubusercontent.com/keiyoushi/extensions/repo/index.json")
    }
}
