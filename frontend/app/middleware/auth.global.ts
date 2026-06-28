/**
 * auth.global — global Nuxt route middleware.
 *
 * Runs on every client-side navigation. Resolves auth state once per
 * cold navigation (a single GET /api/owner/me) and enforces two rules:
 *
 *  1. Unauthenticated requests to any route OTHER than /login → /login.
 *  2. Authenticated requests to /login → / (avoids a stuck login screen).
 *
 * Loop-avoidance: the unauthenticated guard only fires when `to.path !== '/login'`.
 * An unauthed user navigating to /login is allowed through unconditionally
 * (the early `return` exits before the redirect below).
 */
export default defineNuxtRouteMiddleware(async (to) => {
  const { isAuthenticated, checkSession } = useAuth()

  // Resolve real auth state once per cold navigation if it is not yet known.
  // After the first successful login the singleton ref stays true, so this is
  // effectively a no-op for all subsequent navigations within the session.
  if (!isAuthenticated.value) await checkSession()

  if (to.path === '/login') {
    // Already authenticated — bounce away from the login page.
    if (isAuthenticated.value) return navigateTo('/')
    // Not authenticated and heading to /login — allow.
    return
  }

  // Any other route: redirect unauthenticated visitors to login.
  if (!isAuthenticated.value) return navigateTo('/login')
})
