package enginehost

import com.fasterxml.jackson.annotation.JsonCreator
import com.fasterxml.jackson.annotation.JsonValue

enum class AddressMode(@get:JsonValue val wire: String) {
    UNKNOWN("unknown"),
    DIRECT("direct"),
    URL_SEARCH("url_search"),
    ;

    companion object {
        @JvmStatic @JsonCreator fun fromWire(value: String?) = entries.firstOrNull { it.wire == value } ?: UNKNOWN
    }
}

/*
 * Tsundoku engine-host — RPC data-transfer objects.
 *
 * Every request/response is addressed by STABLE (sourceId, url) — never an
 * engine-assigned opaque id. This is the whole point of the Suwayomi-removal
 * milestone: a DB rebuild + extension reinstall yields the same source ids and
 * the same source-relative URLs, so a stored key always resolves to the same
 * series (killing the "wrong-series download" bug).
 */

/**
 * A manga entry in a search/browse result — addressed by its source-relative [url].
 *
 * [url] is the ADDRESSING url: what every subsequent request sends back to identify this manga.
 * It is source-relative and not necessarily a browser-openable link. [realUrl] is the fully-
 * qualified, browser-clickable url (Mihon's `HttpSource.getMangaUrl`) — powers the owner-facing
 * "View on source" external link. The two are NEVER the same thing; never fall back from one to
 * the other.
 */
data class MangaEntryDto(
    val url: String,
    val title: String,
    val thumbnailUrl: String?,
    val realUrl: String?,
    val addressMode: AddressMode = AddressMode.UNKNOWN,
)

/** Full manga details, keyed by [url]. See [MangaEntryDto] for the [url] vs [realUrl] distinction. */
data class MangaDetailsDto(
    val url: String,
    val title: String,
    val author: String?,
    val artist: String?,
    val description: String?,
    val genres: List<String>,
    val status: String,
    val thumbnailUrl: String?,
    val realUrl: String?,
    val addressMode: AddressMode = AddressMode.UNKNOWN,
)

/**
 * A chapter of a manga — addressed by its source-relative [url]. See [MangaEntryDto] for the
 * [url] (addressing) vs [realUrl] (browser-clickable) distinction — the same rule applies here.
 */
data class ChapterDto(
    val url: String,
    val name: String,
    val number: Float,
    val scanlator: String?,
    val uploadDate: Long,
    val realUrl: String?,
)

/**
 * A page of a chapter. The image address is the SOURCE's own page addressing — the pair
 * ([url], [imageUrl]) — NOT an engine id. Both are fed straight back to /image. Most sources
 * set only [imageUrl]; some (e.g. MangaDex) encode routing in [url] (an at-home base tuple)
 * and carry the relative image path in [imageUrl]. Passing both through keeps the source's
 * own imageRequest logic working statelessly.
 */
data class PageDto(
    val index: Int,
    val url: String,
    val imageUrl: String?,
)

/** A source loaded from an extension APK. */
data class LoadedSourceDto(
    val id: Long,
    val name: String,
    val lang: String,
)

// ---- Request bodies (all url-addressed) ----

/**
 * [filters] is accepted for forward-compatibility but **NOT yet applied** — FilterList
 * (de)serialization is P2 work. It is documented as such in RPC-CONTRACT.md so it is never a
 * silent drop; passing it today is a no-op, not an error.
 */
data class SearchRequest(
    val sourceId: Long,
    val query: String,
    val page: Int = 1,
    val filters: List<Any?>? = null,
)

/** Popular / latest browse of a source's catalogue (no query). */
data class BrowseRequest(val sourceId: Long, val page: Int = 1)

data class MangaRequest(val sourceId: Long, val url: String, val addressMode: AddressMode = AddressMode.UNKNOWN, val webUrl: String? = null)

/**
 * [mangaTitle] feeds [enginehost.vendor.ChapterRecognition] (the vendored Suwayomi
 * chapter-number-recognition step SourceCalls.chapters runs before returning) — it strips the
 * manga title from a chapter name before number-matching, so recognition is more accurate with it
 * than without. Optional/defaulted to "" for backward compatibility; recognition still works on ""
 * (it just skips the title-strip step).
 */
data class ChaptersRequest(val sourceId: Long, val url: String, val mangaTitle: String = "", val addressMode: AddressMode = AddressMode.UNKNOWN, val webUrl: String? = null)

/**
 * [mangaUrl] is the OPTIONAL source-relative SERIES url the chapter belongs to. Supplying it lets
 * [SourceCalls.pages] run a series-scoped chapter fetch and hand the real memo-bearing SChapter to
 * `getPageList` — required by the keiyoushi `KeiSource` family (AsuraScans / HiveScans / VortexScans),
 * whose `getChapterUrl` reads a per-chapter `memo["mangaSlug"]` a bare url-only seed lacks (GAP-109).
 * Defaulted to "" for backward compatibility: a blank value keeps the original bare-seed page fetch,
 * which is correct for every source whose `getPageList` needs only the chapter url.
 */
