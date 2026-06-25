package series_test

import (
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/series"
)

func f64(v float64) *float64    { return &v }
func tp(t time.Time) *time.Time { return &t }

// keys builds the SeriesChapterKeys set from a list of keys.
func keys(ks ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(ks))
	for _, k := range ks {
		m[k] = struct{}{}
	}
	return m
}

func TestComputeProviderHealth(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	recent := now.AddDate(0, 0, -2) // within a 14d grace
	old := now.AddDate(0, 0, -40)   // past a 14d grace

	cases := []struct {
		name       string
		in         series.ProviderHealthInput
		wantStatus string
		wantBehind int
	}{
		{
			name: "ok: carries leading edge, recent, no error",
			in: series.ProviderHealthInput{
				ProviderChapters: []*ent.ProviderChapter{
					{ChapterKey: "k1", Number: f64(1), ProviderUploadDate: tp(recent)},
					{ChapterKey: "k2", Number: f64(2), ProviderUploadDate: tp(recent)},
				},
				SeriesChapterKeys: keys("k1", "k2"),
				SeriesMaxNumber:   f64(2),
				MultiSource:       true,
			},
			wantStatus: series.HealthOK,
			wantBehind: 0,
		},
		{
			name: "stale: behind leading edge AND past grace, multi-source",
			in: series.ProviderHealthInput{
				ProviderChapters: []*ent.ProviderChapter{
					{ChapterKey: "k1", Number: f64(1), ProviderUploadDate: tp(old)},
				},
				SeriesChapterKeys: keys("k1", "k2", "k3"),
				SeriesMaxNumber:   f64(3),
				MultiSource:       true,
			},
			wantStatus: series.HealthStale,
			wantBehind: 2,
		},
		{
			name: "not stale: behind but newest upload within grace",
			in: series.ProviderHealthInput{
				ProviderChapters: []*ent.ProviderChapter{
					{ChapterKey: "k1", Number: f64(1), ProviderUploadDate: tp(recent)},
				},
				SeriesChapterKeys: keys("k1", "k2"),
				SeriesMaxNumber:   f64(2),
				MultiSource:       true,
			},
			wantStatus: series.HealthOK,
			wantBehind: 1,
		},
		{
			name: "not stale: single-source series never stale",
			in: series.ProviderHealthInput{
				ProviderChapters: []*ent.ProviderChapter{
					{ChapterKey: "k1", Number: f64(1), ProviderUploadDate: tp(old)},
				},
				SeriesChapterKeys: keys("k1", "k2"),
				SeriesMaxNumber:   f64(2),
				MultiSource:       false,
			},
			wantStatus: series.HealthOK,
			wantBehind: 1,
		},
		{
			name: "not stale: carries leading edge though old (late-join lacks back-catalog)",
			in: series.ProviderHealthInput{
				ProviderChapters: []*ent.ProviderChapter{
					{ChapterKey: "k50", Number: f64(50), ProviderUploadDate: tp(old)},
				},
				SeriesChapterKeys: keys("k1", "k2", "k50"),
				SeriesMaxNumber:   f64(50),
				MultiSource:       true,
			},
			wantStatus: series.HealthOK,
			wantBehind: 2,
		},
		{
			name: "erroring beats stale",
			in: series.ProviderHealthInput{
				SyncState:         &ent.SuwayomiSyncState{LastError: "boom"},
				ProviderChapters:  []*ent.ProviderChapter{{ChapterKey: "k1", Number: f64(1), ProviderUploadDate: tp(old)}},
				SeriesChapterKeys: keys("k1", "k2"),
				SeriesMaxNumber:   f64(2),
				MultiSource:       true,
			},
			wantStatus: series.HealthErroring,
			wantBehind: 1,
		},
		{
			name: "nil newest upload (no dates) → not stale on age clause",
			in: series.ProviderHealthInput{
				ProviderChapters:  []*ent.ProviderChapter{{ChapterKey: "k1", Number: f64(1)}},
				SeriesChapterKeys: keys("k1", "k2"),
				SeriesMaxNumber:   f64(2),
				MultiSource:       true,
			},
			wantStatus: series.HealthOK,
			wantBehind: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := series.ComputeProviderHealth(tc.in, now, 14)
			if got.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", got.Status, tc.wantStatus)
			}
			if got.ChaptersBehind != tc.wantBehind {
				t.Errorf("ChaptersBehind = %d, want %d", got.ChaptersBehind, tc.wantBehind)
			}
		})
	}
}

// TestComputeProviderHealth_CompletedForcesOK proves a completed series is never
// flagged stale/erroring: even with a recorded LastError it returns ok, while the
// informational fields stay populated. Non-vacuous: drop the Completed
// short-circuit and this fails with Status == "erroring".
func TestComputeProviderHealth_CompletedForcesOK(t *testing.T) {
	now := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
	in := series.ProviderHealthInput{
		Completed: true,
		SyncState: &ent.SuwayomiSyncState{LastError: "source offline"},
	}

	got := series.ComputeProviderHealth(in, now, 14)

	if got.Status != series.HealthOK {
		t.Fatalf("Status = %q, want %q (completed must force ok)", got.Status, series.HealthOK)
	}
	if got.LastError != "source offline" {
		t.Errorf("LastError = %q, want it still surfaced for display", got.LastError)
	}
}

func TestComputeProviderHealthCarriesSyncFields(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	synced := now.AddDate(0, 0, -1)
	got := series.ComputeProviderHealth(series.ProviderHealthInput{
		SyncState: &ent.SuwayomiSyncState{LastSyncedAt: tp(synced), LastError: ""},
	}, now, 14)
	if got.LastSyncedAt == nil || !got.LastSyncedAt.Equal(synced) {
		t.Errorf("LastSyncedAt = %v, want %v", got.LastSyncedAt, synced)
	}
	if got.LastError != "" {
		t.Errorf("LastError = %q, want empty", got.LastError)
	}
}
