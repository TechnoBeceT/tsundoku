package enginehost

/*
 * Portions adapted in spirit from Suwayomi-Server (Mozilla Public License 2.0):
 *   suwayomi.tachidesk.manga.impl.extension.ExtensionsList / github.NetworkExtensionStore
 * The Exposed-DB coupling of the originals is removed entirely — this is a stateless,
 * volume-backed extension manager: it fetches the standard Mihon `index.min.json` from the
 * configured repos, merges it with the installed working-set (APKs on the volume + a JSON
 * manifest), and drives install / update / uninstall via [ExtensionLoader].
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
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock

/** One extension entry as advertised by a repo's `index.min.json` (unknown fields ignored). */
@JsonIgnoreProperties(ignoreUnknown = true)
private data class RepoIndexEntry(
    val name: String,
    val pkg: String,
    val apk: String,
    val lang: String,
    val code: Long,
    val version: String,
    val nsfw: Int = 0,
    val sources: List<RepoIndexSource> = emptyList(),
)

@JsonIgnoreProperties(ignoreUnknown = true)
private data class RepoIndexSource(
    val name: String,
    val lang: String,
    val id: String,
    val baseUrl: String? = null,
)

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

/**
 * ExtensionManager owns the extension working-set on the mounted volume — the configured repo
 * URLs, the installed APKs, and a JSON install-manifest — and drives install/update/uninstall
 * through [ExtensionLoader]. It is DB-free and stateless re: library.
 */
