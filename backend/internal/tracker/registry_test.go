package tracker_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/tracker"
)

// fakeTracker is the minimal tracker.Tracker double used by this package's
// own tests (registry + roundtripper) — just enough to exercise the port's
// generic logic without a real network.
type fakeTracker struct {
	key        string
	id         int
	name       string
	needsOAuth bool

	refreshFn func(ctx context.Context, refresh string) (tracker.TokenSet, error)
}

func (f *fakeTracker) Key() string      { return f.key }
func (f *fakeTracker) ID() int          { return f.id }
func (f *fakeTracker) Name() string     { return f.name }
func (f *fakeTracker) NeedsOAuth() bool { return f.needsOAuth }
func (f *fakeTracker) AuthURL(state, redirectURI string) (string, string, error) {
	return "https://example.test/authorize?state=" + state, "", nil
}
func (f *fakeTracker) ExchangeCode(_ context.Context, _, _, _ string) (tracker.TokenSet, error) {
	return tracker.TokenSet{}, nil
}
func (f *fakeTracker) Refresh(ctx context.Context, refresh string) (tracker.TokenSet, error) {
	if f.refreshFn != nil {
		return f.refreshFn(ctx, refresh)
	}
	return tracker.TokenSet{}, tracker.ErrNoRefresh
}
func (f *fakeTracker) Search(_ context.Context, _, _ string) ([]tracker.TrackSearchResult, error) {
	return nil, nil
}
func (f *fakeTracker) GetEntry(_ context.Context, _, _ string) (*tracker.TrackEntry, error) {
	return nil, nil
}
func (f *fakeTracker) SaveEntry(_ context.Context, _ string, e tracker.TrackEntry) (tracker.TrackEntry, error) {
	return e, nil
}
func (f *fakeTracker) UpdateEntry(_ context.Context, _ string, e tracker.TrackEntry) (tracker.TrackEntry, error) {
	return e, nil
}
func (f *fakeTracker) DeleteEntry(_ context.Context, _ string, _ tracker.TrackEntry) error {
	return nil
}

var _ tracker.Tracker = (*fakeTracker)(nil)

// TestRegistry_ByIDAndOrder confirms Trackers() preserves registration order
// and ByID resolves each by its numeric registry id.
func TestRegistry_ByIDAndOrder(t *testing.T) {
	mal := &fakeTracker{key: "mal", id: tracker.IDMAL, name: "MyAnimeList"}
	anilist := &fakeTracker{key: "anilist", id: tracker.IDAniList, name: "AniList"}

	reg := tracker.NewRegistry(mal, anilist)

	ts := reg.Trackers()
	if len(ts) != 2 || ts[0].Key() != "mal" || ts[1].Key() != "anilist" {
		t.Fatalf("Trackers() = %+v, want [mal anilist] in order", ts)
	}

	got, ok := reg.ByID(tracker.IDAniList)
	if !ok || got.Key() != "anilist" {
		t.Fatalf("ByID(IDAniList) = (%v, %v), want anilist tracker", got, ok)
	}

	if _, ok := reg.ByID(999); ok {
		t.Fatalf("ByID(999) reported found for an unregistered id")
	}
}

// TestRegistry_Empty confirms a Registry built with no trackers behaves —
// Trackers() returns an empty (non-nil-crashing) slice and ByID always
// misses.
func TestRegistry_Empty(t *testing.T) {
	reg := tracker.NewRegistry()
	if len(reg.Trackers()) != 0 {
		t.Fatalf("Trackers() on an empty registry = %+v, want empty", reg.Trackers())
	}
	if _, ok := reg.ByID(tracker.IDMAL); ok {
		t.Fatalf("ByID on an empty registry unexpectedly found an id")
	}
}
