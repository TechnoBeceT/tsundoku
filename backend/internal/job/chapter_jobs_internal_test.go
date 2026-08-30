package job

import (
	"context"
	"testing"
)

func TestCanceledTriggeredDownloadLoopRequeuesOpportunity(t *testing.T) {
	r := NewRunner(nil, nil, nil, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r.runDownloadCycleLogging(ctx, true)

	select {
	case <-r.trigger:
	default:
		t.Fatal("canceled loop consumed the shared download trigger instead of returning it for a live loop")
	}
}
