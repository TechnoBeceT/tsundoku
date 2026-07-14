package connect

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestPendingStash_PutTakeRoundTrips confirms a stashed login is retrievable
// exactly once with its PKCE verifier intact.
func TestPendingStash_PutTakeRoundTrips(t *testing.T) {
	s := newPendingStash()
	s.Put(1, pendingLogin{PKCEVerifier: "verifier-1"})

	got, ok := s.Take(1)
	if !ok {
		t.Fatal("Take(1) = false, want true")
	}
	if got.PKCEVerifier != "verifier-1" {
		t.Fatalf("Take(1) = %+v, want PKCEVerifier=verifier-1", got)
	}
}

// TestPendingStash_TakeIsSingleUse confirms a tracker id can only be
// consumed ONCE — a replayed callback for the same tracker must fail,
// closing the replay window a reusable entry would leave open.
func TestPendingStash_TakeIsSingleUse(t *testing.T) {
	s := newPendingStash()
	s.Put(1, pendingLogin{})

	if _, ok := s.Take(1); !ok {
		t.Fatal("first Take(1) = false, want true")
	}
	if _, ok := s.Take(1); ok {
		t.Fatal("second Take(1) = true, want false (single-use)")
	}
}

// TestPendingStash_TakeUnknownTracker confirms an unrecognized tracker id
// (never stashed, or expired+swept) is reported as not-found, never a
// zero-value pendingLogin masquerading as valid.
func TestPendingStash_TakeUnknownTracker(t *testing.T) {
	s := newPendingStash()
	if _, ok := s.Take(9999); ok {
		t.Fatal("Take(9999) = true, want false")
	}
}

// TestPendingStash_ExpiredEntryIsRejected confirms Take refuses an entry
// whose TTL has already elapsed, even though it is still physically present
// in the map (Take deletes-then-checks, so this also proves an expired
// entry can never be replayed after its TTL).
func TestPendingStash_ExpiredEntryIsRejected(t *testing.T) {
	s := newPendingStash()
	// Bypass Put's TTL stamping to inject an already-expired entry directly.
	s.byKey[1] = pendingLogin{ExpiresAt: time.Now().Add(-time.Minute)}

	if _, ok := s.Take(1); ok {
		t.Fatal("Take(1) on an expired entry = true, want false")
	}
}

// TestPendingStash_PutReplacesEarlierPending confirms a fresh AuthURL for a
// tracker that already has a pending login supersedes it — only the LATEST
// verifier is honored, mirroring the reference implementations' single
// per-tracker verifier.
func TestPendingStash_PutReplacesEarlierPending(t *testing.T) {
	s := newPendingStash()
	s.Put(1, pendingLogin{PKCEVerifier: "first"})
	s.Put(1, pendingLogin{PKCEVerifier: "second"})

	got, ok := s.Take(1)
	if !ok {
		t.Fatal("Take(1) = false, want true")
	}
	if got.PKCEVerifier != "second" {
		t.Fatalf("Take(1).PKCEVerifier = %q, want second (the latest AuthURL wins)", got.PKCEVerifier)
	}
}

// TestPendingStash_NotAGlobal is the mission-required proof: two
// INDEPENDENT stash instances (mirroring two Service instances / two
// concurrent logins never sharing state) never see each other's entries —
// pendingStash is always an instance field, never package-level state.
func TestPendingStash_NotAGlobal(t *testing.T) {
	a := newPendingStash()
	b := newPendingStash()

	a.Put(1, pendingLogin{PKCEVerifier: "a-verifier"})
	if _, ok := b.Take(1); ok {
		t.Fatal("stash b saw stash a's entry — the stash is leaking through shared/global state")
	}
	// a's own entry is still there, untouched by b's failed Take.
	if _, ok := a.Take(1); !ok {
		t.Fatal("stash a lost its own entry after an unrelated Take on stash b")
	}
}

// TestPendingStash_ConcurrentLoginsDontCollide drives many concurrent
// Put/Take pairs against ONE stash with distinct tracker ids and asserts
// every login gets back exactly its own PKCEVerifier — no cross-talk
// between concurrent logins for different trackers sharing the one stash a
// real Service instance uses.
func TestPendingStash_ConcurrentLoginsDontCollide(t *testing.T) {
	s := newPendingStash()
	const n = 50

	var wg sync.WaitGroup
	errs := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			verifier := verifierFor(i)
			s.Put(i, pendingLogin{PKCEVerifier: verifier})
			got, ok := s.Take(i)
			if !ok {
				errs <- "Take reported not-found for its own just-stashed tracker id"
				return
			}
			if got.PKCEVerifier != verifier {
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

func verifierFor(i int) string { return "verifier-" + strconv.Itoa(i) }
