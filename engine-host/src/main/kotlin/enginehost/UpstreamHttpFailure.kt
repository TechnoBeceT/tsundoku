package enginehost

import java.time.Duration
import java.time.Instant
import java.time.ZonedDateTime
import java.time.format.DateTimeFormatter
import java.time.format.DateTimeParseException

private const val MAX_RETRY_AFTER_SECONDS = 86_400L

/** A narrowly structured non-success returned by an upstream image server. */
internal class UpstreamHttpFailure(
    val upstreamStatus: Int,
    val retryAfterSeconds: Long?,
) : IllegalStateException("HTTP error $upstreamStatus")

/** Parses a Retry-After delta or HTTP date without accepting an unsafe horizon. */
internal fun parseRetryAfterSeconds(
    value: String?,
    now: Instant = Instant.now(),
): Long? {
    val candidate = value?.trim()?.takeIf { it.isNotEmpty() } ?: return null
    val seconds =
        candidate.toLongOrNull()
            ?: try {
                Duration.between(now, ZonedDateTime.parse(candidate, DateTimeFormatter.RFC_1123_DATE_TIME).toInstant()).seconds
            } catch (_: DateTimeParseException) {
                return null
            }
    return seconds.takeIf { it in 1..MAX_RETRY_AFTER_SECONDS }
}
