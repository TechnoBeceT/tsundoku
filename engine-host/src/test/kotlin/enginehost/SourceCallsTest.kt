package enginehost

/*
 * Pins the Flame Comics / Manhuascan.us production regression (branch v2): the manga-details RPC
 * returned HTTP 502 `UninitializedPropertyAccessException: lateinit property url has not been
 * initialized` for sources whose details parser builds a fresh SManga without setting the lateinit
 * identity fields. `SourceCalls.mangaDetails` used to map that parser-returned SManga's `url`/`title`
 * directly, so a details parser that legitimately omits them (the identity is already known in the
 * normal Mihon/Suwayomi flow) threw before the `.ifBlank { requestedUrl }` fallback could run.
 *
 * The fix re-seeds the requested url onto the parser return (the requested url IS the identity) and
 * reads the still-lateinit `title` defensively — mirroring Suwayomi's own `Manga.updateMangaDatabase`,
 * which trusts the known identity over the parser return. Without the fix both tests below throw.
 */

import eu.kanade.tachiyomi.source.Source
import eu.kanade.tachiyomi.source.model.FilterList
import eu.kanade.tachiyomi.source.model.MangasPage
import eu.kanade.tachiyomi.source.model.Page
import eu.kanade.tachiyomi.source.model.SChapter
import eu.kanade.tachiyomi.source.model.SManga
import eu.kanade.tachiyomi.source.model.SMangaUpdate
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertSame

/**
 * Minimal [Source] test double whose [getMangaUpdate] returns a fresh [SManga] built by a
 * configurable initializer — modelling an extension details parser that may leave the lateinit
 * `url`/`title` unset. Every other source call is unused by [SourceCalls.mangaDetails] and throws.
 * Implements [Source] directly (not [eu.kanade.tachiyomi.source.CatalogueSource]) to avoid its many
 * unrelated abstract members; not an HttpSource, so `realUrl` resolves to null.
 */
private class FakeDetailsSource(
    private val buildParserManga: SManga.() -> Unit,
) : Source {
    override val id: Long = 1L
    override val name: String = "Fake Details Source"
    override val lang: String = "en"
    override val supportsLatest: Boolean = false

    override suspend fun getMangaUpdate(
        manga: SManga,
        chapters: List<SChapter>,
        fetchDetails: Boolean,
        fetchChapters: Boolean,
    ): SMangaUpdate = SMangaUpdate(SManga.create().apply(buildParserManga), emptyList())

    override suspend fun getPopularManga(page: Int): MangasPage = error("unused")

    override suspend fun getLatestUpdates(page: Int): MangasPage = error("unused")

    override suspend fun getSearchManga(
        page: Int,
        query: String,
        filters: FilterList,
    ): MangasPage = error("unused")

    override suspend fun getPageList(chapter: SChapter): List<Page> = error("unused")
}

/**
 * Minimal [Source] test double for the [SourceCalls.pages] GAP-109 flow. [onPageList] models the
 * source's own `getPageList` — it may throw for the bare url-only seed and succeed only for the REAL
 * chapter the warm fetch supplies (the keiyoushi memo mechanism, modelled here by chapter reference
 * identity so the test needs no serialization types). [onMangaUpdate] models the series-scoped
 * chapter fetch used to warm. [mangaUpdateCalls] counts how often the warm fetch actually ran, so a
 * test can assert the bare-first path took ZERO series fetches. Not an HttpSource — pages never needs one.
 */
private class FakePagesSource(
    private val onPageList: (SChapter) -> List<Page>,
    private val onMangaUpdate: () -> List<SChapter> = { error("getMangaUpdate must not be called in this test") },
) : Source {
    override val id: Long = 1L
    override val name: String = "Fake Pages Source"
    override val lang: String = "en"
    override val supportsLatest: Boolean = false

    /** Number of times the series-scoped warm fetch was invoked. */
    var mangaUpdateCalls = 0
        private set

    override suspend fun getMangaUpdate(
        manga: SManga,
        chapters: List<SChapter>,
        fetchDetails: Boolean,
        fetchChapters: Boolean,
    ): SMangaUpdate {
        mangaUpdateCalls++
        return SMangaUpdate(manga, onMangaUpdate())
    }

    override suspend fun getPageList(chapter: SChapter): List<Page> = onPageList(chapter)

    override suspend fun getPopularManga(page: Int): MangasPage = error("unused")

    override suspend fun getLatestUpdates(page: Int): MangasPage = error("unused")

    override suspend fun getSearchManga(
        page: Int,
        query: String,
        filters: FilterList,
    ): MangasPage = error("unused")
}

/** A plain url-only [SChapter], as the series-scoped warm fetch would return it. */
private fun chapterAt(chapterUrl: String): SChapter = SChapter.create().apply { url = chapterUrl }

