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
import java.net.URI
import java.util.concurrent.ConcurrentHashMap
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

/**
 * ExtensionLoader installs a Mihon extension APK on a plain JVM and instantiates its
 * source(s), with NO Suwayomi server, NO database. Loaded sources are cached by their
 * stable [Source.id] so the RPC layer can resolve `(sourceId, url)` calls; the per-package
 * source-id map lets [ExtensionManager] unload an extension cleanly on uninstall/update.
 */
class ExtensionLoader(
    private val workDir: File,
) {
    private val logger = KotlinLogging.logger {}
    private val sources = ConcurrentHashMap<Long, Source>()

    /** All sources loaded so far, in load order. */
    fun loaded(): List<Source> = sources.values.toList()

    /** Resolve a previously-loaded source by its stable id (null if unknown). */
    fun source(sourceId: Long): Source? = sources[sourceId]

    /** Drop the given source ids from the in-memory registry (on uninstall). */
    fun unload(sourceIds: Collection<Long>) {
        sourceIds.forEach { sources.remove(it) }
    }

    /**
     * Load an extension from a local APK path or an http(s) URL and return its full descriptor.
     * Mirrors Suwayomi's install path minus the DB; registers each instantiated source by id.
     */
    fun loadFromApk(apkPathOrUrl: String): LoadedExtension {
        val apkFile = resolveApk(apkPathOrUrl)
        val fileNameWithoutType = apkFile.name.substringBefore(".apk")
        val jarFile = File(workDir, "$fileNameWithoutType.jar")

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

        val instance = loadExtensionSources(jarFile.absolutePath, className)
        val loaded: List<Source> =
            when (instance) {
                is Source -> listOf(instance)
                is SourceFactory -> instance.createSources()
                else -> error("Unknown source class type: ${instance.javaClass}")
            }

        loaded.forEach { source ->
            sources[source.id] = source
            logger.info { "Loaded source id=${source.id} name='${source.name}' lang='${source.lang}'" }
        }

        return LoadedExtension(
            pkgName = packageInfo.packageName,
            versionName = packageInfo.versionName,
            versionCode = packageInfo.versionCode.toLong(),
            mainClass = className,
            jarFile = jarFile,
            sources = loaded,
        )
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
        loaded.forEach { sources[it.id] = it }
        return loaded
    }

    /**
     * Convenience for the CLI bootstrap path (Main.kt's optional APK arg): load and return the
     * source descriptors only.
     */
    fun loadExtension(apkPathOrUrl: String): List<LoadedSourceDto> =
        loadFromApk(apkPathOrUrl).sources.map { LoadedSourceDto(it.id, it.name, it.lang) }

    /** Download (if a URL) or reference (if a local path) the APK into the work dir. */
    private fun resolveApk(apkPathOrUrl: String): File {
        if (!apkPathOrUrl.startsWith("http")) {
            val local = File(apkPathOrUrl)
            require(local.exists()) { "APK not found: $apkPathOrUrl" }
            return local
        }
        val name = apkPathOrUrl.substringAfterLast('/')
        val dest = File(workDir, name)
        if (!dest.exists()) {
            logger.info { "Downloading APK $apkPathOrUrl" }
            URI(apkPathOrUrl).toURL().openStream().use { input ->
                FileOutputStream(dest).use { output -> input.copyTo(output) }
            }
        }
        return dest
    }

    /**
     * Adapted from Suwayomi's Extension.extractAssetsFromApk: copy the APK's `assets/` into the
     * jar and drop `META-INF/` (signature entries would break classloading of the unsigned jar).
     */
    private fun extractAssetsFromApk(
        apkFile: File,
        jarFile: File,
    ) {
        val assetsFolder = File(workDir, "${apkFile.nameWithoutExtension}_assets")
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

        val tempJar = File(workDir, "${jarFile.nameWithoutExtension}_temp.jar")
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

        jarFile.delete()
        tempJar.renameTo(jarFile)
        assetsFolder.deleteRecursively()
    }
}
