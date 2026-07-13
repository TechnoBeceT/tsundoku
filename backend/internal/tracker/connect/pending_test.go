package connect

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestPendingStash_PutTakeRoundTrips confirms a stashed login is retrievable
// exactly once with its tracker id and PKCE verifier intact.
func TestPendingStash_PutTakeRoundTrips(t *testing.T) {
	s := newPendingStash()
	s.Put("state-1", pendingLogin{TrackerID: 1, PKCEVerifier: "verifier-1"})

	got, ok := s.Take("state-1")
	if !ok {
		t.Fatal("Take(state-1) = false, want true")
	}
	if got.TrackerID != 1 || got.PKCEVerifier != "verifier-1" {
		t.Fatalf("Take(state-1) = %+v, want TrackerID=1 PKCEVerifier=verifier-1", got)
	}
}

// TestPendingStash_TakeIsSingleUse confirms a state can only be consumed
// ONCE — a replayed callback with the same state must fail, closing the
// CSRF-replay window a reusable entry would leave open.
func TestPendingStash_TakeIsSingleUse(t *testing.T) {
	s := newPendingStash()
	s.Put("state-1", pendingLogin{TrackerID: 1})

	if _, ok := s.Take("state-1"); !ok {
		t.Fatal("first Take(state-1) = false, want true")
	}
	if _, ok := s.Take("state-1"); ok {
		t.Fatal("second Take(state-1) = true, want false (single-use)")
	}
}

// TestPendingStash_TakeUnknownState confirms an unrecognized state (never
// stashed, or expired+swept) is reported as not-found, never a zero-value
// pendingLogin masquerading as valid.
func TestPendingStash_TakeUnknownState(t *testing.T) {
	s := newPendingStash()
	if _, ok := s.Take("never-stashed"); ok {
		t.Fatal("Take(never-stashed) = true, want false")
	}
	if _, ok := s.Take(""); ok {
		t.Fatal("Take(\"\") = true, want false")
	}
}

// TestPendingStash_ExpiredEntryIsRejected confirms Take refuses an entry
// whose TTL has already elapsed, even though it is still physically present
// in the map (Take deletes-then-checks, so this also proves an expired
// entry can never be replayed after its TTL).
func TestPendingStash_ExpiredEntryIsRejected(t *testing.T) {
	s := newPendingStash()
	// Bypass Put's TTL stamping to inject an already-expired entry directly.
	s.byKey["state-1"] = pendingLogin{TrackerID: 1, ExpiresAt: time.Now().Add(-time.Minute)}

	if _, ok := s.Take("state-1"); ok {
		t.Fatal("Take(state-1) on an expired entry = true, want false")
	}
}

// TestPendingStash_NotAGlobal is the mission-required proof: two
// INDEPENDENT stash instances (mirroring two Service instances / two
// concurrent logins never sharing state) never see each other's entries —
// pendingStash is always an instance field, never package-level state.
func TestPendingStash_NotAGlobal(t *testing.T) {
	a := newPendingStash()
	b := newPendingStash()

	a.Put("shared-looking-state", pendingLogin{TrackerID: tracker1})
	if _, ok := b.Take("shared-looking-state"); ok {
		t.Fatal("stash b saw stash a's entry — the stash is leaking through shared/global state")
	}
	// a's own entry is still there, untouched by b's failed Take.
	if _, ok := a.Take("shared-looking-state"); !ok {
		t.Fatal("stash a lost its own entry after an unrelated Take on stash b")
	}
}

// TestPendingStash_ConcurrentLoginsDontCollide drives many concurrent
// Put/Take pairs against ONE stash with distinct random-looking states
// (mirroring AuthURL's real crypto/rand state generation) and asserts every
// login gets back exactly its own PKCEVerifier — no cross-talk between
// concurrent logins sharing the one stash a real Service instance uses.
func TestPendingStash_ConcurrentLoginsDontCollide(t *testing.T) {
	s := newPendingStash()
	const n = 50

	var wg sync.WaitGroup
	errs := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			state := stateFor(i)
			verifier := verifierFor(i)
			s.Put(state, pendingLogin{TrackerID: i, PKCEVerifier: verifier})
			got, ok := s.Take(state)
			if !ok {
				errs <- "Take reported not-found for its own just-stashed state"
				return
			}
			if got.TrackerID != i || got.PKCEVerifier != verifier {
				errs <- "Take returned a DIFFERENT login's data — collision"
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

const tracker1 = 1

func stateFor(i int) string    { return "state-" + strconv.Itoa(i) }
func verifierFor(i int) string { return "verifier-" + strconv.Itoa(i) }
