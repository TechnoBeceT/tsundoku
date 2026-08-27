package sourceengine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

func TestStatus_DecodesBoundedRuntimeSnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/status" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"ready":                 true,
			"source_workers":        8,
			"per_source_limit":      2,
			"queued":                12,
			"running":               8,
			"completion_sequence":   41,
			"oldest_running_millis": 181_250,
			"completed":             30,
			"cancelled":             3,
			"timed_out":             5,
			"rejected":              7,
			"busiest_sources":       []map[string]any{{"source_id": int64(99), "queued": 4, "running": 2}},
			"extension_running":     true,
			"extension_queued":      1,
		})
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	want := sourceengine.EngineStatus{
		Ready:               true,
		SourceWorkers:       8,
		PerSourceLimit:      2,
		Queued:              12,
		Running:             8,
		CompletionSequence:  41,
		OldestRunningMillis: 181_250,
		Completed:           30,
		Cancelled:           3,
		TimedOut:            5,
		Rejected:            7,
		BusiestSources: []sourceengine.EngineSourceStatus{
			{SourceID: 99, Queued: 4, Running: 2},
		},
		ExtensionRunning: true,
		ExtensionQueued:  1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Status = %+v, want %+v", got, want)
	}
}
