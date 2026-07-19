/**
 * useErrorDiagnosis — the human error-diagnosis engine (ported from Kaizoku.GO's
 * `diagnoseError`). Given a stored `error_category` (from `pkg/errorclass`) and
 * the raw failure message, it produces a plain-language DIAGNOSIS the owner can
 * act on: a title, a one-paragraph explanation of what the category means, and an
 * ordered list of concrete suggestions to try.
 *
 * The diagnosis is driven off the CATEGORY (the backend already classified the
 * error) — the raw message is carried through for the forensic detail but never
 * re-parsed here; classification lives in exactly one place (`pkg/errorclass`,
 * §2 DRY). An absent or unrecognised category falls back to the `unknown`
 * diagnosis while preserving whatever label the category carried.
 *
 * Pure + stateless: it returns a single `diagnose(category, message)` function so
 * a component (the event-detail modal) can diagnose on demand. No fetching.
 */
import { categoryMeta, type ErrorCategoryKey } from '~/utils/errorCategory'

/** The diagnosis rendered for a failed event. */
export interface ErrorDiagnosis {
  /** The resolved category key (falls back to "unknown"). */
  category: string
  /** The category's human badge label (from errorCategory metadata). */
  categoryLabel: string
  /** A short headline naming the failure. */
  title: string
  /** One paragraph explaining what this category means. */
  explanation: string
  /** Ordered, concrete things to try. */
  suggestions: string[]
}

/** The per-category diagnosis text (title/explanation/suggestions). */
type DiagnosisText = Pick<ErrorDiagnosis, 'title' | 'explanation' | 'suggestions'>

const DIAGNOSES: Record<ErrorCategoryKey, DiagnosisText> = {
  captcha: {
    title: 'Blocked by anti-bot protection',
    explanation:
      'The source is reachable but is gating access behind a Cloudflare or CAPTCHA challenge, so the request never got past the interstitial.',
    suggestions: [
      'Warm this source to refresh its challenge clearance',
      'Confirm FlareSolverr is running and reachable in Settings',
      'The source may have raised its protection — retry in a few minutes',
    ],
  },
  rate_limit: {
    title: 'Rate limited by the source',
    explanation:
      'The source rejected requests as too frequent (HTTP 429). It is working, but it is throttling how often Tsundoku may call it.',
    suggestions: [
      'Lower the download concurrency in Settings',
      'Increase the retry backoff so attempts space out',
      'Wait for the limit window to reset — this usually clears itself',
    ],
  },
  not_found: {
    title: 'Resource not found',
    explanation:
      'The requested manga or chapter no longer exists at the source (HTTP 404). The source responded cleanly — the item is simply gone.',
    suggestions: [
      'The series may have been removed or renumbered upstream',
      'Re-match the series to a current source',
      'Remove the dead source from the series if it has moved',
    ],
  },
  server_error: {
    title: 'Source returned a server error',
    explanation:
      'The source itself failed with a 5xx error. This is a fault on the source’s side, not with your setup or network.',
    suggestions: [
      'Usually transient — the retry cycle will pick it up automatically',
      'Check the source’s own status if it persists',
      'If it stays down for hours, the source may be offline',
    ],
  },
  network: {
    title: 'Network failure',
    explanation:
      'The request never reached the source — a connection was refused or reset, or DNS could not resolve the host.',
    suggestions: [
      'Check the engine host’s network and DNS',
      'Verify any SOCKS proxy or network-endpoint binding for this source',
      'Transient drops self-heal on the next cycle',
    ],
  },
  timeout: {
    title: 'Request timed out',
    explanation:
      'The source took too long to respond and the request hit its deadline. Cold anti-bot sessions are the usual cause.',
    suggestions: [
      'Warm the source — a cold Cloudflare session is slow on the first hit',
      'Raise the source timeout if it is consistently slow',
      'Check FlareSolverr latency in the Sources metrics',
    ],
  },
  parse: {
    title: 'Response could not be read',
    explanation:
      'The source responded, but its content could not be parsed. The site’s layout or API most likely changed underneath the extension.',
    suggestions: [
      'The source extension probably needs an update',
      'Refresh the extension repositories in Settings',
      'Report the source to its extension maintainer if it persists',
    ],
  },
  no_pages: {
    title: 'No readable pages',
    explanation:
      'The chapter exists but resolved to zero images — a placeholder, an unreleased chapter, or a broken upload.',
    suggestions: [
      'The chapter may not be published yet — re-check later',
      'Try a different source for this chapter',
      'Rank another provider higher if this one is unreliable',
    ],
  },
  unknown: {
    title: 'Unclassified error',
    explanation:
      'This failure did not match a known category. The raw message below carries the specific cause.',
    suggestions: [
      'Read the raw error for the specific cause',
      'Retry the operation',
      'If it recurs, watch this source’s health over the window',
    ],
  },
}

export function useErrorDiagnosis() {
  /**
   * diagnose — resolve a stored category (+ its raw message) to a human
   * diagnosis. An unmapped/absent category yields the `unknown` diagnosis.
   */
  function diagnose(category: string | null | undefined, _message?: string | null): ErrorDiagnosis {
    const key: ErrorCategoryKey = category != null && category in DIAGNOSES
      ? (category as ErrorCategoryKey)
      : 'unknown'
    const text = DIAGNOSES[key]
    return {
      category: key,
      categoryLabel: categoryMeta(category).label,
      title: text.title,
      explanation: text.explanation,
      suggestions: text.suggestions,
    }
  }

  return { diagnose }
}
