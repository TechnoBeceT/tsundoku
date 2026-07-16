package enginehost

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

data class MangaRequest(val sourceId: Long, val url: String)

/**
 * [mangaTitle] feeds [enginehost.vendor.ChapterRecognition] (the vendored Suwayomi
 * chapter-number-recognition step SourceCalls.chapters runs before returning) — it strips the
 * manga title from a chapter name before number-matching, so recognition is more accurate with it
 * than without. Optional/defaulted to "" for backward compatibility; recognition still works on ""
 * (it just skips the title-strip step).
 */
data class ChaptersRequest(val sourceId: Long, val url: String, val mangaTitle: String = "")

data class PagesRequest(val sourceId: Long, val chapterUrl: String)

/** [pageUrl] = the page's [PageDto.url]; [imageUrl] = the page's [PageDto.imageUrl] (may be null). */
data class ImageRequest(val sourceId: Long, val pageUrl: String, val imageUrl: String? = null)

// ---- Response wrappers ----

data class SearchResponse(val manga: List<MangaEntryDto>, val hasNextPage: Boolean)

data class ChaptersResponse(val chapters: List<ChapterDto>)

data class PagesResponse(val pages: List<PageDto>)

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

data class OkResponse(val ok: Boolean = true)
