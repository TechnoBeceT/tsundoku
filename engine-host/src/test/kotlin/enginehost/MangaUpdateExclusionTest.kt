package enginehost

import eu.kanade.tachiyomi.source.Source
import eu.kanade.tachiyomi.source.model.FilterList
import eu.kanade.tachiyomi.source.model.MangasPage
import eu.kanade.tachiyomi.source.model.Page
import eu.kanade.tachiyomi.source.model.SChapter
import eu.kanade.tachiyomi.source.model.SManga
import eu.kanade.tachiyomi.source.model.SMangaUpdate
import eu.kanade.tachiyomi.source.online.HttpSource
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
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

private class InternallyUpdatingMangaSource(val held: HeldMangaUpdateSource) : HttpSource() {
    override val id = 1L
    override val name = "Internally updating URL search"
    override val lang = "en"
    override val supportsLatest = false
    override val baseUrl = "https://source.example.test"
    override val client = OkHttpClient()
    val searches = AtomicInteger()
    val queries = java.util.concurrent.ConcurrentLinkedQueue<String>()
    private val hydratedChapters = java.util.Collections.synchronizedSet(
        java.util.Collections.newSetFromMap(java.util.IdentityHashMap<SChapter, Boolean>()),
    )
    override fun headersBuilder() = Headers.Builder()
    override fun getMangaUrl(manga: SManga) = if (manga.url.startsWith("https://")) manga.url else "$baseUrl${manga.url}"
    override suspend fun getSearchManga(page: Int, query: String, filters: FilterList): MangasPage {
        searches.incrementAndGet()
        queries.add(query)
        val lookup = query.toHttpUrlOrNull() ?: return MangasPage(emptyList(), false)
        val seed = SManga.create().apply { url = lookup.encodedPath; title = "Hydrated" }
        // Models the extension's internal update, beyond the explicit host call.
        val update = getMangaUpdate(seed, emptyList(), fetchDetails = true, fetchChapters = false)
        return MangasPage(listOf(update.manga), false)
    }
    override suspend fun getMangaUpdate(manga: SManga, chapters: List<SChapter>, fetchDetails: Boolean, fetchChapters: Boolean): SMangaUpdate =
        held.getMangaUpdate(manga, chapters, fetchDetails, fetchChapters).also { hydratedChapters.addAll(it.chapters) }
    override suspend fun getPageList(chapter: SChapter): List<Page> {
        if (!hydratedChapters.contains(chapter)) throw Exception("Refresh Chapter List")
        return listOf(Page(0, "/page", "https://images.example.test/page"))
    }
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
                assertRegistriesRetired()
            }
            assertEquals(320, source.calls.get())
        } finally {
            executor.shutdownNow()
            assertTrue(executor.awaitTermination(5, TimeUnit.SECONDS))
        }
    }

    @Test
    fun `URL search internal update waits for direct manga update`() = withSource { held, submit ->
        val source = InternallyUpdatingMangaSource(held)
        val first = submit { SourceCalls.mangaDetails(source, "/manga", AddressMode.DIRECT) }
        awaitSource(held)
        val next = submit { SourceCalls.chapters(source, "/wire-alias", addressMode = AddressMode.URL_SEARCH, webUrl = "${source.baseUrl}/manga") }
        assertBlocked(next)
        assertEquals(0, source.searches.get(), "waiting hydration entered the extension")
        held.release.complete(Unit)
        first.get(5, TimeUnit.SECONDS)
        next.get(5, TimeUnit.SECONDS)
        assertEquals(3, held.calls.get(), "hydration and explicit update must both run")
    }

    @Test
    fun `page warmup hydration cannot overlap chapters hydration`() = withSource { held, submit ->
        val source = InternallyUpdatingMangaSource(held)
        val first = submit { SourceCalls.pages(source, "/chapter", "/wire-alias", AddressMode.URL_SEARCH, "${source.baseUrl}/manga") }
        awaitSource(held)
        val next = submit { SourceCalls.chapters(source, "/another-alias", addressMode = AddressMode.URL_SEARCH, webUrl = "${source.baseUrl}/manga") }
        assertBlocked(next)
        assertEquals(1, source.searches.get())
        held.release.complete(Unit)
        first.get(5, TimeUnit.SECONDS)
        next.get(5, TimeUnit.SECONDS)
        assertEquals(4, held.calls.get())
    }

    @Test
    fun `cancelled hydration waiter invokes neither search nor update`() = withSource { held, submit ->
        val source = InternallyUpdatingMangaSource(held)
        val first = submit { SourceCalls.chapters(source, "/manga", addressMode = AddressMode.DIRECT) }
        awaitSource(held)
        val cancellation = SourceCallCancellation()
        val waiting = submit { SourceCalls.chapters(source, "/wire-alias", addressMode = AddressMode.URL_SEARCH, webUrl = "${source.baseUrl}/manga", cancellation = cancellation) }
        assertBlocked(waiting)
        cancellation.cancel()
        assertCancelled(waiting)
        assertEquals(0, source.searches.get())
        assertEquals(1, held.calls.get())
        held.release.complete(Unit)
        first.get(5, TimeUnit.SECONDS)
        submit { SourceCalls.chapters(source, "/wire-alias", addressMode = AddressMode.URL_SEARCH, webUrl = "${source.baseUrl}/manga") }.get(5, TimeUnit.SECONDS)
        assertEquals(3, held.calls.get())
    }

    @Test
    fun `hydration of different manga retains parallelism`() = withSource { held, submit ->
        val source = InternallyUpdatingMangaSource(held)
        val first = submit { SourceCalls.chapters(source, "/wire-one", addressMode = AddressMode.URL_SEARCH, webUrl = "${source.baseUrl}/first") }
        awaitSource(held)
        submit { SourceCalls.chapters(source, "/wire-two", addressMode = AddressMode.URL_SEARCH, webUrl = "${source.baseUrl}/second") }.get(5, TimeUnit.SECONDS)
        assertTrue(!first.isDone)
        held.release.complete(Unit)
        first.get(5, TimeUnit.SECONDS)
        assertEquals(4, held.calls.get())
    }

    @Test
    fun `URL search on distinct actual source instances remains independent`() = withSource { held, submit ->
        val source = InternallyUpdatingMangaSource(held)
        val otherHeld = HeldMangaUpdateSource().apply { release.complete(Unit) }
        val otherSource = InternallyUpdatingMangaSource(otherHeld)
        val first = submit { SourceCalls.chapters(source, "/alias", addressMode = AddressMode.URL_SEARCH, webUrl = "${source.baseUrl}/manga") }
        awaitSource(held)
        submit { SourceCalls.chapters(otherSource, "/alias", addressMode = AddressMode.URL_SEARCH, webUrl = "${otherSource.baseUrl}/manga") }.get(5, TimeUnit.SECONDS)
        assertTrue(!first.isDone)
        held.release.complete(Unit)
        first.get(5, TimeUnit.SECONDS)
    }

    @Test
    fun `unknown mode hydration shares the exact public URL with direct mode`() = withSource { held, submit ->
        val source = InternallyUpdatingMangaSource(held)
        val first = submit { SourceCalls.chapters(source, "/manga", addressMode = AddressMode.DIRECT) }
        awaitSource(held)
        val next = submit { SourceCalls.chapters(source, "${source.baseUrl}/manga", addressMode = AddressMode.UNKNOWN) }
        assertBlocked(next)
        assertEquals(0, source.searches.get())
        held.release.complete(Unit)
        first.get(5, TimeUnit.SECONDS)
        next.get(5, TimeUnit.SECONDS)
        assertEquals(3, held.calls.get())
        assertRegistriesRetired()
    }

    @Test
    fun `matching public and seed strings do not recursively lock`() = withSource { held, submit ->
        val source = InternallyUpdatingMangaSource(held)
        val address = "${source.baseUrl}/manga"
        val first = submit { SourceCalls.chapters(source, address, addressMode = AddressMode.DIRECT) }
        awaitSource(held)
        val next = submit { SourceCalls.chapters(source, address, addressMode = AddressMode.DIRECT) }
        assertBlocked(next)
        held.release.complete(Unit)
        first.get(5, TimeUnit.SECONDS)
        next.get(5, TimeUnit.SECONDS)
        assertEquals(2, held.calls.get())
        assertRegistriesRetired()
    }

    @Test
    fun `cancelled noncooperative hydration retains its public gate until return`() = withSource(HeldMangaUpdateSource(ignoreCancellation = true)) { held, submit ->
        val source = InternallyUpdatingMangaSource(held)
        val cancellation = SourceCallCancellation()
        val first = submit { SourceCalls.chapters(source, "/alias", addressMode = AddressMode.URL_SEARCH, webUrl = "${source.baseUrl}/manga", cancellation = cancellation) }
        awaitSource(held)
        cancellation.cancel()
        val next = submit { SourceCalls.chapters(source, "/alias", addressMode = AddressMode.URL_SEARCH, webUrl = "${source.baseUrl}/manga") }
        assertBlocked(next)
        assertEquals(1, source.searches.get())
        held.release.complete(Unit)
        assertCancelled(first)
        next.get(5, TimeUnit.SECONDS)
        assertEquals(3, held.calls.get())
        assertRegistriesRetired()
    }

    @Test
    fun `hydration exception propagates unchanged and releases both identities`() {
        val cause = IllegalStateException("internal update failed")
        withSource(HeldMangaUpdateSource(failure = cause)) { held, submit ->
            val source = InternallyUpdatingMangaSource(held)
            val first = submit { SourceCalls.chapters(source, "/alias", addressMode = AddressMode.URL_SEARCH, webUrl = "${source.baseUrl}/manga") }
            awaitSource(held)
            val next = submit { SourceCalls.chapters(source, "/alias", addressMode = AddressMode.URL_SEARCH, webUrl = "${source.baseUrl}/manga") }
            assertBlocked(next)
            held.release.complete(Unit)
            assertSame(cause, assertFailsWith<ExecutionException> { first.get(5, TimeUnit.SECONDS) }.cause)
            next.get(5, TimeUnit.SECONDS)
            assertEquals(3, held.calls.get())
            assertRegistriesRetired()
        }
    }

    @Test
    fun `absolute URL search excludes direct update for same public manga`() = withSource { held, submit ->
        val source = InternallyUpdatingMangaSource(held)
        val query = "${source.baseUrl}/manga"
        val first = submit { SourceCalls.search(source, query, 1) }
        awaitSource(held)
        val next = submit { SourceCalls.chapters(source, "/manga", addressMode = AddressMode.DIRECT) }
        assertBlocked(next)
        held.release.complete(Unit)
        first.get(5, TimeUnit.SECONDS)
        next.get(5, TimeUnit.SECONDS)
        assertEquals(listOf(query), source.queries.toList())
        assertEquals(2, held.calls.get())
        assertRegistriesRetired()
    }

    @Test
    fun `absolute URL search excludes internally updating hydration`() = withSource { held, submit ->
        val source = InternallyUpdatingMangaSource(held)
        val query = "${source.baseUrl}/manga"
        val first = submit { SourceCalls.search(source, query, 1) }
        awaitSource(held)
        val next = submit { SourceCalls.pages(source, "/chapter", "/alias", AddressMode.URL_SEARCH, query) }
        assertBlocked(next)
        assertEquals(1, source.searches.get())
        held.release.complete(Unit)
        first.get(5, TimeUnit.SECONDS)
        next.get(5, TimeUnit.SECONDS)
        assertEquals(listOf(query, query), source.queries.toList())
        assertEquals(3, held.calls.get())
        assertRegistriesRetired()
    }

    @Test
    fun `cancelled absolute URL search waiter never invokes extension`() = withSource { held, submit ->
        val source = InternallyUpdatingMangaSource(held)
        val first = submit { SourceCalls.mangaDetails(source, "/manga", AddressMode.DIRECT) }
        awaitSource(held)
        val cancellation = SourceCallCancellation()
        val waiting = submit { SourceCalls.search(source, "${source.baseUrl}/manga", 1, cancellation) }
        assertBlocked(waiting)
        cancellation.cancel()
        assertCancelled(waiting)
        assertEquals(0, source.searches.get())
        assertTrue(source.queries.isEmpty())
        assertEquals(1, held.calls.get())
        held.release.complete(Unit)
        first.get(5, TimeUnit.SECONDS)
        assertRegistriesRetired()
    }

    @Test
    fun `ordinary text search stays concurrent and keeps exact query`() = withSource { held, submit ->
        val source = InternallyUpdatingMangaSource(held)
        val first = submit { SourceCalls.chapters(source, "/manga", addressMode = AddressMode.DIRECT) }
        awaitSource(held)
        val query = "Genius martial arts trainer"
        submit { SourceCalls.search(source, query, 3) }.get(5, TimeUnit.SECONDS)
        assertTrue(!first.isDone)
        assertEquals(listOf(query), source.queries.toList())
        assertEquals(1, held.calls.get())
        held.release.complete(Unit)
        first.get(5, TimeUnit.SECONDS)
        assertRegistriesRetired()
    }

    @Test
    fun `different absolute search URL retains concurrency`() = withSource { held, submit ->
        val source = InternallyUpdatingMangaSource(held)
        val first = submit { SourceCalls.chapters(source, "/first", addressMode = AddressMode.DIRECT) }
        awaitSource(held)
        val query = "${source.baseUrl}/second"
        submit { SourceCalls.search(source, query, 1) }.get(5, TimeUnit.SECONDS)
        assertTrue(!first.isDone)
        assertEquals(listOf(query), source.queries.toList())
        assertEquals(2, held.calls.get())
        held.release.complete(Unit)
        first.get(5, TimeUnit.SECONDS)
        assertRegistriesRetired()
    }

    private fun assertRegistriesRetired() {
        for (name in listOf("mangaUpdateGates", "mangaHydrationGates")) {
            val registry = SourceCalls::class.java.getDeclaredField(name).apply { isAccessible = true }.get(SourceCalls) as Map<*, *>
            assertTrue(registry.isEmpty(), "completed operation retained $name")
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
