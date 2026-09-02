package enginehost

/*
 * Portions adapted from Suwayomi-Server (Mozilla Public License 2.0):
 *   suwayomi.tachidesk.manga.impl.extension.Extension  (installAPK / extractAssetsFromApk)
 * The DB/GraphQL coupling of the original is removed — this is a stateless,
 * in-memory loader: APK -> dex2jar -> classload -> instantiate Source(s) ->
 * register in a map keyed by the STABLE Tachiyomi source id.
 *
 * This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at https://mozilla.org/MPL/2.0/.
 */

import eu.kanade.tachiyomi.source.Source
import eu.kanade.tachiyomi.source.SourceFactory
import io.github.oshai.kotlinlogging.KotlinLogging
import suwayomi.tachidesk.manga.impl.util.PackageTools
import suwayomi.tachidesk.manga.impl.util.PackageTools.LIB_VERSION_MAX
import suwayomi.tachidesk.manga.impl.util.PackageTools.LIB_VERSION_MIN
import suwayomi.tachidesk.manga.impl.util.PackageTools.METADATA_SOURCE_CLASS
import suwayomi.tachidesk.manga.impl.util.PackageTools.dex2jar
import suwayomi.tachidesk.manga.impl.util.PackageTools.getPackageInfo
import suwayomi.tachidesk.manga.impl.util.PackageTools.loadExtensionSources
import java.io.File
import java.io.FileOutputStream
import java.lang.ref.Cleaner
import java.net.URLClassLoader
import java.nio.file.Path
import java.util.Collections
import java.util.IdentityHashMap
import java.util.concurrent.atomic.AtomicInteger
import java.util.zip.ZipEntry
import java.util.zip.ZipInputStream
import java.util.zip.ZipOutputStream

/** A fully-loaded extension: its package identity, version, main class, on-disk jar, and sources. */
data class LoadedExtension(
    val pkgName: String,
    val versionName: String,
    val versionCode: Long,
    val mainClass: String,
    val jarFile: File,
    val sources: List<Source>,
)

/** Verified APK metadata and its derived jar, prepared without mutating the active source registry. */
internal data class PreparedExtension(
    val pkgName: String,
    val versionName: String,
    val versionCode: Long,
    val mainClass: String,
    val apkFile: Path,
    val jarFile: Path,
    val signerFingerprints: Set<String>,
)

internal fun interface ExtensionPreparer {
    fun prepare(apk: Path): PreparedExtension
}

/** One immutable generation published atomically to source and installed-state readers. */
internal data class ExtensionRegistrySnapshot(
    val sources: Map<Long, Source>,
    val installed: Map<String, InstalledExtension>,
)

/**
 * ExtensionLoader installs a Mihon extension APK on a plain JVM and instantiates its
 * source(s), with NO Suwayomi server, NO database. Loaded sources are cached by their
 * stable [Source.id] so the RPC layer can resolve `(sourceId, url)` calls; the per-package
 * source-id map lets [ExtensionManager] unload an extension cleanly on uninstall/update.
 */
