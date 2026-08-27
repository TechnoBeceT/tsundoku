package enginehost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxStatusBodyBytes = 32 * 1024

// EngineSourceStatus is one bounded, payload-free source occupancy row from
// engine-host's GET /status response.
type EngineSourceStatus struct {
	SourceID int64 `json:"source_id"`
	Queued   int   `json:"queued"`
	Running  int   `json:"running"`
}

// EngineStatus is the complete approved GET /status contract. It deliberately
// contains no request data, URLs, headers, preferences, or free-form text.
type EngineStatus struct {
	Ready               bool                 `json:"ready"`
	SourceWorkers       int                  `json:"source_workers"`
	PerSourceLimit      int                  `json:"per_source_limit"`
	Queued              int                  `json:"queued"`
	Running             int                  `json:"running"`
	CompletionSequence  int64                `json:"completion_sequence"`
	OldestRunningMillis int64                `json:"oldest_running_millis"`
	Completed           int64                `json:"completed"`
	Cancelled           int64                `json:"cancelled"`
	TimedOut            int64                `json:"timed_out"`
	Rejected            int64                `json:"rejected"`
	BusiestSources      []EngineSourceStatus `json:"busiest_sources"`
	ExtensionRunning    bool                 `json:"extension_running"`
	ExtensionQueued     int                  `json:"extension_queued"`
}

// ExhaustionDiagnostic is the bounded evidence emitted immediately before a
// managed profile is restarted for sustained source-worker exhaustion.
type ExhaustionDiagnostic struct {
	ProfileKey   string
	PID          int
	FirstSample  time.Time
	LatestSample time.Time
	Fingerprint  string
	Status       EngineStatus
}

func newHTTPStatusProber(timeout time.Duration) StatusProber {
	client := &http.Client{Timeout: timeout}
	return func(ctx context.Context, baseURL string) (EngineStatus, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/status", nil)
		if err != nil {
			return EngineStatus{}, fmt.Errorf("enginehost: build status probe: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return EngineStatus{}, fmt.Errorf("enginehost: status probe %s: %w", baseURL, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return EngineStatus{}, fmt.Errorf("enginehost: status probe %s: status %d", baseURL, resp.StatusCode)
		}
		if resp.ContentLength > maxStatusBodyBytes {
			return EngineStatus{}, fmt.Errorf("enginehost: status probe %s: response exceeds %d bytes", baseURL, maxStatusBodyBytes)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxStatusBodyBytes+1))
		if err != nil {
			return EngineStatus{}, fmt.Errorf("enginehost: read status probe %s: %w", baseURL, err)
		}
		if len(body) > maxStatusBodyBytes {
			return EngineStatus{}, fmt.Errorf("enginehost: status probe %s: response exceeds %d bytes", baseURL, maxStatusBodyBytes)
		}
		if err := validateStatusShape(body); err != nil {
			return EngineStatus{}, fmt.Errorf("enginehost: decode status probe %s: %w", baseURL, err)
		}

		var status EngineStatus
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&status); err != nil {
			return EngineStatus{}, fmt.Errorf("enginehost: decode status probe %s: %w", baseURL, err)
		}
		if err := requireJSONEOF(decoder); err != nil {
			return EngineStatus{}, fmt.Errorf("enginehost: decode status probe %s: %w", baseURL, err)
		}
		if err := status.validate(); err != nil {
			return EngineStatus{}, fmt.Errorf("enginehost: validate status probe %s: %w", baseURL, err)
		}
		return status, nil
	}
}

func validateStatusShape(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return fmt.Errorf("status must be one JSON object")
	}
	seen := make(map[string]struct{}, 14)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok || !approvedStatusField(key) {
			return fmt.Errorf("unapproved status field")
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate status field %q", key)
		}
		seen[key] = struct{}{}
		if key == "busiest_sources" {
			if err := validateSourceRows(decoder); err != nil {
				return err
			}
			continue
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return fmt.Errorf("unterminated status object")
	}
	if len(seen) != 14 {
		return fmt.Errorf("status is missing required fields")
	}
	return requireJSONEOF(decoder)
}

