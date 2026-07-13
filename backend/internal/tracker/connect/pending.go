package connect

import (
	"sync"
	"time"
)

// pendingLogin is what AuthURL stashes for one in-flight OAuth attempt,
// keyed by its random state (see pendingStash) so CompleteOAuth can look up
// which tracker + PKCE verifier a callback's state belongs to.
type pendingLogin struct {
	TrackerID    int
	PKCEVerifier string
	ExpiresAt    time.Time
}

// pendingStashTTL bounds how long a stashed login is honored — the owner is
// expected to complete an OAuth redirect within minutes; a much older entry
// is almost certainly abandoned (a closed tab, a bookmarked stale link) and
// is swept rather than accepted indefinitely.
const pendingStashTTL = 10 * time.Minute

// pendingStash is a mutex-guarded, per-Service store of in-flight OAuth
// logins keyed by their random state. It is ALWAYS an instance field of
// Service (see Service.stash in service.go), never a package-level
// variable — a process-global stash would let unrelated Service instances
// (e.g. two independent test cases, or a future multi-instance deployment)
// collide on the same state space. Keeping it per-instance is what makes
// "two concurrent logins don't collide" true by construction: each state is
// a fresh random value (see randomState) stored in ONE Service's own map,
// so two logins in flight at once — on the same or different Service
// instances — never share a key.
type pendingStash struct {
	mu    sync.Mutex
	byKey map[string]pendingLogin
}

// newPendingStash builds an empty stash.
func newPendingStash() *pendingStash {
	return &pendingStash{byKey: make(map[string]pendingLogin)}
}

// Put stashes login under state, first sweeping any already-expired entries
// so the map cannot grow unbounded across many abandoned login attempts.
func (s *pendingStash) Put(state string, login pendingLogin) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	login.ExpiresAt = time.Now().Add(pendingStashTTL)
	s.byKey[state] = login
}

// Take removes and returns the pending login for state, when present and
// not expired. A login is consumable exactly ONCE — a replayed callback
// carrying the same state fails on the second attempt, closing the
// CSRF-replay window a reusable stash entry would leave open.
func (s *pendingStash) Take(state string) (pendingLogin, bool) {
	if state == "" {
		return pendingLogin{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	login, ok := s.byKey[state]
	delete(s.byKey, state)
	if !ok || time.Now().After(login.ExpiresAt) {
		return pendingLogin{}, false
	}
	return login, true
}

// sweepLocked deletes every expired entry. Caller must hold s.mu.
func (s *pendingStash) sweepLocked() {
	now := time.Now()
	for k, v := range s.byKey {
		if now.After(v.ExpiresAt) {
			delete(s.byKey, k)
		}
	}
}
