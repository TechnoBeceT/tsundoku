package enginehost

import eu.kanade.tachiyomi.source.Source
import eu.kanade.tachiyomi.source.model.FilterList
import eu.kanade.tachiyomi.source.model.MangasPage
import eu.kanade.tachiyomi.source.model.Page
import eu.kanade.tachiyomi.source.model.SChapter
import eu.kanade.tachiyomi.source.model.SManga
import eu.kanade.tachiyomi.source.model.SMangaUpdate
import eu.kanade.tachiyomi.source.online.HttpSource
import okhttp3.Headers
import okhttp3.OkHttpClient
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext
import java.util.concurrent.CountDownLatch
import java.util.concurrent.ExecutionException
import java.util.concurrent.Executors
import java.util.concurrent.Future
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertIs
import kotlin.test.assertSame
import kotlin.test.assertTrue

private class HeldMangaUpdateSource(private val failure: Throwable? = null, private val ignoreCancellation: Boolean = false, private val yieldUpdate: Boolean = false) : Source {
    override val id = 1L
    override val name = "Held manga update"
    override val lang = "en"
    override val supportsLatest = false
    val entered = CountDownLatch(1)
    val release = CompletableDeferred<Unit>()
    val calls = AtomicInteger()
    private val active = java.util.concurrent.ConcurrentHashMap<String, Boolean>()
    // Equal numeric ids and values must not merge distinct source instances.
    override fun equals(other: Any?) = other is HeldMangaUpdateSource
    override fun hashCode() = 1

    override suspend fun getMangaUpdate(manga: SManga, chapters: List<SChapter>, fetchDetails: Boolean, fetchChapters: Boolean): SMangaUpdate {
        check(active.putIfAbsent(manga.url, true) == null) { "getMangaUpdate already running for ${manga.url}" }
        try {
            if (calls.incrementAndGet() == 1) {
                entered.countDown()
                if (ignoreCancellation) withContext(NonCancellable) { release.await() } else release.await()
                failure?.let { throw it }
            }
            if (yieldUpdate) kotlinx.coroutines.yield()
            val chapter = SChapter.create().apply { url = "/chapter"; name = "Chapter 1"; chapter_number = 1f }
            return SMangaUpdate(manga, listOf(chapter))
        } finally { active.remove(manga.url) }
    }
    override suspend fun getPageList(chapter: SChapter): List<Page> {
        if (calls.get() == 0) throw Exception("Refresh Chapter List")
        return listOf(Page(0, "/page", "https://images.example.test/page"))
    }
    override suspend fun getPopularManga(page: Int): MangasPage = error("unused")
    override suspend fun getLatestUpdates(page: Int): MangasPage = error("unused")
    override suspend fun getSearchManga(page: Int, query: String, filters: FilterList): MangasPage = error("unused")
}

private class AliasedMangaUpdateSource(val held: HeldMangaUpdateSource) : HttpSource() {
    override val id = 1L
    override val name = "Aliased manga update"
    override val lang = "en"
    override val supportsLatest = false
    override val baseUrl = "https://source.example.test"
    override val client = OkHttpClient()
    override fun headersBuilder() = Headers.Builder()
    override fun getMangaUrl(manga: SManga) = "$baseUrl/visible"
    override suspend fun getSearchManga(page: Int, query: String, filters: FilterList): MangasPage =
        MangasPage(listOf(SManga.create().apply { url = "/manga"; title = "Hydrated" }), false)
    override suspend fun getMangaUpdate(manga: SManga, chapters: List<SChapter>, fetchDetails: Boolean, fetchChapters: Boolean): SMangaUpdate =
        held.getMangaUpdate(manga, chapters, fetchDetails, fetchChapters)
}

