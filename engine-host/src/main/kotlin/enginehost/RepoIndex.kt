@file:OptIn(kotlinx.serialization.ExperimentalSerializationApi::class)

package enginehost

/*
 * Repo-index model + format-aware parser (GAP-145).
 *
 * A Mihon extension repo advertises its catalogue in one of two shapes that differ by URL suffix:
 *   - `index.json` — a wrapper OBJECT `{ …, extensionList:{ extensions:[ … ] } }` (the current
 *     Keiyoushi schema). Also accepts the LEGACY flat `[ … ]` array a differently-configured repo
 *     may still serve.
 *   - `index.pb`   — the same model, gzip-compressed protobuf.
 * (`index.min.json` was neutered to a 2-entry deprecation stub, which is why the fetch moved off it.)
 *
 * Both formats normalise into ONE internal [RepoIndexEntry], so every caller in [ExtensionManager]
 * is format-agnostic. Entries carry RESOLVED absolute apk/icon URLs — the new schema supplies them
 * directly (`resources.apkUrl`/`iconUrl`), the legacy schema's relative filenames are resolved
 * against the repo base at parse time — so no caller ever rebuilds a URL from a repo base.
 */

import com.fasterxml.jackson.annotation.JsonIgnoreProperties
import com.fasterxml.jackson.databind.ObjectMapper
import com.fasterxml.jackson.module.kotlin.convertValue
import io.github.oshai.kotlinlogging.KotlinLogging
import kotlinx.serialization.Serializable
import kotlinx.serialization.protobuf.ProtoBuf
import kotlinx.serialization.protobuf.ProtoNumber
import java.util.zip.GZIPInputStream

/**
 * One extension advertised by a repo, normalised across the JSON-wrapper, legacy-flat and protobuf
 * formats. [apkUrl]/[iconUrl] are already ABSOLUTE (resolved at parse time). [code] is the numeric
 * version code used for update comparison; [nsfw] is 1 for an adult extension, 0 otherwise.
 */
internal data class RepoIndexEntry(
    val name: String,
    val pkg: String,
    val apkUrl: String,
    val iconUrl: String?,
    val lang: String,
    val code: Long,
    val version: String,
    val nsfw: Int,
    val sources: List<RepoIndexSource>,
)

/** A source advertised by a repo entry, with the source id already parsed to the engine's [Long] id. */
internal data class RepoIndexSource(
    val id: Long,
    val name: String,
    val lang: String,
)

/**
 * RepoIndexParser turns the raw index bytes of any supported format into [RepoIndexEntry]s. It is
 * FAIL-SOFT: any malformed / short / undecodable input logs a warning and yields an empty list
 * (the same degrade [ExtensionManager.fetchIndex] already relied on), never a thrown exception.
 */
internal object RepoIndexParser {
    private val logger = KotlinLogging.logger {}
    private val protobuf = ProtoBuf { }

    /**
     * Parse [bytes] into entries. Format is chosen by [indexUrl]'s suffix (`.pb` → protobuf) with a
     * gzip-magic fallback; everything else is JSON (new wrapper, or the legacy flat array). [repoBase]
     * resolves the legacy schema's relative apk/icon filenames to absolute URLs.
     */
    fun parse(
        bytes: ByteArray,
        indexUrl: String,
        repoBase: String,
        mapper: ObjectMapper,
    ): List<RepoIndexEntry> =
        runCatching {
            if (looksLikeProtobuf(indexUrl, bytes)) parseProtobuf(bytes) else parseJson(bytes, repoBase, mapper)
        }.onFailure { logger.warn(it) { "Failed to parse repo index $indexUrl" } }
            .getOrDefault(emptyList())

    private fun looksLikeProtobuf(
        indexUrl: String,
        bytes: ByteArray,
    ): Boolean = indexUrl.endsWith(".pb") || isGzip(bytes)

    private fun isGzip(bytes: ByteArray): Boolean = bytes.size >= 2 && bytes[0] == GZIP_MAGIC_0 && bytes[1] == GZIP_MAGIC_1

    // ---- JSON ----

    private fun parseJson(
        bytes: ByteArray,
        repoBase: String,
        mapper: ObjectMapper,
    ): List<RepoIndexEntry> {
        val root = mapper.readTree(bytes)
        // A top-level array is the legacy flat schema; an object is the new `extensionList` wrapper.
        return if (root.isArray) {
            mapper.convertValue<List<LegacyRepoEntry>>(root).map { it.toEntry(repoBase) }
        } else {
            mapper.convertValue<JsonRepoIndex>(root).extensionList.extensions.map { it.toEntry() }
        }
    }

    // ---- Protobuf ----

    private fun parseProtobuf(bytes: ByteArray): List<RepoIndexEntry> {
        val raw = if (isGzip(bytes)) GZIPInputStream(bytes.inputStream()).use { it.readBytes() } else bytes
        return protobuf.decodeFromByteArray(PbRepoIndex.serializer(), raw).extensionList.extensions.map { it.toEntry() }
    }

    private const val GZIP_MAGIC_0: Byte = 0x1f
    private const val GZIP_MAGIC_1: Byte = 0x8b.toByte()
}

// ---- Shared normalisation ----

/**
 * Derive an extension's display language from its sources now that the new schema carries no
 * top-level `lang`: the single distinct source language when they agree, otherwise "all"
 * (mirrors the installed-record derivation in [ExtensionManager.install]).
 */
