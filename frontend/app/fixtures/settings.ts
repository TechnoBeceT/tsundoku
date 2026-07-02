/**
 * Story-only fixtures for the Settings screen. NOT imported by app code — only
 * by Storybook stories — so the screen stays props-driven and backend-free.
 *
 * Mirrors the Claude Design prototype's seed state: the M12 library knobs, the
 * five seed categories (Other protected + default), an embedded engine with an
 * upgrade available, the proxied Suwayomi server config (FlareSolverr on, SOCKS
 * off), and the installed/available/repo extension sets.
 */
import type {
  DurationValue,
  EngineInfo,
  Extension,
  LibrarySettings,
  Repo,
  SettingsCategory,
  SuwayomiConfig,
  SystemInfo,
  UpgradeStep,
} from '../components/screens/settings.types'

/** The runtime-editable library knobs (2a). */
export const librarySettings: LibrarySettings = {
  refreshInterval: { value: 2, unit: 'h' },
  downloadInterval: { value: 15, unit: 'm' },
  retryBackoff: { value: 10, unit: 'm' },
  maxRetries: 5,
  staleGraceDays: 14,
  refreshConcurrency: 4,
}

/** Read-only deploy-time facts for the System card (2a). */
export const systemInfo: SystemInfo = {
  storageFolder: '/data/manga',
  serverPort: '9833',
  database: 'db:5432 / tsundoku',
}

/** The five seed categories — "Other" is protected + the default landing. */
export const settingsCategories: SettingsCategory[] = [
  { id: 'cat-manga', name: 'Manga', count: 42, isDefault: false, protected: false },
  { id: 'cat-manhwa', name: 'Manhwa', count: 28, isDefault: false, protected: false },
  { id: 'cat-manhua', name: 'Manhua', count: 11, isDefault: false, protected: false },
  { id: 'cat-comic', name: 'Comic', count: 0, isDefault: false, protected: false },
  { id: 'cat-other', name: 'Other', count: 6, isDefault: true, protected: true },
]

/** An embedded engine, running, with a newer pinned version available. */
export const engineInfo: EngineInfo = {
  mode: 'embedded',
  externalUrl: 'http://suwayomi:4567',
  runningVersion: 'v2.2.2100',
  pinnedVersion: 'v2.2.2200',
  runtimeDir: '/data/suwayomi',
  javaPath: 'java',
  status: 'running',
  upgradeAvailable: true,
  availableVersion: 'v2.2.2200',
}

/** A mid-flight upgrade stepper (Swap JAR active) — for the in-progress story. */
export const upgradeStepsInProgress: UpgradeStep[] = [
  { label: 'Clean stop', status: 'done' },
  { label: 'Backup', status: 'done' },
  { label: 'Swap JAR', status: 'active' },
  { label: 'Migration boot', status: 'pending' },
  { label: 'Verify', status: 'pending' },
]

/** The proxied Suwayomi server config: read-only DB, SOCKS off, FlareSolverr on. */
export const suwayomiConfig: SuwayomiConfig = {
  database: {
    type: 'PostgreSQL',
    url: 'jdbc:postgresql://db:5432/suwayomi',
    username: 'suwayomi',
  },
  socks: {
    enabled: false,
    version: '5',
    host: '',
    port: '1080',
    username: '',
    password: '',
  },
  flareSolverr: {
    enabled: true,
    url: 'http://flaresolverr:8191',
    timeout: { value: 60, unit: 's' },
    session: 'tsundoku',
    sessionTtl: { value: 15, unit: 'm' },
    fallback: true,
  },
}

/**
 * Installed extensions — two carry an available update (UPDATE badge). No
 * backend is running in Storybook, so iconUrl is '' here (the fallback tinted
 * square); ExtensionRow.stories.ts adds a dedicated icon fixture separately.
 */
export const installedExtensions: Extension[] = [
  { id: 'mangadex', name: 'MangaDex', lang: 'en', version: '1.4.21', hasUpdate: false, iconUrl: '' },
  { id: 'asurascans', name: 'Asura Scans', lang: 'en', version: '1.4.9', hasUpdate: true, iconUrl: '' },
  { id: 'comick', name: 'ComicK', lang: 'en', version: '2.0.3', hasUpdate: false, iconUrl: '' },
  { id: 'weebcentral', name: 'Weeb Central', lang: 'en', version: '1.2.0', hasUpdate: true, iconUrl: '' },
  { id: 'bilibili', name: 'BiliBili Comics', lang: 'zh', version: '1.3.7', hasUpdate: false, iconUrl: '' },
]

/** Available (installable) extensions. */
export const availableExtensions: Extension[] = [
  { id: 'reaperscans', name: 'Reaper Scans', lang: 'en', version: '1.5.1', hasUpdate: false, iconUrl: '' },
  { id: 'flamecomics', name: 'Flame Comics', lang: 'en', version: '1.1.2', hasUpdate: false, iconUrl: '' },
  { id: 'mangaplus', name: 'MANGA Plus', lang: 'en', version: '1.6.0', hasUpdate: false, iconUrl: '' },
  { id: 'webtoons', name: 'Webtoons', lang: 'en', version: '2.1.0', hasUpdate: false, iconUrl: '' },
  { id: 'kakao', name: 'Kakao', lang: 'ko', version: '1.0.4', hasUpdate: false, iconUrl: '' },
]

/** Extension repositories — the first is the pre-populated default. */
export const repos: Repo[] = [
  { id: 'r1', url: 'https://raw.githubusercontent.com/keiyoushi/extensions/repo/index.min.json', isDefault: true },
  { id: 'r2', url: 'https://raw.githubusercontent.com/my-org/tachi-extras/repo/index.min.json', isDefault: false },
]

/** Background extension update-check cadence (2e). */
export const extCheckInterval: DurationValue = { value: 12, unit: 'h' }