class MangaUpdateExclusionTest {
    @Test
    fun `details and chapters share exclusion without caching results`() = withSource { source, submit ->
        val first = submit { SourceCalls.mangaDetails(source, "/manga", AddressMode.DIRECT) }
        awaitSource(source)
        val next = submit { SourceCalls.chapters(source, "/manga", "different title", AddressMode.DIRECT) }
        assertBlocked(next)
        source.release.complete(Unit)
        first.get(5, TimeUnit.SECONDS); next.get(5, TimeUnit.SECONDS)
        assertEquals(2, source.calls.get())
    }
    @Test
    fun `page warmup and chapters share exclusion`() = withSource { source, submit ->
        val first = submit { SourceCalls.pages(source, "/chapter", "/manga", AddressMode.DIRECT) }
        awaitSource(source)
        val next = submit { SourceCalls.chapters(source, "/manga", addressMode = AddressMode.DIRECT) }
        assertBlocked(next)
        source.release.complete(Unit)
        first.get(5, TimeUnit.SECONDS); next.get(5, TimeUnit.SECONDS)
        assertEquals(2, source.calls.get())
    }
    @Test
    fun `different manga remain concurrent`() = withSource { source, submit ->
        val first = submit { SourceCalls.chapters(source, "/first", addressMode = AddressMode.DIRECT) }
        awaitSource(source)
        submit { SourceCalls.chapters(source, "/second", addressMode = AddressMode.DIRECT) }.get(5, TimeUnit.SECONDS)
        assertTrue(!first.isDone)
        source.release.complete(Unit); first.get(5, TimeUnit.SECONDS)
    }
    @Test
    fun `equal source values and ids remain independent`() = withSource { source, submit ->
        val other = HeldMangaUpdateSource().apply { release.complete(Unit) }
        val first = submit { SourceCalls.chapters(source, "/manga", addressMode = AddressMode.DIRECT) }
        awaitSource(source)
        submit { SourceCalls.chapters(other, "/manga", addressMode = AddressMode.DIRECT) }.get(5, TimeUnit.SECONDS)
        assertTrue(!first.isDone)
        source.release.complete(Unit); first.get(5, TimeUnit.SECONDS)
    }
    @Test
    fun `cancelled waiter never invokes source and later call progresses`() = withSource { source, submit ->
        val first = submit { SourceCalls.chapters(source, "/manga", addressMode = AddressMode.DIRECT) }
        awaitSource(source)
        val cancellation = SourceCallCancellation()
        val waiting = submit { SourceCalls.chapters(source, "/manga", addressMode = AddressMode.DIRECT, cancellation = cancellation) }
        assertBlocked(waiting)
        cancellation.cancel(); assertCancelled(waiting)
        assertEquals(1, source.calls.get())
        source.release.complete(Unit); first.get(5, TimeUnit.SECONDS)
        submit { SourceCalls.chapters(source, "/manga", addressMode = AddressMode.DIRECT) }.get(5, TimeUnit.SECONDS)
        assertEquals(2, source.calls.get())
    }
    @Test
    fun `active cancellation keeps ownership until source actually returns`() = withSource(HeldMangaUpdateSource(ignoreCancellation = true)) { source, submit ->
        val cancellation = SourceCallCancellation()
        val first = submit { SourceCalls.chapters(source, "/manga", addressMode = AddressMode.DIRECT, cancellation = cancellation) }
        awaitSource(source); cancellation.cancel()
        val next = submit { SourceCalls.chapters(source, "/manga", addressMode = AddressMode.DIRECT) }
        assertBlocked(next); assertEquals(1, source.calls.get())
        source.release.complete(Unit); assertCancelled(first)
        next.get(5, TimeUnit.SECONDS); assertEquals(2, source.calls.get())
    }
    @Test
    fun `exception identity survives and exclusion releases`() {
        val cause = IllegalStateException("source update failed")
        withSource(HeldMangaUpdateSource(failure = cause)) { source, submit ->
            val first = submit { SourceCalls.chapters(source, "/manga", addressMode = AddressMode.DIRECT) }
            awaitSource(source)
            val next = submit { SourceCalls.chapters(source, "/manga", addressMode = AddressMode.DIRECT) }
            assertBlocked(next); source.release.complete(Unit)
            assertSame(cause, assertFailsWith<ExecutionException> { first.get(5, TimeUnit.SECONDS) }.cause)
            next.get(5, TimeUnit.SECONDS); assertEquals(2, source.calls.get())
        }
    }
    @Test
    fun `hydrated alias and direct address share resolved seed exclusion`() = withSource { held, submit ->
        val source = AliasedMangaUpdateSource(held)
        val first = submit { SourceCalls.mangaDetails(source, "/manga", AddressMode.DIRECT) }
        awaitSource(held)
        val next = submit { SourceCalls.chapters(source, "/different-wire-address", addressMode = AddressMode.URL_SEARCH, webUrl = "${source.baseUrl}/visible") }
        assertBlocked(next)
        held.release.complete(Unit)
        first.get(5, TimeUnit.SECONDS)
        next.get(5, TimeUnit.SECONDS)
        assertEquals(2, held.calls.get())
    }

    @Test
    fun `repeated registry handoff and retirement never overlap or retain sources`() {
        val source = HeldMangaUpdateSource(yieldUpdate = true).apply { release.complete(Unit) }
        val executor = Executors.newFixedThreadPool(8)
        try {
            repeat(40) {
                val start = CountDownLatch(1)
                val calls = (1..8).map {
                    executor.submit<Any> {
                        assertTrue(start.await(5, TimeUnit.SECONDS))
                        SourceCalls.chapters(source, "/manga", addressMode = AddressMode.DIRECT)
                    }
                }
                start.countDown()
                calls.forEach { it.get(5, TimeUnit.SECONDS) }
                val registry = SourceCalls::class.java.getDeclaredField("mangaUpdateGates").apply { isAccessible = true }.get(SourceCalls) as Map<*, *>
                assertTrue(registry.isEmpty(), "completed handoff retained a source gate")
            }
            assertEquals(320, source.calls.get())
        } finally {
            executor.shutdownNow()
            assertTrue(executor.awaitTermination(5, TimeUnit.SECONDS))
        }
    }

    private fun awaitSource(source: HeldMangaUpdateSource) = assertTrue(source.entered.await(5, TimeUnit.SECONDS))
    private fun assertBlocked(result: Future<*>) { assertFailsWith<TimeoutException> { result.get(200, TimeUnit.MILLISECONDS) } }
    private fun assertCancelled(result: Future<*>) {
        assertIs<java.util.concurrent.CancellationException>(assertFailsWith<ExecutionException> { result.get(5, TimeUnit.SECONDS) }.cause)
    }
    private fun withSource(source: HeldMangaUpdateSource = HeldMangaUpdateSource(), block: (HeldMangaUpdateSource, (() -> Any) -> Future<Any>) -> Unit) {
        val executor = Executors.newFixedThreadPool(3)
        try {
            block(source) { call ->
                val entered = CountDownLatch(1)
                executor.submit<Any> { entered.countDown(); call() }.also { assertTrue(entered.await(5, TimeUnit.SECONDS)) }
            }
        } finally {
            source.release.complete(Unit)
            executor.shutdownNow()
            assertTrue(executor.awaitTermination(5, TimeUnit.SECONDS))
        }
    }
}
