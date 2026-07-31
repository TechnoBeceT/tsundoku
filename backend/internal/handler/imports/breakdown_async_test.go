package imports_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sse"
)

// breakdownResponse is the wire shape GET .../breakdown answers with since
// GAP-140 (Task 4): the same Total/Scanlators rows as before, plus the
// persisted snapshot's own state, so a client can tell "ready and fresh" from
// "still converging" from "broken" without a separate poll.
type breakdownResponse struct {
	imports.SourceBreakdownDTO
	Status     string `json:"status"`
	ComputedAt string `json:"computedAt"`
	Error      string `json:"error"`
}

// doBreakdownRequest wires fc into a fresh handler environment and issues one
// authenticated GET against the breakdown endpoint for (sourceID, url),
// returning the raw response so callers assert on status/body directly.
func doBreakdownRequest(t *testing.T, fc *fakeEngineClient, sourceID, url string) *httptest.ResponseRecorder {
	t.Helper()
	env := newTestEnv(t, fc)
	return env.do(http.MethodGet, fmt.Sprintf("/api/sources/%s/manga/1/breakdown?url=%s", sourceID, url), "")
}

// blockingEngine builds a fakeEngineClient whose Chapters call blocks until
// released, standing in for the slow WebView walk GAP-140 exists to survive
// (~330 navigations for a 1,301-chapter series). t.Cleanup releases it
// unconditionally so a test that never explicitly does so doesn't leave the
// background goroutine parked past the test's lifetime.
func blockingEngine(t *testing.T) *fakeEngineClient {
	t.Helper()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	sourceID, err := strconv.ParseInt("42", 10, 64)
	if err != nil {
		t.Fatalf("source id: %v", err)
	}
	return &fakeEngineClient{
		sources: []sourceengine.Source{{ID: sourceID, Name: "Test Source", Lang: "en"}},
		blockCh: release,
	}
}

// doBreakdownRequestAfterCompute drives ONE completed walk to persist a READY
// snapshot for (sourceID, url) — `total` synthetic chapters, resolved
// near-instantly by the fake engine so the FIRST request already lands within
// the fast-path window — then issues a SECOND request for the same pair and
// asserts the engine's Chapters was never called again: the later view must
// come straight from the persisted snapshot, not repeat the walk (the whole
// point of GAP-140 — one completed computation makes every later view free).
func doBreakdownRequestAfterCompute(t *testing.T, sourceID, url string, total int) *httptest.ResponseRecorder {
	t.Helper()

	numericID, err := strconv.ParseInt(sourceID, 10, 64)
	if err != nil {
		t.Fatalf("source id: %v", err)
	}
	fc := &fakeEngineClient{
		sources:       []sourceengine.Source{{ID: numericID, Name: "Test Source", Lang: "en"}},
		chaptersByURL: map[string][]sourceengine.Chapter{url: makeChapters(url, total)},
	}
	env := newTestEnv(t, fc)

	target := fmt.Sprintf("/api/sources/%s/manga/1/breakdown?url=%s", sourceID, url)
	seed := env.do(http.MethodGet, target, "")
	if seed.Code != http.StatusOK {
		t.Fatalf("seed request: want 200, got %d (%s)", seed.Code, seed.Body.String())
	}

	callsAfterSeed := fc.chaptersCallCount
	second := env.do(http.MethodGet, target, "")
	if fc.chaptersCallCount != callsAfterSeed {
		t.Errorf("Chapters called again on the second request (calls %d -> %d) — the snapshot was not served from the store",
			callsAfterSeed, fc.chaptersCallCount)
	}
	return second
}

// TestBreakdownReturnsPendingRatherThanHanging is the whole point of GAP-140.
// A 1,301-chapter series needs ~330 WebView navigations (~15-20 min), which no
// HTTP timeout tolerates — the endpoint used to 502 and cache NOTHING, so every
// retry paid full price. It must now answer promptly with `pending` and let the
// work continue in the background.
func TestBreakdownReturnsPendingRatherThanHanging(t *testing.T) {
	// Engine fake blocks until the test releases it, standing in for the slow walk.
	rec := doBreakdownRequest(t, blockingEngine(t), "42", "/qly0d-apotheosis")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a pending body", rec.Code)
	}
	var body breakdownResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "pending" {
		t.Errorf("status = %q, want pending (the slow walk must not block the response)", body.Status)
	}
}

// TestBreakdownServesThePersistedSnapshot proves a second request is INSTANT and
// carries the as-of, i.e. one completed walk makes every later view free.
func TestBreakdownServesThePersistedSnapshot(t *testing.T) {
	rec := doBreakdownRequestAfterCompute(t, "42", "/x", 1301)

	var body breakdownResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ready" || body.Total != 1301 {
		t.Errorf("body = %+v, want ready with 1301", body)
	}
	if body.ComputedAt == "" {
		t.Error("computedAt empty — the owner cannot tell how stale the snapshot is")
	}
}

