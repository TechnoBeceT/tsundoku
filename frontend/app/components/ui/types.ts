/**
 * Shared types for the `ui/` atom kit.
 *
 * `ChapterState` is the seven-value chapter-state enum the backend's
 * `Chapter.state` carries — the single union the `StatusBadge` atom (and any
 * future atom that keys off a chapter state) maps over. Each value lines up
 * 1:1 with a `--state-<value>-{fg,bg,dot}` token triple in `tokens/status.css`.
 */
export type ChapterState =
  | 'wanted'
  | 'downloading'
  | 'downloaded'
  | 'upgrade_available'
  | 'upgrading'
  | 'failed'
  | 'permanently_failed'
