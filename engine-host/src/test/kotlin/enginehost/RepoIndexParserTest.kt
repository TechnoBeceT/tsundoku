package enginehost

/*
 * Pins the format-aware repo-index parser (GAP-145): the Keiyoushi repo replaced the flat
 * `index.min.json` array with a wrapper `index.json` object (and a gzipped `index.pb`), which made
 * the old flat-array parser see zero extensions.
 *
 * The load-bearing assertion is the JSON↔protobuf CROSS-CHECK: JSON is ground truth, and the two
 * formats must decode to the byte-identical entry set. That is what proves the empirically-derived
 * protobuf field numbers are right — a wrong number silently shifts a field and the equality fails.
 *
 * Fixtures under src/test/resources were generated from the live index for the SAME two packages, so
 * they are guaranteed consistent and need no network at test time.
 */

import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class RepoIndexParserTest {
    private val mapper = jacksonObjectMapper()
    private val repoBase = "https://raw.githubusercontent.com/keiyoushi/extensions/repo"

    private val globalComix = "eu.kanade.tachiyomi.extension.all.globalcomix"
    private val aHottie = "eu.kanade.tachiyomi.extension.all.ahottie"

    private fun fixture(name: String): ByteArray =
        requireNotNull(javaClass.getResourceAsStream("/$name")) { "missing test fixture $name" }.use { it.readBytes() }

    private fun parseNewJson() =
        RepoIndexParser.parse(fixture("repo-index-new.json"), "$repoBase/index.json", repoBase, mapper)

    private fun parseNewPb() =
        RepoIndexParser.parse(fixture("repo-index-new.pb"), "$repoBase/index.pb", repoBase, mapper)

    @Test
    fun `new wrapper JSON yields extensions with resolved apk urls and per-source languages`() {
        val entries = parseNewJson()
        assertTrue(entries.isNotEmpty(), "new-schema JSON must parse to at least one extension")

        val gc = entries.single { it.pkg == globalComix }
        assertEquals(
            "https://cdn.jsdelivr.net/gh/keiyoushi/extensions@repo/apk/tachiyomi-all.globalcomix-v1.4.4.apk",
            gc.apkUrl,
            "the full resources.apkUrl must be carried through verbatim, not rebuilt from the repo base",
        )
        assertTrue(gc.iconUrl!!.startsWith("https://cdn.jsdelivr.net/"), "resources.iconUrl must be used directly")
        assertEquals("1.4.4", gc.version)
        assertEquals(4, gc.code, "versionCode string must parse to a Long")
        // A multi-language package: many distinct source languages collapse to "all".
        assertTrue(gc.sources.size > 1, "GlobalComix is a multi-source package")
        assertTrue(gc.sources.map { it.lang }.toSet().containsAll(setOf("en", "ja", "fr")))
        assertEquals("all", gc.lang, "distinct source languages must derive lang = all")
        assertTrue(gc.sources.all { it.id > 0 }, "source ids must parse to positive Longs")
    }

    @Test
    fun `contentWarning drives the nsfw flag`() {
        val ah = parseNewJson().single { it.pkg == aHottie }
        assertEquals(1, ah.nsfw, "CONTENT_WARNING_NSFW must map to nsfw = 1")
        assertEquals("all", ah.lang)
        assertEquals(0, parseNewJson().single { it.pkg == globalComix }.nsfw, "a non-NSFW warning must map to nsfw = 0")
    }

    @Test
    fun `gzipped protobuf decodes to the identical entry set as the JSON ground truth`() {
        val json = parseNewJson()
        val pb = parseNewPb()
        // Full structural equality is the strongest possible cross-check: it pins every protobuf
        // field number (name/pkg/apk/icon/versionName/versionCode/nsfw/sources) against the JSON.
        assertEquals(json, pb, "protobuf must decode to the same normalised entries as the JSON index")
    }

    @Test
    fun `legacy flat array still parses and resolves relative filenames against the repo base`() {
        val entries = RepoIndexParser.parse(fixture("repo-index-legacy.json"), "$repoBase/index.min.json", repoBase, mapper)
        assertEquals(2, entries.size)

        val one = entries.single { it.pkg == "eu.kanade.tachiyomi.extension.en.legacyone" }
        assertEquals("$repoBase/apk/tachiyomi-en.legacyone-v1.2.3.apk", one.apkUrl, "relative apk must resolve to absolute")
        assertEquals("$repoBase/icon/eu.kanade.tachiyomi.extension.en.legacyone.png", one.iconUrl)
        assertEquals("en", one.lang)
        assertEquals(7, one.code)

        val two = entries.single { it.pkg == "eu.kanade.tachiyomi.extension.all.legacytwo" }
        assertEquals(1, two.nsfw)
        assertEquals(2, two.sources.size)
        assertEquals(setOf("en", "es"), two.sources.map { it.lang }.toSet())
    }

    @Test
    fun `malformed or short input degrades to an empty list without crashing`() {
        assertEquals(emptyList(), RepoIndexParser.parse("{ not json".toByteArray(), "$repoBase/index.json", repoBase, mapper))
        assertEquals(emptyList(), RepoIndexParser.parse(ByteArray(0), "$repoBase/index.json", repoBase, mapper))
        assertEquals(emptyList(), RepoIndexParser.parse(byteArrayOf(1, 2, 3, 4), "$repoBase/index.pb", repoBase, mapper))
    }
}
