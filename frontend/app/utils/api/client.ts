/**
 * apiClient is the single typed HTTP client for the Tsundoku backend.
 *
 * Types are generated from the OpenAPI 3.1 contract at
 * backend/internal/api/openapi.yaml via `bun run gen:api`. Never hand-edit schema.d.ts
 * — regenerate it and run `bun run check:api-drift` to confirm alignment.
 *
 * GOTCHA: openapi-fetch sends no base URL prefix by default — the empty
 * baseUrl: "/" makes every request relative to the page origin, matching the
 * same-origin deployment topology (QCAT-020).
 */
import createClient, { type Middleware } from 'openapi-fetch'
import type { paths } from './schema.d.ts'

// A single callback the auth composable registers; invoked on any 401 so the
// global route guard can bounce to /login. Kept here to avoid a client→composable
// import cycle (composable imports client, never the reverse).
let unauthorizedHandler: (() => void) | null = null
export const setUnauthorizedHandler = (fn: () => void): void => {
  unauthorizedHandler = fn
}

const authMiddleware: Middleware = {
  onResponse({ response }) {
    if (response.status === 401) unauthorizedHandler?.()
    return response
  },
}

// apiClient is the pre-configured fetch client typed against the backend spec.
// Import this everywhere in the app instead of calling fetch() directly.
export const apiClient = createClient<paths>({ baseUrl: '/' })
apiClient.use(authMiddleware)