class SourceCallsTest {
    /**
     * A details parser that sets `title` but omits the lateinit `url`: mangaDetails must NOT throw
     * and must fall the identity url back to the requested url (the Flame Comics / Manhuascan.us case).
     */
    @Test
    fun `mangaDetails falls back to the requested url when the parser omits url`() {
        val source = FakeDetailsSource { title = "Solo Leveling" }

        val dto = SourceCalls.mangaDetails(source, "/series/83")

        assertEquals("/series/83", dto.url)
        assertEquals("Solo Leveling", dto.title)
    }

    /**
     * A details parser that omits BOTH lateinit identity fields: mangaDetails must NOT throw, must
     * fall the url back to the requested url, and must fall the title back to "" (Suwayomi's own
     * fallback) rather than surfacing an UninitializedPropertyAccessException.
     */
    @Test
    fun `mangaDetails falls title back to blank when the parser omits title`() {
        val source = FakeDetailsSource { /* neither url nor title set */ }

        val dto = SourceCalls.mangaDetails(source, "/series/83")

        assertEquals("/series/83", dto.url)
        assertEquals("", dto.title)
    }

    /**
     * GAP-109 bare-first: when the bare url-only seed's getPageList succeeds, pages returns those
     * pages and NEVER runs the series-scoped warm fetch (zero extra requests for the common source).
     */
    @Test
    fun `pages returns bare-seed pages without warming when the bare seed succeeds`() {
        val source =
            FakePagesSource(
                onPageList = { listOf(Page(index = 0, url = "https://x/p1"), Page(index = 1, url = "https://x/p2")) },
            )

        val response = SourceCalls.pages(source, "/ch/1", mangaUrl = "/series/1")

        assertEquals(listOf("https://x/p1", "https://x/p2"), response.pages.map { it.url })
        assertEquals(0, source.mangaUpdateCalls)
    }

    /**
     * GAP-109 warm-on-failure: the bare seed throws (keiyoushi's empty-memo case), so pages warms via
     * the series fetch, finds the memo-bearing chapter whose url matches, and returns ITS pages.
     */
    @Test
    fun `pages warms and returns the matched chapter's pages when the bare seed throws`() {
        // The warm chapter (memo-bearing in production) is modelled by reference identity: the source's
        // getPageList succeeds ONLY for this exact instance and throws for the bare seed pages() builds.
        val warmChapter = chapterAt("/ch/1")
        val source =
            FakePagesSource(
                onPageList = { chapter ->
                    if (chapter === warmChapter) listOf(Page(index = 0, url = "https://x/warm"))
                    else throw IllegalStateException("Refresh Chapter List")
                },
                onMangaUpdate = { listOf(warmChapter) },
            )

        val response = SourceCalls.pages(source, "/ch/1", mangaUrl = "/series/1")

        assertEquals(listOf("https://x/warm"), response.pages.map { it.url })
        assertEquals(1, source.mangaUpdateCalls)
    }

    /**
     * GAP-109 error preservation: the warm fetch ITSELF throwing must not mask the bare failure — the
     * ORIGINAL bare-seed exception is rethrown so failure classification is unchanged.
     */
    @Test
    fun `pages rethrows the original bare error when the warm fetch throws`() {
        val bareError = IllegalStateException("Refresh Chapter List")
        val source =
            FakePagesSource(
                onPageList = { throw bareError },
                onMangaUpdate = { throw RuntimeException("cloudflare on the series page") },
            )

        val thrown =
            assertFailsWith<IllegalStateException> { SourceCalls.pages(source, "/ch/1", mangaUrl = "/series/1") }

        assertSame(bareError, thrown)
        assertEquals(1, source.mangaUpdateCalls)
    }

    /**
     * GAP-109 error preservation: a successful warm fetch that contains NO chapter matching the url is
     * treated as no help — the original bare-seed exception is rethrown, not swallowed.
     */
    @Test
    fun `pages rethrows the original bare error when no warm chapter url matches`() {
        val bareError = IllegalStateException("Refresh Chapter List")
        val source =
            FakePagesSource(
                onPageList = { throw bareError },
                onMangaUpdate = { listOf(chapterAt("/ch/OTHER")) },
            )

        val thrown =
            assertFailsWith<IllegalStateException> { SourceCalls.pages(source, "/ch/1", mangaUrl = "/series/1") }

        assertSame(bareError, thrown)
        assertEquals(1, source.mangaUpdateCalls)
    }

    /**
     * GAP-109 no-warm guard: a blank mangaUrl leaves nothing to warm from, so the bare-seed failure
     * propagates as-is and the series fetch is NEVER attempted.
     */
    @Test
    fun `pages propagates the bare error without warming when mangaUrl is blank`() {
        val bareError = IllegalStateException("Refresh Chapter List")
        val source = FakePagesSource(onPageList = { throw bareError })

        val thrown = assertFailsWith<IllegalStateException> { SourceCalls.pages(source, "/ch/1", mangaUrl = "") }

        assertSame(bareError, thrown)
        assertEquals(0, source.mangaUpdateCalls)
    }
}
