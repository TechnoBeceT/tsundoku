// Package account_test exercises ResolveToken/MarkExpired against an
// ephemeral PostgreSQL instance (testdb) with a fake tracker.Tracker double
// whose Refresh behavior is configurable per test — no network calls.
package account_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	enttrackerconnection "github.com/technobecet/tsundoku/internal/ent/trackerconnection"
	"github.com/technobecet/tsundoku/internal/tracker"
	"github.com/technobecet/tsundoku/internal/tracker/account"
)

// fakeTracker is a minimal tracker.Tracker double whose Refresh is
// programmable per test via refreshFn; every other method is an unused stub
// (ResolveToken never calls them).
type fakeTracker struct {
	id        int
	refreshFn func(ctx context.Context, refresh string) (tracker.TokenSet, error)

	refreshCalls int
}

func (f *fakeTracker) Key() string           { return "fake" }
func (f *fakeTracker) ID() int               { return f.id }
func (f *fakeTracker) Name() string          { return "Fake Tracker" }
func (f *fakeTracker) NeedsOAuth() bool      { return false }
func (f *fakeTracker) SupportsPrivate() bool { return false }

func (f *fakeTracker) AuthURL(string, string) (string, string, error) {
	return "", "", tracker.ErrOAuthNotSupported
}

func (f *fakeTracker) ExchangeCode(context.Context, string, string, string) (tracker.TokenSet, error) {
	return tracker.TokenSet{}, tracker.ErrOAuthNotSupported
}

func (f *fakeTracker) Refresh(ctx context.Context, refresh string) (tracker.TokenSet, error) {
	f.refreshCalls++
	if f.refreshFn != nil {
		return f.refreshFn(ctx, refresh)
	}
	return tracker.TokenSet{}, tracker.ErrNoRefresh
}

func (f *fakeTracker) Search(context.Context, string, string) ([]tracker.TrackSearchResult, error) {
	return nil, nil
}
func (f *fakeTracker) GetEntry(context.Context, string, string) (*tracker.TrackEntry, error) {
	return nil, nil
}
func (f *fakeTracker) SaveEntry(_ context.Context, _ string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
	return entry, nil
}
func (f *fakeTracker) UpdateEntry(_ context.Context, _ string, entry tracker.TrackEntry) (tracker.TrackEntry, error) {
	return entry, nil
}
func (f *fakeTracker) DeleteEntry(context.Context, string, tracker.TrackEntry) error { return nil }

var _ tracker.Tracker = (*fakeTracker)(nil)

const fakeTrackerID = 950

// seedConnection creates a TrackerConnection row with the given token shape.
func seedConnection(ctx context.Context, t *testing.T, client *ent.Client, trackerID int, access, refresh string, expiresAt *time.Time) *ent.TrackerConnection {
	t.Helper()
	create := client.TrackerConnection.Create().
		SetTrackerID(trackerID).
		SetAccessToken(access).
		SetRefreshToken(refresh)
	if expiresAt != nil {
		create = create.SetExpiresAt(*expiresAt)
	}
	conn, err := create.Save(ctx)
	if err != nil {
		t.Fatalf("seed tracker connection: %v", err)
	}
	return conn
}

func reload(ctx context.Context, t *testing.T, client *ent.Client, trackerID int) *ent.TrackerConnection {
	t.Helper()
	conn, err := client.TrackerConnection.Query().Where(enttrackerconnection.TrackerID(trackerID)).Only(ctx)
	if err != nil {
		t.Fatalf("reload tracker connection: %v", err)
	}
	return conn
}

// TestResolveToken_NotExpired_ReturnedUnchanged confirms the common path: a
// token whose expires_at is still in the future is returned VERBATIM, with
// Refresh never called.
func TestResolveToken_NotExpired_ReturnedUnchanged(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	future := time.Now().Add(time.Hour)
	seedConnection(ctx, t, client, fakeTrackerID, "access-1", "refresh-1", &future)
	ft := &fakeTracker{id: fakeTrackerID}
	registry := tracker.NewRegistry(ft)

	got, err := account.ResolveToken(ctx, client, registry, fakeTrackerID)
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if got != "access-1" {
		t.Fatalf("ResolveToken = %q, want the stored access token unchanged", got)
	}
	if ft.refreshCalls != 0 {
		t.Fatalf("Refresh calls = %d, want 0 (token not expired)", ft.refreshCalls)
	}
}

