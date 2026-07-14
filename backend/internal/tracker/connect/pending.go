package connect

import (
	"sync"
	"time"
)

// pendingLogin is what AuthURL stashes for one in-flight OAuth attempt,
// keyed by TRACKER ID (see pendingStash) so CompleteOAuth can look up which
// PKCE verifier a callback for that tracker belongs to. There is at most ONE
// pending login per tracker at a time — a fresh AuthURL call for a tracker
// that already has one in flight simply replaces it. This mirrors the
// reference implementations (Suwayomi-Server, Komikku), which likewise hold
// a single per-tracker verifier rather than a per-attempt CSRF token — see
// the package doc comment for why AniList/MAL's real authorize endpoints
// reject a state param outright.
type pendingLogin struct {
	PKCEVerifier string
	ExpiresAt    time.Time
}

// pendingStashTTL bounds how long a stashed login is honored — the owner is
// expected to complete an OAuth redirect within minutes; a much older entry
// is almost certainly abandoned (a closed tab, a bookmarked stale link) and
// is swept rather than accepted indefinitely.
const pendingStashTTL = 10 * time.Minute

// pendingStash is a mutex-guarded, per-Service store of in-flight OAuth
// logins keyed by TRACKER ID (never a per-login random state — see the
// package doc comment). It is ALWAYS an instance field of Service (see
// Service.stash in service.go), never a package-level variable — a
// process-global stash would let unrelated Service instances (e.g. two
// independent test cases, or a future multi-instance deployment) collide on
// the same key space. Keeping it per-instance is what makes "two concurrent
// logins for different trackers don't collide" true by construction: each
// tracker id is its own map key in ONE Service's own map.
type pendingStash struct {
	mu    sync.Mutex
	byKey map[int]pendingLogin
}

// newPendingStash builds an empty stash.
func newPendingStash() *pendingStash {
	return &pendingStash{byKey: make(map[int]pendingLogin)}
}

// Put stashes login under trackerID, first sweeping any already-expired
// entries so the map cannot grow unbounded across many abandoned login
// attempts. A login already pending for trackerID is silently replaced —
// starting a fresh AuthURL supersedes an earlier one nobody completed.
func (s *pendingStash) Put(trackerID int, login pendingLogin) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	login.ExpiresAt = time.Now().Add(pendingStashTTL)
	s.byKey[trackerID] = login
}

// Take removes and returns the pending login for trackerID, when present and
// not expired. A login is consumable exactly ONCE — a replayed callback for
// the same tracker fails on the second attempt, closing the replay window a
// reusable stash entry would leave open.
func (s *pendingStash) Take(trackerID int) (pendingLogin, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	login, ok := s.byKey[trackerID]
	delete(s.byKey, trackerID)
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