private fun deriveLang(sources: List<RepoIndexSource>): String = sources.map { it.lang }.toSet().singleOrNull() ?: "all"

/** The `contentWarning` enum member that marks an adult extension (JSON name / protobuf ordinal). */
private const val CONTENT_WARNING_NSFW = "CONTENT_WARNING_NSFW"
private const val CONTENT_WARNING_NSFW_ORDINAL = 3

// ---- New JSON-wrapper schema (Jackson) ----

@JsonIgnoreProperties(ignoreUnknown = true)
private data class JsonRepoIndex(
    val extensionList: JsonExtensionList = JsonExtensionList(),
)

@JsonIgnoreProperties(ignoreUnknown = true)
private data class JsonExtensionList(
    val extensions: List<JsonRepoExtension> = emptyList(),
)

@JsonIgnoreProperties(ignoreUnknown = true)
private data class JsonRepoExtension(
    val name: String,
    val packageName: String,
    val versionName: String = "",
    val versionCode: String = "",
    val contentWarning: String? = null,
    val resources: JsonRepoResources = JsonRepoResources(),
    val sources: List<JsonRepoSource> = emptyList(),
) {
    fun toEntry(): RepoIndexEntry {
        val srcs = sources.mapNotNull { it.toSource() }
        return RepoIndexEntry(
            name = name,
            pkg = packageName,
            apkUrl = resources.apkUrl.orEmpty(),
            iconUrl = resources.iconUrl?.ifBlank { null },
            lang = deriveLang(srcs),
            code = versionCode.toLongOrNull() ?: 0,
            version = versionName,
            nsfw = if (contentWarning == CONTENT_WARNING_NSFW) 1 else 0,
            sources = srcs,
        )
    }
}

@JsonIgnoreProperties(ignoreUnknown = true)
private data class JsonRepoResources(
    val apkUrl: String? = null,
    val iconUrl: String? = null,
)

@JsonIgnoreProperties(ignoreUnknown = true)
private data class JsonRepoSource(
    val id: String,
    val name: String = "",
    val language: String = "",
) {
    // A source id that does not parse to a Long is unusable by the engine (which keys on Long ids); drop it.
    fun toSource(): RepoIndexSource? = id.toLongOrNull()?.let { RepoIndexSource(it, name, language) }
}

// ---- Legacy flat schema (Jackson) ----

@JsonIgnoreProperties(ignoreUnknown = true)
private data class LegacyRepoEntry(
    val name: String,
    val pkg: String,
    val apk: String,
    val lang: String,
    val code: Long,
    val version: String,
    val nsfw: Int = 0,
    val sources: List<LegacyRepoSource> = emptyList(),
) {
    fun toEntry(repoBase: String): RepoIndexEntry =
        RepoIndexEntry(
            name = name,
            pkg = pkg,
            apkUrl = "$repoBase/apk/$apk",
            iconUrl = "$repoBase/icon/$pkg.png",
            lang = lang,
            code = code,
            version = version,
            nsfw = nsfw,
            sources = sources.mapNotNull { src -> src.id.toLongOrNull()?.let { RepoIndexSource(it, src.name, src.lang) } },
        )
}

@JsonIgnoreProperties(ignoreUnknown = true)
private data class LegacyRepoSource(
    val name: String,
    val lang: String,
    val id: String,
    val baseUrl: String? = null,
)

// ---- Protobuf schema (kotlinx.serialization) ----
// Field numbers verified EMPIRICALLY by decoding the live `index.pb` and cross-checking every
// extension against `index.json` (0 mismatches across the full catalogue). Unknown fields
// (badgeLabel/signingKey/contact on the root, extensionLib on an extension, jarUrl at resources
// field 501) are intentionally omitted — protobuf decoding skips them.

@Serializable
private data class PbRepoIndex(
    @ProtoNumber(101) val extensionList: PbExtensionList = PbExtensionList(),
)

@Serializable
private data class PbExtensionList(
    @ProtoNumber(1) val extensions: List<PbRepoExtension> = emptyList(),
)

@Serializable
private data class PbRepoExtension(
    @ProtoNumber(1) val name: String = "",
    @ProtoNumber(2) val packageName: String = "",
    @ProtoNumber(3) val resources: PbRepoResources = PbRepoResources(),
    @ProtoNumber(5) val versionCode: Long = 0,
    @ProtoNumber(6) val versionName: String = "",
    @ProtoNumber(7) val contentWarning: Int = 0,
    @ProtoNumber(8) val sources: List<PbRepoSource> = emptyList(),
) {
    fun toEntry(): RepoIndexEntry {
        val srcs = sources.map { RepoIndexSource(it.id, it.name, it.language) }
        return RepoIndexEntry(
            name = name,
            pkg = packageName,
            apkUrl = resources.apkUrl,
            iconUrl = resources.iconUrl.ifBlank { null },
            lang = deriveLang(srcs),
            code = versionCode,
            version = versionName,
            nsfw = if (contentWarning == CONTENT_WARNING_NSFW_ORDINAL) 1 else 0,
            sources = srcs,
        )
    }
}

@Serializable
private data class PbRepoResources(
    @ProtoNumber(1) val apkUrl: String = "",
    @ProtoNumber(2) val iconUrl: String = "",
)

@Serializable
private data class PbRepoSource(
    @ProtoNumber(1) val id: Long = 0,
    @ProtoNumber(2) val name: String = "",
    @ProtoNumber(3) val language: String = "",
)
