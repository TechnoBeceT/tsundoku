/**
 * useAuth — module-singleton auth composable.
 *
 * State refs are declared at module scope so every component shares the same
 * isAuthenticated / ownerId instance (singleton pattern). The 401 interceptor
 * is registered once at module load.
 *
 * Auth is HttpOnly-cookie-based: the server sets/clears the cookie; no token is
 * ever stored in JS. checkSession() is the canonical way to confirm auth state.
 */
import { ref, readonly } from 'vue'
import { apiClient, setUnauthorizedHandler } from '~/utils/api/client'

const isAuthenticated = ref(false)
const ownerId = ref<string | null>(null)

// Register the global 401 reaction once, at module load.
// Any 401 from any endpoint flips the singleton to logged-out so the route
// guard can redirect to /login.
setUnauthorizedHandler(() => {
  isAuthenticated.value = false
  ownerId.value = null
})

export function useAuth() {
  /**
   * Checks the current session by calling GET /api/owner/me.
   * Returns true when authenticated, false otherwise.
   * Updates isAuthenticated and ownerId in place.
   */
  async function checkSession(): Promise<boolean> {
    const { data, error } = await apiClient.GET('/api/owner/me')
    if (error || !data) {
      isAuthenticated.value = false
      ownerId.value = null
      return false
    }
    ownerId.value = data.ownerId
    isAuthenticated.value = true
    return true
  }

  /**
   * Authenticates with username + password. The server sets an HttpOnly session
   * cookie on success — no token is stored in JS. Calls checkSession() to
   * populate the singleton refs after a successful login.
   * Throws with a user-readable message on failure.
   */
  async function login(username: string, password: string): Promise<void> {
    const { error } = await apiClient.POST('/api/owner/login', {
      body: { username, password },
    })
    if (error) {
      const msg
        = typeof error === 'object' && error !== null && 'message' in error
          ? String((error as { message: unknown }).message)
          : 'Invalid credentials'
      throw new Error(msg)
    }
    await checkSession()
  }

  /**
   * Logs out by calling POST /api/owner/logout (which clears the session cookie)
   * and then clears the singleton auth state.
   */
  async function logout(): Promise<void> {
    await apiClient.POST('/api/owner/logout')
    isAuthenticated.value = false
    ownerId.value = null
  }

  return {
    isAuthenticated: readonly(isAuthenticated),
    ownerId: readonly(ownerId),
    checkSession,
    login,
    logout,
  }
}
