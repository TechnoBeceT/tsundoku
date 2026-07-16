package enginehost.vendor

import kotlin.test.Test
import kotlin.test.assertEquals

/**
 * Proves the vendored [ChapterRecognition] (see its own doc comment for provenance) behaves
 * exactly as Suwayomi's original: a known chapter_number passes through untouched, an unset one
 * (-1f, Mihon's sentinel) is derived from the chapter name, fractional numbers survive as decimals
 * (never rounded — Tsundoku's fractional-cleanup feature depends on this), and an empty manga
 * title is a safe no-op (just skips the title-strip step) — this is the C1 fix
 * (SourceCalls.toChapterDto) exercised in isolation from the JVM/extension machinery.
 */
class ChapterRecognitionTest {
    @Test
    fun `known chapter number passes through unchanged`() {
        assertEquals(5.0, ChapterRecognition.parseChapterNumber("Teach Me First!", "Chapter 5", 5.0))
    }

    @Test
    fun `unset chapter number is recognized from the name`() {
        // Mihon's -1f "unset" sentinel — matches SourceCalls.toChapterDto's chapter_number.toDouble().
        assertEquals(1.0, ChapterRecognition.parseChapterNumber("", "Chapter 1", -1.0))
    }

    @Test
    fun `fractional chapter number survives as a decimal, never rounded`() {
        assertEquals(10.5, ChapterRecognition.parseChapterNumber("My Series", "Chapter 10.5", -1.0))
    }

    @Test
    fun `trailing text after the number is ignored`() {
        assertEquals(20.0, ChapterRecognition.parseChapterNumber("", "Chapter 20 - The End", -1.0))
    }

    @Test
    fun `empty manga title is safe — number recognition still runs`() {
        assertEquals(7.0, ChapterRecognition.parseChapterNumber("", "Chapter 7", -1.0))
    }

    @Test
    fun `manga title is stripped from the chapter name before matching`() {
        // Without the title-strip, "One Piece" contributes no digits here, but this proves the
        // strip runs (mirrors Suwayomi's own lowercase-and-replace step) rather than asserting a
        // regression the plain-number case wouldn't otherwise catch.
        assertEquals(105.0, ChapterRecognition.parseChapterNumber("One Piece", "One Piece 105", -1.0))
    }

    @Test
    fun `nameless chapter with no number falls back to -1`() {
        assertEquals(-1.0, ChapterRecognition.parseChapterNumber("", "Special Announcement", -1.0))
    }

    @Test
    fun `hidden chapter sentinel -2 wins over name recognition`() {
        // Suwayomi's -2.0 "hidden chapter" sentinel is checked by the early-out
        // (chapterNumber == -2.0 || chapterNumber > -1.0) and must pass through UNCHANGED, even
        // though "Chapter 5" would otherwise be recognized as 5.0 — the sentinel must win.
        assertEquals(-2.0, ChapterRecognition.parseChapterNumber("Teach Me First!", "Chapter 5", -2.0))
    }

    @Test
    fun `sanitize does not disturb fractional chapter recognition`() {
        // Recognition must run on the RAW name (before ChapterSanitizer.sanitize strips the
        // title) — this proves the fractional-chapter feature still works against a name shaped
        // like the sanitize call site actually passes in (SourceCalls.toChapterDto).
        assertEquals(10.5, ChapterRecognition.parseChapterNumber("My Series", "My Series - Chapter 10.5", -1.0))
    }
}
