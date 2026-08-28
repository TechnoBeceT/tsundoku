package schema_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
)

func TestSourceThroughputPolicyNullableValuesRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	concurrency := client.SourceThroughputPolicy.Create().
		SetSourceID(101).
		SetDownloadConcurrency(2).
		SaveX(ctx)
	delay := client.SourceThroughputPolicy.Create().
		SetSourceID(202).
		SetImageRequestDelayMs(0).
		SaveX(ctx)

	gotConcurrency := client.SourceThroughputPolicy.GetX(ctx, concurrency.ID)
	if gotConcurrency.DownloadConcurrency == nil || *gotConcurrency.DownloadConcurrency != 2 {
		t.Fatalf("download_concurrency = %v, want pointer to 2", gotConcurrency.DownloadConcurrency)
	}
	if gotConcurrency.ImageRequestDelayMs != nil {
		t.Fatalf("image_request_delay_ms = %v, want nil", gotConcurrency.ImageRequestDelayMs)
	}
	if gotConcurrency.CreatedAt.IsZero() || gotConcurrency.UpdatedAt.IsZero() {
		t.Fatalf("timestamps were not defaulted: created=%v updated=%v", gotConcurrency.CreatedAt, gotConcurrency.UpdatedAt)
	}

	gotDelay := client.SourceThroughputPolicy.GetX(ctx, delay.ID)
	if gotDelay.DownloadConcurrency != nil {
		t.Fatalf("download_concurrency = %v, want nil", gotDelay.DownloadConcurrency)
	}
	if gotDelay.ImageRequestDelayMs == nil || *gotDelay.ImageRequestDelayMs != 0 {
		t.Fatalf("image_request_delay_ms = %v, want pointer to explicit zero", gotDelay.ImageRequestDelayMs)
	}
}

func TestSourceThroughputPolicySourceIDIsUnique(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	client.SourceThroughputPolicy.Create().
		SetSourceID(101).
		SetDownloadConcurrency(1).
		SaveX(ctx)

	_, err := client.SourceThroughputPolicy.Create().
		SetSourceID(101).
		SetImageRequestDelayMs(500).
		Save(ctx)
	if err == nil || !ent.IsConstraintError(err) {
		t.Fatalf("duplicate source_id error = %v, want constraint error", err)
	}
}
