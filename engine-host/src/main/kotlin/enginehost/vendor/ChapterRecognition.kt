package enginehost.vendor

/*
 * VENDORED from Suwayomi-Server @ commit b0bc8c6fb3cdd050dbbfdeb50a9ee1b0d2cbad45
 * (server/src/main/kotlin/eu/kanade/tachiyomi/util/chapter/ChapterRecognition.kt — see
 * Dockerfile SUWAYOMI_COMMIT for the pinned version this engine-host build tracks).
 *
 * This is Suwayomi-SERVER code, not part of the Mihon extension-api a source links against, so
 * it is not reachable through the composite-build substitution engine-host otherwise consumes
 * (implementation("suwayomi:server") wires in the source-api + network stack, not this
 * service-layer utility). Copied in verbatim (package renamed to enginehost.vendor to avoid a
 * duplicate-class collision with the same fully-qualified name on the suwayomi:server
 * dependency's own classpath) so SourceCalls.toChapterDto can mirror the number-recognition step
 * Suwayomi's own Chapter.kt runs before storing a chapter (see SourceCalls.kt's toChapterDto).
 *
 * NO logic changes from the original. Original license: Suwayomi-Server is MPL-2.0.
 *
 * -R> = regex conversion.
 */
object ChapterRecognition {
    private const val NUMBER_PATTERN = """([0-9]+)(\.[0-9]+)?(\.?[a-z]+)?"""

    /**
     * All cases with Ch.xx
     * Mokushiroku Alice Vol.1 Ch. 4: Misrepresentation -R> 4
     */
    private val basic = Regex("""(?<=ch\.) *$NUMBER_PATTERN""")

    /**
     * Example: Bleach 567: Down With Snowwhite -R> 567
     */
    private val number = Regex(NUMBER_PATTERN)

    /**
     * Regex used to remove unwanted tags
     * Example Prison School 12 v.1 vol004 version1243 volume64 -R> Prison School 12
     */
    private val unwanted = Regex("""\b(?:v|ver|vol|version|volume|season|s)[^a-z]?[0-9]+""")

    /**
     * Regex used to remove unwanted whitespace
     * Example One Piece 12 special -R> One Piece 12special
     */
    private val unwantedWhiteSpace = Regex("""\s(?=extra|special|omake)""")

    fun parseChapterNumber(
        mangaTitle: String,
        chapterName: String,
        chapterNumber: Double? = null,
    ): Double {
        // If chapter number is known return.
        if (chapterNumber != null && (chapterNumber == -2.0 || chapterNumber > -1.0)) {
            return chapterNumber
        }

        // Get chapter title with lower case
        val cleanChapterName =
            chapterName
                .lowercase()
                // Remove manga title from chapter title.
                .replace(mangaTitle.lowercase(), "")
                .trim()
                // Remove comma's or hyphens.
                .replace(',', '.')
                .replace('-', '.')
                // Remove unwanted white spaces.
                .replace(unwantedWhiteSpace, "")

        val numberMatch = number.findAll(cleanChapterName)

        when {
            numberMatch.none() -> {
                return chapterNumber ?: -1.0
            }

            numberMatch.count() > 1 -> {
                // Remove unwanted tags.
                unwanted.replace(cleanChapterName, "").let { name ->
                    // Check base case ch.xx
                    basic.find(name)?.let { return getChapterNumberFromMatch(it) }

                    // need to find again first number might already removed
                    number.find(name)?.let { return getChapterNumberFromMatch(it) }
                }
            }
        }

        // return the first number encountered
        return getChapterNumberFromMatch(numberMatch.first())
    }

    /**
     * Check if chapter number is found and return it
     * @param match result of regex
     * @return chapter number if found else null
     */
    private fun getChapterNumberFromMatch(match: MatchResult): Double =
        match.let {
            val initial = it.groups[1]?.value?.toDouble()!!
            val subChapterDecimal = it.groups[2]?.value
            val subChapterAlpha = it.groups[3]?.value
            val addition = checkForDecimal(subChapterDecimal, subChapterAlpha)
            initial.plus(addition)
        }

    /**
     * Check for decimal in received strings
     * @param decimal decimal value of regex
     * @param alpha alpha value of regex
     * @return decimal/alpha float value
     */
    private fun checkForDecimal(
        decimal: String?,
        alpha: String?,
    ): Double {
        if (!decimal.isNullOrEmpty()) {
            return decimal.toDouble()
        }

        if (!alpha.isNullOrEmpty()) {
            if (alpha.contains("extra")) {
                return 0.99
            }

            if (alpha.contains("omake")) {
                return 0.98
            }

            if (alpha.contains("special")) {
                return 0.97
            }

            val trimmedAlpha = alpha.trimStart('.')
            if (trimmedAlpha.length == 1) {
                return parseAlphaPostFix(trimmedAlpha[0])
            }
        }

        return 0.0
    }

    /**
     * x.a -> x.1, x.b -> x.2, etc
     */
    private fun parseAlphaPostFix(alpha: Char): Double {
        val number = alpha.code - ('a'.code - 1)
        if (number >= 10) return 0.0
        return number / 10.0
    }
}