data class PagesRequest(val sourceId: Long, val chapterUrl: String, val mangaUrl: String = "", val addressMode: AddressMode = AddressMode.UNKNOWN, val webUrl: String? = null)

/** [pageUrl] = the page's [PageDto.url]; [imageUrl] = the page's [PageDto.imageUrl] (may be null). */
data class ImageRequest(val sourceId: Long, val pageUrl: String, val imageUrl: String? = null)

// ---- Response wrappers ----

data class SearchResponse(val manga: List<MangaEntryDto>, val hasNextPage: Boolean)

data class ChaptersResponse(val chapters: List<ChapterDto>, val addressMode: AddressMode = AddressMode.UNKNOWN)

data class PagesResponse(val pages: List<PageDto>, val addressMode: AddressMode = AddressMode.UNKNOWN)

data class ErrorResponse(val error: String)

// ---- Extension management ----

/** A source advertised by an extension (installed or available). */
data class ExtensionSourceDto(
    val id: Long,
    val name: String,
    val lang: String,
)

/**
 * An extension, merged across the installed working-set and the configured repos.
 * [isInstalled] = present on the volume; [hasUpdate] = a repo advertises a higher versionCode.
 */
data class ExtensionDto(
    val pkgName: String,
    val name: String,
    val versionName: String,
    val versionCode: Long,
    val lang: String,
    val isInstalled: Boolean,
    val hasUpdate: Boolean,
    val isNsfw: Boolean,
    val iconUrl: String?,
    val repoUrl: String?,
    val sources: List<ExtensionSourceDto>,
)

/**
 * Install request. Provide [pkgName] to install from the configured repos, or [apkUrl] to install
 * a specific APK directly. At least one is required.
 */
data class InstallRequest(
    val pkgName: String? = null,
    val apkUrl: String? = null,
)

/** The configured extension-repo index URLs (each an `index.min.json` or its base). */
data class ReposDto(val repos: List<String>)

// ---- Per-source preferences ----

/**
 * A single source preference descriptor — enough for Tsundoku to render a settings form.
 * [type] is the androidx.preference class name (EditTextPreference / SwitchPreferenceCompat /
 * ListPreference / CheckBoxPreference / MultiSelectListPreference). [entries]/[entryValues] are
 * present only for list-style preferences.
 */
data class PreferenceDto(
    val key: String?,
    val type: String,
    val title: String?,
    val summary: String?,
    val currentValue: Any?,
    val defaultValue: Any?,
    val entries: List<String>?,
    val entryValues: List<String>?,
)

data class PreferencesResponse(val preferences: List<PreferenceDto>)

// ---- Config passthrough (Tsundoku pushes in; all fields optional/partial) ----

/** Partial FlareSolverr config; only non-null fields are applied. [sessionTtl] minutes, [timeout] seconds. */
data class FlareSolverrConfigRequest(
    val enabled: Boolean? = null,
    val url: String? = null,
    val session: String? = null,
    val sessionTtl: Int? = null,
    val timeout: Int? = null,
    val asResponseFallback: Boolean? = null,
)

/**
 * Partial SOCKS-proxy config; only non-null fields are applied. [version] is 4 or 5.
 * `NON_NULL` inclusion means the read-back response OMITS the password entirely (never echoed).
 */
@com.fasterxml.jackson.annotation.JsonInclude(com.fasterxml.jackson.annotation.JsonInclude.Include.NON_NULL)
data class SocksConfigRequest(
    val enabled: Boolean? = null,
    val version: Int? = null,
    val host: String? = null,
    val port: String? = null,
    val username: String? = null,
    val password: String? = null,
)

/**
 * Partial impersonate-gateway config (GAP-111 — the Chrome-fingerprint image-fetch gateway); only
 * non-null fields are applied. [url] is the gateway endpoint; a blank/absent url disables it. Unlike
 * FlareSolverr/SOCKS this is NOT a Suwayomi `serverConfig` field — it lives in [ImpersonateConfig].
 *
 * [sourceIds] is the GAP-131 per-source gating set: the source ids allowed to use the gateway. It
 * carries IDS, never names — a source id is the only identity this host resolves (see RpcServer's
 * `resolve(sourceId)`), so Tsundoku maps its owner-facing source names to ids before pushing. An
 * absent (null) list leaves the stored set untouched, like every other field here; an explicitly
 * EMPTY list is the meaningful "no source uses the gateway" value and CLEARS it.
 */
data class ImpersonateConfigRequest(
    val enabled: Boolean? = null,
    val url: String? = null,
    val sourceIds: List<Long>? = null,
)

/**
 * Partial image connection-policy config. [reuseSourceIds] selects sources
 * whose cacheless image calls reuse the source's normal pooled client. An
 * absent list preserves the selection; an explicitly empty list clears it.
 */
data class ImageTransportConfigRequest(
    val reuseSourceIds: List<Long>? = null,
)

data class OkResponse(val ok: Boolean = true)