// doCancelingRequest mimics what a REAL HTTP server does to a request's
// context: it is canceled the instant the top-level handler returns
// (net/http's conn.serve derives a per-request context and cancels it right
// after ServeHTTP returns, before serving the next request on the
// connection). httptest.NewRequest's own context is plain context.Background()
// and NOTHING ever cancels it, so a plain env.do call cannot exercise
// imports.Service.Coverage's detachment at all — this helper closes that gap
// by deriving a cancelable context and cancelling it the moment ServeHTTP
// returns, exactly where production would.
func doCancelingRequest(env *testEnv, method, target string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.Header.Set("Authorization", "Bearer "+env.token)
	ctx, cancel := context.WithCancel(r.Context())
	r = r.WithContext(ctx)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	cancel()
	return rec
}

// decodeCoverageDone decodes an sse.Event's payload as a CoverageDoneEvent, or
// reports a test failure if the event isn't shaped the way
// broadcastCoverageDone always sends it (extracted from awaitCoverageDone to
// keep that function's branching under the lint complexity threshold).
func decodeCoverageDone(t *testing.T, ev sse.Event) imports.CoverageDoneEvent {
	t.Helper()
	raw, ok := ev.Data.(json.RawMessage)
	if !ok {
		t.Fatalf("imports.coverage.done payload is %T, want json.RawMessage", ev.Data)
	}
	var done imports.CoverageDoneEvent
	if err := json.Unmarshal(raw, &done); err != nil {
		t.Fatalf("decode imports.coverage.done: %v", err)
	}
	return done
}

// awaitCoverageDone reads events until an imports.coverage.done for
// (sourceID, mangaURL) arrives or the deadline passes, returning nil on
// timeout. Deadline-based, never time.Sleep.
func awaitCoverageDone(t *testing.T, events <-chan sse.Event, sourceID, mangaURL string) *imports.CoverageDoneEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Type != "imports.coverage.done" {
				continue
			}
			done := decodeCoverageDone(t, ev)
			if done.SourceID == sourceID && done.MangaURL == mangaURL {
				return &done
			}
		case <-deadline:
			return nil
		}
	}
}

// TestCoverageSurvivesRequestContextCancellation is the direct proof behind
// GAP-140's single most load-bearing line: imports.Service.Coverage runs the
// background walk on context.WithoutCancel(ctx), specifically so it survives
// the ORIGINATING request's own context being torn down (which a real HTTP
// server does the instant the handler returns — see doCancelingRequest).
//
// It drives the request through the SAME timeout branch a 20-minute walk
// would take (the engine blocks past coverageFastPath), cancels the request's
// context exactly like production does, THEN lets the walk finish — proving
// the persisted outcome is "ready", not silently lost to a cancelled context.
func TestCoverageSurvivesRequestContextCancellation(t *testing.T) {
	const sourceID, url = "42", "/qly0d-apotheosis"
	numericID, err := strconv.ParseInt(sourceID, 10, 64)
	if err != nil {
		t.Fatalf("source id: %v", err)
	}

	release := make(chan struct{})
	fc := &fakeEngineClient{
		sources:       []sourceengine.Source{{ID: numericID, Name: "Test Source", Lang: "en"}},
		chaptersByURL: map[string][]sourceengine.Chapter{url: makeChapters(url, 7)},
		blockCh:       release,
	}
	env := newTestEnv(t, fc)
	events, unsubscribe := env.hub.Subscribe()
	defer unsubscribe()

	// The engine blocks, so this request rides the fast-path timeout branch
	// and returns `pending` — then its OWN context is cancelled the instant
	// the handler returns, exactly like a real server would.
	rec := doCancelingRequest(env, http.MethodGet, fmt.Sprintf("/api/sources/%s/manga/1/breakdown?url=%s", sourceID, url))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a pending body", rec.Code)
	}
	var body breakdownResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "pending" {
		t.Fatalf("status = %q, want pending", body.Status)
	}

	// Now let the walk actually finish — the request that started it is long
	// gone (its context was cancelled above), so the ONLY way this outcome
	// can be "ready" is if the background computation runs on a context that
	// survives that cancellation.
	close(release)

	done := awaitCoverageDone(t, events, sourceID, url)
	if done == nil {
		t.Fatal("no imports.coverage.done — the background computation never reported an outcome")
	}
	if done.Status != "ready" || done.Total != 7 {
		t.Errorf("coverage.done = %+v, want ready with 7 chapters (the computation must survive the request's context being cancelled)", done)
	}
}
