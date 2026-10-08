package enginehost

import eu.kanade.tachiyomi.source.model.FilterList
import eu.kanade.tachiyomi.source.model.MangasPage
import eu.kanade.tachiyomi.source.model.Page
import eu.kanade.tachiyomi.source.model.SChapter
import eu.kanade.tachiyomi.source.model.SManga
import eu.kanade.tachiyomi.source.model.SMangaUpdate
import eu.kanade.tachiyomi.source.online.HttpSource
import okhttp3.Headers
import okhttp3.OkHttpClient
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertSame

private class KaynMemoSource(override val id: Long = 6622233282902198923L) : HttpSource() {
    override val name = "Kayn memo fixture"
    override val lang = "en"
    override val supportsLatest = false
    override val baseUrl = "https://kayn.fixture"
    override val client = OkHttpClient()
    override fun headersBuilder() = Headers.Builder()
    // Reference identity models the memo-bearing objects returned by the extension: a reconstructed
    // chapter with the same url cannot substitute for the original source chapter.
    val authoritative = SChapter.create().apply {
        url = "kayn-c-1938"
    }
    private val hydratedManga = SManga.create().apply { url = "kayn-s-24"; title = "Food truck" }
    var offered = listOf(authoritative)
    var updateFailure: Exception? = null
    var pageFailure: Exception? = null
    var updates = 0
    val searches = mutableListOf<String>()
    val pageInputs = mutableListOf<SChapter>()
    override fun getMangaUrl(manga: SManga) = "$baseUrl/series/comic/${if (manga === hydratedManga) "food-truck" else ""}"
    override suspend fun getSearchManga(page: Int, query: String, filters: FilterList): MangasPage {
        searches += query
        return MangasPage(listOf(hydratedManga), false)
    }
    override suspend fun getMangaUpdate(manga: SManga, chapters: List<SChapter>, fetchDetails: Boolean, fetchChapters: Boolean): SMangaUpdate {
        updates++
        check(manga === hydratedManga) { "Authoritative manga memo required" }
        check(!fetchDetails && fetchChapters)
        updateFailure?.let { throw it }
        return SMangaUpdate(manga, offered)
    }
    override suspend fun getPageList(chapter: SChapter): List<Page> {
        pageInputs += chapter
        // The installed source dereferences slug and number rather than emitting a refresh signal.
        if (chapter !== authoritative) throw NullPointerException("Missing slug memo")
        pageFailure?.let { throw it }
        return listOf(Page(0, imageUrl = "https://images.fixture/first.webp"), Page(1, imageUrl = "https://images.fixture/second.webp"))
    }
}

class KaynChapterHydrationTest {
    private fun pages(source: KaynMemoSource) = SourceCalls.pages(source, "kayn-c-1938", "kayn-s-24", AddressMode.URL_SEARCH, "https://kayn.fixture/series/comic/food-truck")

    @Test
    fun `Kayn hydrates manga and exact authoritative chapter before pages`() {
        val source = KaynMemoSource()
        val result = pages(source)
        assertEquals(listOf("https://images.fixture/first.webp", "https://images.fixture/second.webp"), result.pages.map { it.imageUrl })
        assertEquals(listOf("", ""), result.pages.map { it.url })
        assertEquals(listOf("https://kayn.fixture/series/comic/food-truck"), source.searches)
        assertEquals(1, source.updates)
        assertEquals(AddressMode.URL_SEARCH, result.addressMode)
        assertSame(source.authoritative, source.pageInputs.single())
    }

    @Test
    fun `missing or source-hidden chapter cannot reach pages`() {
        val source = KaynMemoSource().apply { offered = emptyList() }
        val error = assertFailsWith<NoSuchElementException> { pages(source) }
        assertEquals("chapter not found in refreshed chapter list: kayn-c-1938", error.message)
        assertEquals(0, source.pageInputs.size)
    }

    @Test
    fun `different chapter offer cannot substitute for the requested chapter`() {
        val source = KaynMemoSource().apply { offered = listOf(SChapter.create().apply { url = "kayn-c-1939" }) }
        assertFailsWith<NoSuchElementException> { pages(source) }
        assertEquals(0, source.pageInputs.size)
    }

    @Test
    fun `visible locked chapter preserves source refusal`() {
        val locked = IllegalStateException("This chapter is locked and requires coins to read")
        val source = KaynMemoSource().apply { pageFailure = locked }
        assertSame(locked, assertFailsWith<IllegalStateException> { pages(source) })
        assertEquals(1, source.updates)
        assertSame(source.authoritative, source.pageInputs.single())
    }

    @Test
    fun `chapter refresh failure propagates without pages or retries`() {
        val failure = java.io.IOException("HTTP 429")
        val source = KaynMemoSource().apply { updateFailure = failure }
        assertSame(failure, assertFailsWith<java.io.IOException> { pages(source) })
        assertEquals(1, source.updates)
        assertEquals(0, source.pageInputs.size)
    }

    @Test
    fun `authoritative page failure propagates without refresh retry`() {
        val failure = NullPointerException("unrelated parser fault")
        val source = KaynMemoSource().apply { pageFailure = failure }
        assertSame(failure, assertFailsWith<NullPointerException> { pages(source) })
        assertEquals(1, source.updates)
        assertEquals(1, source.pageInputs.size)
    }

    @Test
    fun `unrelated source missing memo does not trigger hydration`() {
        val source = KaynMemoSource(id = 999L)
        assertFailsWith<NullPointerException> { pages(source) }
        assertEquals(0, source.updates)
        assertEquals(0, source.searches.size)
    }

    @Test
    fun `Kayn requires series address before any source request`() {
        val source = KaynMemoSource()
        assertFailsWith<IllegalArgumentException> { SourceCalls.pages(source, "kayn-c-1938") }
        assertEquals(0, source.pageInputs.size)
        assertEquals(0, source.updates)
        assertEquals(0, source.searches.size)
    }
}
