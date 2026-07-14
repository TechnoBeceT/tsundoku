// Package sync is the pure tracker-sync RULE KERNEL — the safety-critical
// heart of progress push/pull between Tsundoku's local chapter state and a
// bound tracker (AniList/MAL/Kitsu/MangaUpdates). It implements EXACTLY the
// rules ratified in spec/trackers-and-rich-library-umbrella-v2 §6 and
// spec/trackers-sync-phase4 §2:
//
//   - never-regress push (NextPush)
//   - max-wins conflict resolution (Converge)
//   - integer-count-tracker truncation (TruncateForInteger)
//   - unparseable-chapter filtering + monotonic-stop mark-read walk
//     (SyncableNumbers, MarkReadUpTo)
//   - total>0 && last==total auto-complete (ShouldAutoComplete)
//   - native-score-to-0-10 display normalization (NormalizeTo10)
//
// This package is deliberately ENT-FREE and NETWORK-FREE — stdlib only, no
// internal/ent import, no HTTP client. It holds no state and makes no I/O;
// every function is a pure, deterministic transformation over plain values.
// Phase 4c's push/pull services (internal/tracker/sync's future sibling
// packages, or a service layer built on top of this one) are the ONLY
// callers that touch the network/DB — they apply these rules, this package
// only DECIDES what the rules say.
package sync
