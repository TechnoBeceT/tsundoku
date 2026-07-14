package notify

import (
	"context"
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	entsettings "github.com/technobecet/tsundoku/internal/ent/settings"
)

// SetWatermarkForTest writes the internal notify watermark directly (test-only),
// so a test can pin the "last notified" boundary deterministically.
func SetWatermarkForTest(ctx context.Context, client *ent.Client, t time.Time) error {
	s := &Service{client: client}
	return s.writeWatermark(ctx, t)
}

// GetWatermarkForTest reads the raw persisted watermark (test-only). present is
// false when no watermark row exists yet (never seeds one, unlike readWatermark).
func GetWatermarkForTest(ctx context.Context, client *ent.Client) (t time.Time, present bool, err error) {
	row, err := client.Settings.Query().Where(entsettings.KeyEQ(watermarkKey)).Only(ctx)
	if ent.IsNotFound(err) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, row.Value)
	if err != nil {
		return time.Time{}, false, err
	}
	return parsed, true, nil
}