// TestResolveToken_Expired_RefreshSucceeds confirms an expired token with a
// working refresh grant is refreshed, persisted (access/refresh/expires_at,
// token_expired=false), and the FRESH access token is returned.
func TestResolveToken_Expired_RefreshSucceeds(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	past := time.Now().Add(-time.Hour)
	seedConnection(ctx, t, client, fakeTrackerID, "stale-access", "stale-refresh", &past)
	// Pre-mark expired so the row also proves ResolveToken clears the flag
	// on a successful refresh.
	if _, err := client.TrackerConnection.Update().
		Where(enttrackerconnection.TrackerID(fakeTrackerID)).
		SetTokenExpired(true).
		Save(ctx); err != nil {
		t.Fatalf("pre-mark token_expired: %v", err)
	}

	newExpiry := time.Now().Add(2 * time.Hour)
	ft := &fakeTracker{
		id: fakeTrackerID,
		refreshFn: func(_ context.Context, refresh string) (tracker.TokenSet, error) {
			if refresh != "stale-refresh" {
				t.Fatalf("Refresh called with %q, want stale-refresh", refresh)
			}
			return tracker.TokenSet{Access: "fresh-access", Refresh: "fresh-refresh", ExpiresAt: &newExpiry}, nil
		},
	}
	registry := tracker.NewRegistry(ft)

	got, err := account.ResolveToken(ctx, client, registry, fakeTrackerID)
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if got != "fresh-access" {
		t.Fatalf("ResolveToken = %q, want fresh-access", got)
	}
	if ft.refreshCalls != 1 {
		t.Fatalf("Refresh calls = %d, want 1", ft.refreshCalls)
	}

	assertRefreshedConnectionPersisted(t, reload(ctx, t, client, fakeTrackerID), newExpiry)
}

// assertRefreshedConnectionPersisted fails t unless conn carries the fresh
// token/expiry a successful refresh should have written and token_expired
// was cleared — extracted so its caller stays under the fleet's
// per-function cyclomatic-complexity budget.
func assertRefreshedConnectionPersisted(t *testing.T, conn *ent.TrackerConnection, wantExpiry time.Time) {
	t.Helper()
	if conn.AccessToken != "fresh-access" || conn.RefreshToken != "fresh-refresh" {
		t.Fatalf("persisted tokens = access=%q refresh=%q, want fresh-access/fresh-refresh",
			conn.AccessToken, conn.RefreshToken)
	}
	// Postgres' timestamp column truncates to microsecond precision, so
	// compare with a generous tolerance rather than exact equality.
	if conn.ExpiresAt == nil || conn.ExpiresAt.Sub(wantExpiry).Abs() > time.Millisecond {
		t.Fatalf("persisted expires_at = %v, want ~%v", conn.ExpiresAt, wantExpiry)
	}
	if conn.TokenExpired {
		t.Fatalf("persisted token_expired = true, want false after a successful refresh")
	}
}

// TestResolveToken_Expired_RefreshFails confirms a failing refresh call
// returns tracker.ErrTokenExpired and flags the row token_expired=true.
func TestResolveToken_Expired_RefreshFails(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	past := time.Now().Add(-time.Hour)
	seedConnection(ctx, t, client, fakeTrackerID, "stale-access", "stale-refresh", &past)
	ft := &fakeTracker{
		id: fakeTrackerID,
		refreshFn: func(context.Context, string) (tracker.TokenSet, error) {
			return tracker.TokenSet{}, errors.New("upstream rejected the refresh")
		},
	}
	registry := tracker.NewRegistry(ft)

	_, err := account.ResolveToken(ctx, client, registry, fakeTrackerID)
	if !errors.Is(err, tracker.ErrTokenExpired) {
		t.Fatalf("ResolveToken err = %v, want tracker.ErrTokenExpired", err)
	}
	if ft.refreshCalls != 1 {
		t.Fatalf("Refresh calls = %d, want 1", ft.refreshCalls)
	}

	conn := reload(ctx, t, client, fakeTrackerID)
	if !conn.TokenExpired {
		t.Fatalf("persisted token_expired = false, want true after a failed refresh")
	}
	// The stale access/refresh tokens are left untouched — no partial write
	// on a failed refresh.
	if conn.AccessToken != "stale-access" || conn.RefreshToken != "stale-refresh" {
		t.Fatalf("stored tokens changed on a failed refresh: access=%q refresh=%q", conn.AccessToken, conn.RefreshToken)
	}
}

