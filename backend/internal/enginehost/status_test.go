package enginehost_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/enginehost"
)

const validStatusJSON = `{"ready":true,"source_workers":8,"per_source_limit":2,"queued":4,"running":8,"completion_sequence":41,"oldest_running_millis":181001,"completed":20,"cancelled":1,"timed_out":8,"rejected":0,"busiest_sources":[{"source_id":11,"queued":1,"running":2},{"source_id":22,"queued":1,"running":2},{"source_id":33,"queued":1,"running":2},{"source_id":44,"queued":1,"running":2}],"extension_running":false,"extension_queued":0}`

func TestHTTPStatusProber_ReturnsTypedApprovedStatus(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validStatusJSON))
	}))
	defer srv.Close()

	status, err := enginehost.HTTPStatusProber(time.Second)(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if gotPath != "/status" {
		t.Errorf("path = %q, want /status", gotPath)
	}
	if !status.Ready || status.SourceWorkers != 8 || status.Running != 8 || status.CompletionSequence != 41 {
		t.Errorf("status = %+v, want decoded approved fields", status)
	}
	if len(status.BusiestSources) != 4 || status.BusiestSources[0].SourceID != 11 {
		t.Errorf("busiest_sources = %+v, want four typed source rows", status.BusiestSources)
	}
}

func TestHTTPStatusProber_FailsClosedOnUnapprovedMalformedAndOversizedBodies(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "unapproved secret field", body: strings.TrimSuffix(validStatusJSON, "}") + `,"token":"must-not-survive"}`},
		{name: "missing required field", body: strings.Replace(validStatusJSON, `,"extension_queued":0`, "", 1)},
		{name: "duplicate field", body: strings.Replace(validStatusJSON, `"ready":true`, `"ready":true,"ready":true`, 1)},
		{name: "malformed", body: `{"ready":true`},
		{name: "oversized", body: strings.Repeat("x", 32*1024+1)},
		{name: "second value", body: validStatusJSON + `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			if _, err := enginehost.HTTPStatusProber(time.Second)(context.Background(), srv.URL); err == nil {
				t.Fatal("probe succeeded, want fail-closed error")
			}
		})
	}
}

func TestHTTPStatusProber_FailsClosedOnHTTPFailureAndInvalidPhysicalCounts(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "http failure", status: http.StatusServiceUnavailable, body: validStatusJSON},
		{name: "running source sum mismatch", status: http.StatusOK, body: strings.Replace(validStatusJSON, `"running":8`, `"running":7`, 1)},
		{name: "too many source rows", status: http.StatusOK, body: fmt.Sprintf(`{"ready":true,"source_workers":8,"per_source_limit":2,"queued":0,"running":0,"completion_sequence":0,"oldest_running_millis":0,"completed":0,"cancelled":0,"timed_out":0,"rejected":0,"busiest_sources":[%s],"extension_running":false,"extension_queued":0}`, strings.TrimSuffix(strings.Repeat(`{"source_id":1,"queued":0,"running":0},`, 11), ","))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			if _, err := enginehost.HTTPStatusProber(time.Second)(context.Background(), srv.URL); err == nil {
				t.Fatal("probe succeeded, want fail-closed error")
			}
		})
	}
}

func TestHTTPStatusProber_ContextCancellationStopsRead(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := enginehost.HTTPStatusProber(time.Minute)(ctx, srv.URL)
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("probe returned nil after context cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("probe did not return promptly after context cancellation")
	}
}

func TestExhaustionFingerprint_MatchesTask7PhysicalIdentity(t *testing.T) {
	first := enginehost.EngineStatus{
		SourceWorkers:      8,
		PerSourceLimit:     2,
		Queued:             4,
		Running:            8,
		CompletionSequence: 41,
		BusiestSources: []enginehost.EngineSourceStatus{
			{SourceID: 44, Queued: 1, Running: 2},
			{SourceID: 11, Queued: 1, Running: 2},
			{SourceID: 33, Queued: 1, Running: 2},
			{SourceID: 22, Queued: 1, Running: 2},
		},
	}
	second := first
	second.CompletionSequence = 42
	second.Queued = 99
	second.BusiestSources = []enginehost.EngineSourceStatus{
		{SourceID: 22, Queued: 8, Running: 2},
		{SourceID: 33, Queued: 9, Running: 2},
		{SourceID: 11, Queued: 7, Running: 2},
		{SourceID: 44, Queued: 10, Running: 2},
	}

	firstFingerprint, firstOK := enginehost.ExhaustionFingerprint(first)
	secondFingerprint, secondOK := enginehost.ExhaustionFingerprint(second)
	const want = "8|8|11:2,22:2,33:2,44:2"
	if !firstOK || !secondOK {
		t.Fatalf("fingerprint validity = %v/%v, want true/true", firstOK, secondOK)
	}
	if firstFingerprint != want || secondFingerprint != want {
		t.Fatalf("fingerprints = %q / %q, want Task 7 physical identity %q", firstFingerprint, secondFingerprint, want)
	}
}