class ExtensionManager(
    private val loader: ExtensionLoader,
    private val extensionsRoot: File,
) {
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

    /** Run [block] under the mutation lock — used by the RPC layer for the preference write+reload path. */
    fun <T> underLock(block: () -> T): T = mutationLock.withLock(block)

    /** pkgName -> installed record. */
    private val installed = ConcurrentHashMap<String, InstalledExtension>()

    @Volatile
    private var repos: List<String> = DEFAULT_REPOS

    /** repoUrl -> parsed index (cleared by [refresh]; fetched lazily). */
    private val repoCache = ConcurrentHashMap<String, List<RepoIndexEntry>>()

    init {
        extensionsRoot.mkdirs()
        loadReposFromDisk()
        loadManifestFromDisk()
    }

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
        reposFile.writeText(mapper.writeValueAsString(repos))
        logger.info { "Repos updated: $repos" }
    }

    /** Drop the cached repo indexes so the next [list] re-fetches them. */
    fun refresh() = mutationLock.withLock {
        repoCache.clear()
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
                iconUrl = inst?.iconUrl ?: avail?.let { iconUrlFor(repoUrl!!, it.pkg) },
                repoUrl = inst?.repoUrl ?: repoUrl,
                sources = inst?.sources ?: avail?.sources?.map { ExtensionSourceDto(it.id.toLong(), it.name, it.lang) } ?: emptyList(),
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
    ): List<ExtensionDto> = mutationLock.withLock {
        require(pkgName != null || apkUrl != null) { "install requires pkgName or apkUrl" }

        val (url, repoUrl, repoEntry) =
            if (apkUrl != null) {
                Triple(apkUrl, null, null)
            } else {
                val (rUrl, entry) = findInRepos(pkgName!!) ?: throw IllegalArgumentException("pkgName '$pkgName' not found in configured repos")
                Triple(apkUrlFor(rUrl, entry.apk), rUrl, entry)
            }

        // Ensure the APK bytes live on the volume so the extension survives a restart. A local
        // path is copied in; an http URL is downloaded by the loader into extensionsRoot.
        val volumeUrl = ensureOnVolume(url)
        val ext = loader.loadFromApk(volumeUrl)

        // Reinstall/update: clean up the previous version of this pkg (its now-superseded APK/jar
        // and any source ids the new version dropped). loadFromApk already re-registered the new
        // sources, so we only tidy the delta.
        installed[ext.pkgName]?.let { old ->
            val newApk = File(volumeUrl).name
            if (old.apkFileName != newApk) {
                File(extensionsRoot, old.apkFileName).delete()
                File(extensionsRoot, old.apkFileName.substringBefore(".apk") + ".jar").delete()
            }
            loader.unload(old.sourceIds - ext.sources.map { it.id }.toSet())
        }
        val record =
            InstalledExtension(
                pkgName = ext.pkgName,
                name = repoEntry?.name ?: ext.sources.firstOrNull()?.name ?: ext.pkgName,
                versionName = ext.versionName,
                versionCode = ext.versionCode,
                lang = repoEntry?.lang ?: (ext.sources.map { it.lang }.toSet().singleOrNull() ?: "all"),
                apkFileName = File(volumeUrl).name,
                mainClass = ext.mainClass,
                isNsfw = repoEntry?.nsfw == 1,
                iconUrl = if (repoUrl != null && repoEntry != null) iconUrlFor(repoUrl, repoEntry.pkg) else null,
                repoUrl = repoUrl,
                sourceIds = ext.sources.map { it.id },
                sources = ext.sources.map { ExtensionSourceDto(it.id, it.name, it.lang) },
            )
        installed[ext.pkgName] = record
        persistManifest()
        logger.info { "Installed ${ext.pkgName} v${ext.versionName} (${ext.sources.size} source(s))" }
        list()
    }

    fun uninstall(pkgName: String): List<ExtensionDto> = mutationLock.withLock {
        val record = installed[pkgName] ?: throw IllegalArgumentException("extension '$pkgName' is not installed")
        uninstallRecord(record)
        persistManifest()
        logger.info { "Uninstalled $pkgName" }
        list()
    }

    /** Update = reinstall the latest repo version (fails if no newer version is advertised). */
    fun update(pkgName: String): List<ExtensionDto> = mutationLock.withLock {
        val record = installed[pkgName] ?: throw IllegalArgumentException("extension '$pkgName' is not installed")
        val (repoUrl, entry) = findInRepos(pkgName) ?: throw IllegalArgumentException("no repo advertises '$pkgName'")
        require(entry.code > record.versionCode) { "'$pkgName' is already up to date (installed ${record.versionCode}, repo ${entry.code})" }
        logger.info { "Updating $pkgName ${record.versionCode} -> ${entry.code} from $repoUrl" }
        // install() re-acquires the (reentrant) lock.
        install(pkgName, null)
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

    /** Guarantee the APK is inside [extensionsRoot]: copy a local file in; leave an http URL for the loader. */
    private fun ensureOnVolume(url: String): String {
        if (url.startsWith("http")) return url
        val src = File(url)
        require(src.exists()) { "APK not found: $url" }
        val dest = File(extensionsRoot, src.name)
        if (src.absolutePath != dest.absolutePath) src.copyTo(dest, overwrite = true)
        return dest.absolutePath
    }

    private fun persistManifest() {
        manifestFile.writeText(mapper.writeValueAsString(installed.values.toList()))
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
    private fun availableByPkg(): Map<String, Pair<String, RepoIndexEntry>> {
        val best = HashMap<String, Pair<String, RepoIndexEntry>>()
        repos.forEach { repoUrl ->
            fetchIndex(repoUrl).forEach { entry ->
                val existing = best[entry.pkg]
                if (existing == null || entry.code > existing.second.code) {
                    best[entry.pkg] = repoUrl to entry
                }
            }
        }
        return best
    }

    private fun findInRepos(pkgName: String): Pair<String, RepoIndexEntry>? = availableByPkg()[pkgName]

    private fun fetchIndex(repoUrl: String): List<RepoIndexEntry> =
        repoCache.getOrPut(repoUrl) {
            val indexUrl = indexUrlFor(repoUrl)
            runCatching {
                URI(indexUrl).toURL().openStream().use { mapper.readValue<List<RepoIndexEntry>>(it.readBytes()) }
            }.onFailure { logger.warn(it) { "Failed to fetch repo index $indexUrl" } }
                .getOrDefault(emptyList())
        }

    private fun indexUrlFor(repoUrl: String): String =
        if (repoUrl.endsWith(".json")) repoUrl else "${repoUrl.trimEnd('/')}/index.min.json"

    private fun repoBaseFor(repoUrl: String): String =
        if (repoUrl.endsWith(".json")) repoUrl.substringBeforeLast('/') else repoUrl.trimEnd('/')

    private fun apkUrlFor(
        repoUrl: String,
        apk: String,
    ): String = "${repoBaseFor(repoUrl)}/apk/$apk"

    private fun iconUrlFor(
        repoUrl: String,
        pkg: String,
    ): String = "${repoBaseFor(repoUrl)}/icon/$pkg.png"

    companion object {
        /** The standard community repo, pre-configured so a fresh host is usable immediately. */
        val DEFAULT_REPOS = listOf("https://raw.githubusercontent.com/keiyoushi/extensions/repo/index.min.json")
    }
}
