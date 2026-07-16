package enginehost.vendor

/*
 * VENDORED from Suwayomi-Server @ commit b0bc8c6fb3cdd050dbbfdeb50a9ee1b0d2cbad45
 * (server/src/main/kotlin/eu/kanade/tachiyomi/util/chapter/ChapterSanitizer.kt — see
 * Dockerfile SUWAYOMI_COMMIT for the pinned version this engine-host build tracks).
 *
 * This is Suwayomi-SERVER code, not part of the Mihon extension-api a source links against, so
 * it is not reachable through the composite-build substitution engine-host otherwise consumes
 * (implementation("suwayomi:server") wires in the source-api + network stack, not this
 * service-layer utility). Copied in verbatim (package renamed to enginehost.vendor to avoid a
 * duplicate-class collision with the same fully-qualified name on the suwayomi:server
 * dependency's own classpath) so SourceCalls.toChapterDto can mirror the name-sanitize step
 * Suwayomi's own Chapter.kt runs before storing a chapter (see SourceCalls.kt's toChapterDto).
 *
 * NO logic changes from the original. Original license: Suwayomi-Server is MPL-2.0.
 */
object ChapterSanitizer {
    fun String.sanitize(title: String): String =
        trim()
            .removePrefix(title)
            .trim(*CHAPTER_TRIM_CHARS)

    private val CHAPTER_TRIM_CHARS =
        arrayOf(
            // Whitespace
            ' ',
            '\u0009',
            '\u000A',
            '\u000B',
            '\u000C',
            '\u000D',
            '\u0020',
            '\u0085',
            '\u00A0',
            '\u1680',
            '\u2000',
            '\u2001',
            '\u2002',
            '\u2003',
            '\u2004',
            '\u2005',
            '\u2006',
            '\u2007',
            '\u2008',
            '\u2009',
            '\u200A',
            '\u2028',
            '\u2029',
            '\u202F',
            '\u205F',
            '\u3000',
            // Separators
            '-',
            '_',
            ',',
            ':',
        ).toCharArray()
}
