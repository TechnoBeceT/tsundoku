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
		body, err := readStatusBody(ctx, client, baseURL)
		if err != nil {
			return EngineStatus{}, err
		}
		return decodeStatus(body, baseURL)
	}
}

func readStatusBody(ctx context.Context, client *http.Client, baseURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/status", nil)
	if err != nil {
		return nil, fmt.Errorf("enginehost: build status probe: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("enginehost: status probe %s: %w", baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("enginehost: status probe %s: status %d", baseURL, resp.StatusCode)
	}
	if resp.ContentLength > maxStatusBodyBytes {
		return nil, fmt.Errorf("enginehost: status probe %s: response exceeds %d bytes", baseURL, maxStatusBodyBytes)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxStatusBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("enginehost: read status probe %s: %w", baseURL, err)
	}
	if len(body) > maxStatusBodyBytes {
		return nil, fmt.Errorf("enginehost: status probe %s: response exceeds %d bytes", baseURL, maxStatusBodyBytes)
	}
	return body, nil
}

func decodeStatus(body []byte, baseURL string) (EngineStatus, error) {
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

func validateStatusShape(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := requireJSONDelim(decoder, '{', "status must be one JSON object"); err != nil {
		return err
	}
	seen, err := validateStatusFields(decoder)
	if err != nil {
		return err
	}
	if err := requireJSONDelim(decoder, '}', "unterminated status object"); err != nil {
		return err
	}
	if len(seen) != 14 {
		return fmt.Errorf("status is missing required fields")
	}
	return requireJSONEOF(decoder)
}

func validateStatusFields(decoder *json.Decoder) (map[string]struct{}, error) {
	seen := make(map[string]struct{}, 14)
	for decoder.More() {
		if err := validateStatusField(decoder, seen); err != nil {
			return nil, err
		}
	}
	return seen, nil
}

func validateStatusField(decoder *json.Decoder, seen map[string]struct{}) error {
	key, err := readApprovedField(decoder, approvedStatusField, "unapproved status field")
	if err != nil {
		return err
	}
	if _, duplicate := seen[key]; duplicate {
		return fmt.Errorf("duplicate status field %q", key)
	}
	seen[key] = struct{}{}
	if key == "busiest_sources" {
		return validateSourceRows(decoder)
	}
	return discardJSONValue(decoder)
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
	if err := requireJSONDelim(decoder, '[', "busiest_sources must be an array"); err != nil {
		return err
	}
	rows := 0
	for decoder.More() {
		rows++
		if rows > 10 {
			return fmt.Errorf("busiest_sources exceeds ten rows")
		}
		if err := validateSourceRow(decoder); err != nil {
			return err
		}
	}
	return requireJSONDelim(decoder, ']', "unterminated busiest_sources array")
}

func validateSourceRow(decoder *json.Decoder) error {
	if err := requireJSONDelim(decoder, '{', "source status must be an object"); err != nil {
		return err
	}
	seen := make(map[string]struct{}, 3)
	for decoder.More() {
		key, err := readApprovedField(decoder, approvedSourceField, "unapproved source status field")
		if err != nil {
			return err
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate source status field %q", key)
		}
		seen[key] = struct{}{}
		if err := discardJSONValue(decoder); err != nil {
			return err
		}
	}
	if err := requireJSONDelim(decoder, '}', "unterminated source status object"); err != nil {
		return err
	}
	if len(seen) != 3 {
		return fmt.Errorf("source status is missing required fields")
	}
	return nil
}

func approvedSourceField(key string) bool {
	return key == "source_id" || key == "queued" || key == "running"
}

func readApprovedField(decoder *json.Decoder, approved func(string) bool, message string) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", err
	}
	key, ok := token.(string)
	if !ok || !approved(key) {
		return "", fmt.Errorf("%s", message)
	}
	return key, nil
}

func requireJSONDelim(decoder *json.Decoder, want json.Delim, message string) error {
	token, err := decoder.Token()
	if err != nil || token != want {
		return fmt.Errorf("%s", message)
	}
	return nil
}

func discardJSONValue(decoder *json.Decoder) error {
	var value json.RawMessage
	return decoder.Decode(&value)
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
	if err := s.validateCounters(); err != nil {
		return err
	}
	if s.Running > s.SourceWorkers {
		return fmt.Errorf("running workers exceed configured workers")
	}
	if len(s.BusiestSources) > 10 {
		return fmt.Errorf("busiest_sources exceeds ten rows")
	}
	running, err := s.validateSourceOccupancy()
	if err != nil {
		return err
	}
	if running != s.Running {
		return fmt.Errorf("source running sum %d does not match running %d", running, s.Running)
	}
	return nil
}

func (s EngineStatus) validateCounters() error {
	counters := []int64{
		int64(s.SourceWorkers), int64(s.PerSourceLimit), int64(s.Queued), int64(s.Running),
		s.CompletionSequence, s.OldestRunningMillis, s.Completed, s.Cancelled, s.TimedOut,
		s.Rejected, int64(s.ExtensionQueued),
	}
	for _, counter := range counters {
		if counter < 0 {
			return fmt.Errorf("negative counter")
		}
	}
	return nil
}

func (s EngineStatus) validateSourceOccupancy() (int, error) {
	seen := make(map[int64]struct{}, len(s.BusiestSources))
	running := 0
	for _, source := range s.BusiestSources {
		if source.SourceID < 0 || source.Queued < 0 || source.Running < 0 {
			return 0, fmt.Errorf("invalid source occupancy")
		}
		if source.Running > s.PerSourceLimit {
			return 0, fmt.Errorf("source running count exceeds per-source limit")
		}
		if _, ok := seen[source.SourceID]; ok {
			return 0, fmt.Errorf("duplicate source id")
		}
		seen[source.SourceID] = struct{}{}
		running += source.Running
	}
	return running, nil
}

// exhaustionFingerprint is the canonical physical-running source population.
// Completion progress is evaluated separately by the supervisor; queue counts
// and response order are intentionally absent.
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