func approvedStatusField(key string) bool {
	switch key {
	case "ready", "source_workers", "per_source_limit", "queued", "running",
		"completion_sequence", "oldest_running_millis", "completed", "cancelled",
		"timed_out", "rejected", "busiest_sources", "extension_running", "extension_queued":
		return true
	default:
		return false
	}
}

func validateSourceRows(decoder *json.Decoder) error {
	start, err := decoder.Token()
	if err != nil || start != json.Delim('[') {
		return fmt.Errorf("busiest_sources must be an array")
	}
	rows := 0
	for decoder.More() {
		rows++
		if rows > 10 {
			return fmt.Errorf("busiest_sources exceeds ten rows")
		}
		start, err := decoder.Token()
		if err != nil || start != json.Delim('{') {
			return fmt.Errorf("source status must be an object")
		}
		seen := make(map[string]struct{}, 3)
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok || (key != "source_id" && key != "queued" && key != "running") {
				return fmt.Errorf("unapproved source status field")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate source status field %q", key)
			}
			seen[key] = struct{}{}
			var value json.RawMessage
			if err := decoder.Decode(&value); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return fmt.Errorf("unterminated source status object")
		}
		if len(seen) != 3 {
			return fmt.Errorf("source status is missing required fields")
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim(']') {
		return fmt.Errorf("unterminated busiest_sources array")
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func (s EngineStatus) validate() error {
	if s.SourceWorkers < 0 || s.PerSourceLimit < 0 || s.Queued < 0 || s.Running < 0 ||
		s.CompletionSequence < 0 || s.OldestRunningMillis < 0 || s.Completed < 0 ||
		s.Cancelled < 0 || s.TimedOut < 0 || s.Rejected < 0 || s.ExtensionQueued < 0 {
		return fmt.Errorf("negative counter")
	}
	if s.Running > s.SourceWorkers {
		return fmt.Errorf("running workers exceed configured workers")
	}
	if len(s.BusiestSources) > 10 {
		return fmt.Errorf("busiest_sources exceeds ten rows")
	}
	seen := make(map[int64]struct{}, len(s.BusiestSources))
	running := 0
	for _, source := range s.BusiestSources {
		if source.SourceID < 0 || source.Queued < 0 || source.Running < 0 {
			return fmt.Errorf("invalid source occupancy")
		}
		if source.Running > s.PerSourceLimit {
			return fmt.Errorf("source running count exceeds per-source limit")
		}
		if _, ok := seen[source.SourceID]; ok {
			return fmt.Errorf("duplicate source id")
		}
		seen[source.SourceID] = struct{}{}
		running += source.Running
	}
	if running != s.Running {
		return fmt.Errorf("source running sum %d does not match running %d", running, s.Running)
	}
	return nil
}

// exhaustionFingerprint combines completion progress with the canonical
// physical-running source population. Queue counts and response order are
// intentionally absent, so queue churn cannot erase stable exhaustion proof.
func (s EngineStatus) exhaustionFingerprint() (string, bool) {
	if err := s.validate(); err != nil {
		return "", false
	}
	running := make([]EngineSourceStatus, 0, len(s.BusiestSources))
	for _, source := range s.BusiestSources {
		if source.Running > 0 {
			running = append(running, source)
		}
	}
	sort.Slice(running, func(i, j int) bool { return running[i].SourceID < running[j].SourceID })

	var b strings.Builder
	b.WriteString(strconv.FormatInt(s.CompletionSequence, 10))
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(s.SourceWorkers))
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(s.Running))
	b.WriteByte('|')
	for i, source := range running {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatInt(source.SourceID, 10))
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(source.Running))
	}
	return b.String(), true
}

func logExhaustionDiagnostic(ctx context.Context, d ExhaustionDiagnostic) {
	slog.WarnContext(ctx, "enginehost: sustained source-worker exhaustion confirmed; restarting managed profile",
		"profile", d.ProfileKey,
		"pid", d.PID,
		"first_sample_at", d.FirstSample,
		"latest_sample_at", d.LatestSample,
		"fingerprint", d.Fingerprint,
		"status", d.Status)
}