class ExtensionLoader internal constructor(
    private val workDir: File,
    private val signatureVerifier: ApkSignatureVerifier,
) {
    constructor(workDir: File) : this(workDir, ApkSignerVerifier)

    private val logger = KotlinLogging.logger {}
    @Volatile
    private var registry = ExtensionRegistrySnapshot(emptyMap(), emptyMap())

    /** All sources loaded so far, in load order. */
    fun loaded(): List<Source> = registry.sources.values.toList()

    /** Resolve a previously-loaded source by its stable id (null if unknown). */
    fun source(sourceId: Long): Source? = registry.sources[sourceId]

    internal fun snapshotRegistry(): ExtensionRegistrySnapshot = registry

    internal fun prepareRegistry(
        sources: Map<Long, Source>,
        installed: Map<String, InstalledExtension>,
    ): ExtensionRegistrySnapshot =
        ExtensionRegistrySnapshot(
            sources = Collections.unmodifiableMap(HashMap(sources)),
            installed = Collections.unmodifiableMap(HashMap(installed)),
        )

    /** Publish a fully prepared source/installed generation with one volatile write. */
    internal fun publishRegistry(snapshot: ExtensionRegistrySnapshot) {
        registry = snapshot
    }

    /** Publish bootstrap or preference-reload sources without changing installed ownership. */
    internal fun registerSources(replacement: Collection<Source>) {
        val current = registry
        val nextSources = HashMap(current.sources)
        replacement.forEach { nextSources[it.id] = it }
        publishRegistry(prepareRegistry(nextSources, current.installed))
    }

    /** Load and register an extension from an existing local APK. */
    fun loadFromApk(apkPath: String): LoadedExtension {
        val prepared = prepareFromApk(Path.of(apkPath))
        return instantiatePrepared(prepared).also { registerSources(it.sources) }
    }

    /**
     * Validate and transform an existing local APK without changing the active source registry.
     * Callers may run this outside the extension mutation lock, then instantiate the verified jar
     * under the lock immediately before committing local state.
     */
    internal fun prepareFromApk(apkPath: Path): PreparedExtension {
        val apkFile = apkPath.toFile()
        require(apkFile.exists()) { "APK not found: $apkPath" }
        val signerFingerprints = signatureVerifier.verify(apkPath)
        val fileNameWithoutType = apkFile.name.substringBefore(".apk")
        val jarFile = File(workDir, "$fileNameWithoutType.jar")

        try {
            val packageInfo = getPackageInfo(apkFile.absolutePath)

            // Validate the extension lib version (same guard Suwayomi enforces).
            val libVersion = packageInfo.versionName.substringBeforeLast('.').toDouble()
            require(libVersion in LIB_VERSION_MIN..LIB_VERSION_MAX) {
                "Lib version $libVersion outside supported $LIB_VERSION_MIN..$LIB_VERSION_MAX"
            }

            val sourceClass =
                packageInfo.applicationInfo.metaData
                    .getString(METADATA_SOURCE_CLASS)!!
                    .trim()
            val className =
                if (sourceClass.startsWith(".")) packageInfo.packageName + sourceClass else sourceClass

            logger.info { "Extension ${packageInfo.packageName} main class: $className" }

            // dex -> jar (+ Suwayomi's android-class bytecode fixups), then strip META-INF / merge assets.
            dex2jar(apkFile.absolutePath, jarFile.absolutePath, fileNameWithoutType)
            extractAssetsFromApk(apkFile, jarFile)

            // Repair the StackMapTable dex2jar (+ Suwayomi's BytecodeEditor) leaves broken on newer
            // extension APKs, which otherwise fails class verification with "Expecting a stackmap frame
            // at branch target N" (GAP-100 — e.g. Asura Scans 1.6.66). See DexStackFrameRewriter.
            DexStackFrameRewriter.repairStackFrames(jarFile.toPath(), javaClass.classLoader)

            return PreparedExtension(
                pkgName = packageInfo.packageName,
                versionName = packageInfo.versionName,
                versionCode = packageInfo.versionCode.toLong(),
                mainClass = className,
                apkFile = apkFile.toPath(),
                jarFile = jarFile.toPath(),
                signerFingerprints = signerFingerprints,
            )
        } catch (failure: Throwable) {
            jarFile.delete()
            throw failure
        }
    }

    internal fun verifyApkSigners(apk: Path): Set<String> = signatureVerifier.verify(apk)

    /** Instantiate a prepared jar without changing the active registry. */
    internal fun instantiatePrepared(prepared: PreparedExtension): LoadedExtension {
        val instance = loadExtensionSources(prepared.jarFile.toString(), prepared.mainClass)
        val loaded: List<Source> =
            when (instance) {
                is Source -> listOf(instance)
                is SourceFactory -> instance.createSources()
                else -> error("Unknown source class type: ${instance.javaClass}")
            }

        loaded.forEach { source ->
            logger.info { "Loaded source id=${source.id} name='${source.name}' lang='${source.lang}'" }
        }

        return LoadedExtension(
            pkgName = prepared.pkgName,
            versionName = prepared.versionName,
            versionCode = prepared.versionCode,
            mainClass = prepared.mainClass,
            jarFile = prepared.jarFile.toFile(),
            sources = loaded,
        )
    }

    /**
     * Instantiate a staged candidate only long enough to inspect its declared source IDs, then
     * close and evict the staging-path classloader. The active registry is never changed.
     */
    internal fun inspectPreparedSourceIds(prepared: PreparedExtension): List<Long> =
        try {
            instantiatePrepared(prepared).sources.map { it.id }
        } finally {
            evictAndClose(prepared.jarFile)
        }

    /** Remove and close a loader whose sources were never committed to the active registry. */
    internal fun evictAndClose(jarFile: Path) {
        PackageTools.jarLoaderMap.remove(jarFile.toString())?.close()
    }

    /**
     * Stop future reuse of a superseded loader immediately, but keep its jar and loader available
     * until every replaced source instance becomes unreachable. In-flight calls may load helper
     * classes lazily, so closing the loader at registry replacement time is not safe.
     */
    internal fun retire(
        jarFile: Path,
        replacedSources: Collection<Source>,
    ) {
        val classLoader = PackageTools.jarLoaderMap.remove(jarFile.toString())
        if (classLoader == null) {
            jarFile.toFile().delete()
            return
        }
        val distinctSources = Collections.newSetFromMap(IdentityHashMap<Source, Boolean>()).apply { addAll(replacedSources) }
        if (distinctSources.isEmpty()) {
            classLoader.close()
            jarFile.toFile().delete()
            return
        }
        val retirement = LoaderRetirement(classLoader, jarFile, distinctSources.size)
        distinctSources.forEach { source -> retiredLoaderCleaner.register(source, retirement::release) }
    }

    /** Build a complete prospective source generation without changing the active registry. */
    internal fun replacementSources(
        current: Map<Long, Source>,
        previousSourceIds: Collection<Long>,
        replacement: List<Source>,
    ): Map<Long, Source> {
        val next = HashMap(current)
        val replacementIds = replacement.mapTo(HashSet()) { it.id }
        replacement.forEach { next[it.id] = it }
        previousSourceIds.filterNot { it in replacementIds }.forEach { next.remove(it) }
        return next
    }

    /**
     * Re-instantiate an already-installed extension's source(s) from its EXISTING jar, WITHOUT
     * re-running dex2jar or the asset-rewrite. Used on a preference reload so a source picks up a
     * just-written SharedPreferences value without the wasteful (and unsafe — it deletes+renames the
     * jar a live classloader still references) reinstall pipeline. `loadExtensionSources` reuses the
     * cached ChildFirstURLClassLoader for this jar, so the fresh instance reads the current prefs.
     * MUST be called under [ExtensionManager]'s mutation lock (the classloader cache is not thread-safe).
     */
    fun reinstantiate(
        jarPath: String,
        className: String,
    ): List<Source> {
        val instance = loadExtensionSources(jarPath, className)
        val loaded: List<Source> =
            when (instance) {
                is Source -> listOf(instance)
                is SourceFactory -> instance.createSources()
                else -> error("Unknown source class type: ${instance.javaClass}")
            }
        registerSources(loaded)
        return loaded
    }

    /**
     * Convenience for the CLI bootstrap path (Main.kt's optional APK arg): load and return the
     * source descriptors only.
     */
    fun loadExtension(apkPath: String): List<LoadedSourceDto> =
        loadFromApk(apkPath).sources.map { LoadedSourceDto(it.id, it.name, it.lang) }

    /**
     * Adapted from Suwayomi's Extension.extractAssetsFromApk: copy the APK's `assets/` into the
     * jar and drop `META-INF/` (signature entries would break classloading of the unsigned jar).
     */
    private fun extractAssetsFromApk(
        apkFile: File,
        jarFile: File,
    ) {
        val assetsFolder = File(workDir, "${apkFile.nameWithoutExtension}_assets")
        val tempJar = File(workDir, "${jarFile.nameWithoutExtension}_temp.jar")
        try {
            assetsFolder.mkdirs()
            ZipInputStream(apkFile.inputStream()).use { zin ->
                var entry = zin.nextEntry
                while (entry != null) {
                    if (entry.name.startsWith("assets/") && !entry.isDirectory) {
                        val assetFile = File(assetsFolder, entry.name)
                        assetFile.parentFile.mkdirs()
                        FileOutputStream(assetFile).use { out -> zin.copyTo(out) }
                    }
                    entry = zin.nextEntry
                }
            }

            ZipInputStream(jarFile.inputStream()).use { jin ->
                ZipOutputStream(FileOutputStream(tempJar)).use { jout ->
                    var entry = jin.nextEntry
                    while (entry != null) {
                        if (!entry.name.startsWith("META-INF/")) {
                            jout.putNextEntry(ZipEntry(entry.name))
                            jin.copyTo(jout)
                        }
                        entry = jin.nextEntry
                    }
                    assetsFolder.walkTopDown().forEach { file ->
                        if (file.isFile) {
                            jout.putNextEntry(
                                ZipEntry(file.relativeTo(assetsFolder).toString().replace("\\", "/")),
                            )
                            file.inputStream().use { it.copyTo(jout) }
                            jout.closeEntry()
                        }
                    }
                }
            }

            try {
                java.nio.file.Files.move(
                    tempJar.toPath(),
                    jarFile.toPath(),
                    java.nio.file.StandardCopyOption.ATOMIC_MOVE,
                    java.nio.file.StandardCopyOption.REPLACE_EXISTING,
                )
            } catch (_: java.nio.file.AtomicMoveNotSupportedException) {
                java.nio.file.Files.move(tempJar.toPath(), jarFile.toPath(), java.nio.file.StandardCopyOption.REPLACE_EXISTING)
            }
        } finally {
            tempJar.delete()
            assetsFolder.deleteRecursively()
        }
    }

    private class LoaderRetirement(
        private val classLoader: URLClassLoader,
        private val jarFile: Path,
        sourceCount: Int,
    ) {
        private val remaining = AtomicInteger(sourceCount)

        fun release() {
            if (remaining.decrementAndGet() != 0) return
            runCatching { classLoader.close() }
            runCatching { jarFile.toFile().delete() }
        }
    }

    companion object {
        private val retiredLoaderCleaner: Cleaner = Cleaner.create()
    }
}
