package enginehost

import java.time.Duration
import java.time.Instant
import java.time.ZonedDateTime
import java.time.format.DateTimeFormatter
import java.time.format.DateTimeParseException

private const val MAX_RETRY_AFTER_SECONDS = 86_400L
private val MAX_RETRY_AFTER_DURATION: Duration = Duration.ofSeconds(MAX_RETRY_AFTER_SECONDS)

/** A narrowly structured non-success returned by an upstream image server. */
internal class UpstreamHttpFailure(
    val upstreamStatus: Int,
    val retryAfterSeconds: Long?,
) : IllegalStateException("HTTP error $upstreamStatus")

/** Parses a Retry-After delta or HTTP date without accepting an unsafe horizon. */
internal fun parseRetryAfterSeconds(
    value: String?,
    now: Instant = Instant.now(),
): Long? = value?.let { parseRetryAfterSeconds(listOf(it), now) }

/** Rejects repeated fields so intermediaries cannot change the delay by reordering values. */
internal fun parseRetryAfterSeconds(
    values: List<String>,
    now: Instant = Instant.now(),
): Long? {
    if (values.size != 1) return null
    val candidate = values.single().trim().takeIf { it.isNotEmpty() } ?: return null
    candidate.toLongOrNull()?.let { seconds ->
        return seconds.takeIf { it in 1..MAX_RETRY_AFTER_SECONDS }
    }

    val duration =
        try {
            Duration.between(now, ZonedDateTime.parse(candidate, DateTimeFormatter.RFC_1123_DATE_TIME).toInstant())
        } catch (_: DateTimeParseException) {
            return null
        }
    if (duration.isZero || duration.isNegative || duration > MAX_RETRY_AFTER_DURATION) return null
    return duration.seconds + if (duration.nano == 0) 0 else 1
}