// TestResolveToken_Expired_NoRefreshToken confirms an expired token with NO
// stored refresh token returns tracker.ErrTokenExpired WITHOUT ever calling
// Refresh, and flags the row.
func TestResolveToken_Expired_NoRefreshToken(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	past := time.Now().Add(-time.Hour)
	seedConnection(ctx, t, client, fakeTrackerID, "stale-access", "", &past)
	ft := &fakeTracker{id: fakeTrackerID}
	registry := tracker.NewRegistry(ft)

	_, err := account.ResolveToken(ctx, client, registry, fakeTrackerID)
	if !errors.Is(err, tracker.ErrTokenExpired) {
		t.Fatalf("ResolveToken err = %v, want tracker.ErrTokenExpired", err)
	}
	if ft.refreshCalls != 0 {
		t.Fatalf("Refresh calls = %d, want 0 (no refresh token stored)", ft.refreshCalls)
	}

	conn := reload(ctx, t, client, fakeTrackerID)
	if !conn.TokenExpired {
		t.Fatalf("persisted token_expired = false, want true")
	}
}

// TestResolveToken_AniListShape_NeverForceExpired confirms an AniList-shape
// token (no refresh token, nil expires_at) is treated as NOT expired and
// returned unchanged — its only real expiry signal is a reactive 401 this
// proactive check cannot see (see the package doc comment's follow-up note).
func TestResolveToken_AniListShape_NeverForceExpired(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedConnection(ctx, t, client, fakeTrackerID, "anilist-access", "", nil)
	ft := &fakeTracker{id: fakeTrackerID}
	registry := tracker.NewRegistry(ft)

	got, err := account.ResolveToken(ctx, client, registry, fakeTrackerID)
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if got != "anilist-access" {
		t.Fatalf("ResolveToken = %q, want anilist-access unchanged", got)
	}
	if ft.refreshCalls != 0 {
		t.Fatalf("Refresh calls = %d, want 0", ft.refreshCalls)
	}

	conn := reload(ctx, t, client, fakeTrackerID)
	if conn.TokenExpired {
		t.Fatalf("persisted token_expired = true, want false (AniList shape must never be force-expired)")
	}
}

// TestResolveToken_NotConnected confirms ResolveToken fails closed with
// account.ErrTrackerNotConnected when no TrackerConnection row exists.
func TestResolveToken_NotConnected(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	ft := &fakeTracker{id: fakeTrackerID}
	registry := tracker.NewRegistry(ft)

	if _, err := account.ResolveToken(ctx, client, registry, fakeTrackerID); !errors.Is(err, account.ErrTrackerNotConnected) {
		t.Fatalf("ResolveToken err = %v, want account.ErrTrackerNotConnected", err)
	}
}

// TestMarkExpired_SetsFlag confirms MarkExpired flips token_expired=true on
// the matching row.
func TestMarkExpired_SetsFlag(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedConnection(ctx, t, client, fakeTrackerID, "access", "refresh", nil)

	account.MarkExpired(ctx, client, fakeTrackerID)

	conn := reload(ctx, t, client, fakeTrackerID)
	if !conn.TokenExpired {
		t.Fatalf("token_expired = false after MarkExpired, want true")
	}
}

// TestMarkExpired_NoRowIsANoOp confirms MarkExpired never panics/errors
// loudly when there is nothing to flag (best-effort — see its own doc
// comment).
func TestMarkExpired_NoRowIsANoOp(t *testing.T) {
	client := testdb.New(t)
	account.MarkExpired(context.Background(), client, 999999)
}
