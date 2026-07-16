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
}
