package sourceengine_test

import (
	"encoding/json"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// TestAddressModeJSONRoundTrip catches a dropped or misspelled addressMode
// field on engine candidates. Search/browse is the provenance boundary: if a
// mode disappears here, every later adopt is forced back through legacy
// unknown-mode probing.
func TestAddressModeJSONRoundTrip(t *testing.T) {
	for _, want := range []string{"unknown", "direct", "url_search"} {
		t.Run(want, func(t *testing.T) {
			var entry sourceengine.MangaEntry
			if err := json.Unmarshal([]byte(`{"url":"opaque-key","addressMode":"`+want+`"}`), &entry); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			raw, err := json.Marshal(entry)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("decode round trip: %v", err)
			}
			if got["addressMode"] != want {
				t.Fatalf("addressMode after round trip = %v, want %q", got["addressMode"], want)
			}
		})
	}
}

// TestAddressModeJSONLegacyDefault proves an engine response from before the
// additive field existed remains compatible and is represented as unknown,
// never as an empty or invented known mode.
func TestAddressModeJSONLegacyDefault(t *testing.T) {
	var details sourceengine.MangaDetails
	if err := json.Unmarshal([]byte(`{"url":"/manga/legacy","title":"Legacy"}`), &details); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	raw, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode round trip: %v", err)
	}
	if got["addressMode"] != "unknown" {
		t.Fatalf("legacy omitted addressMode round trip = %v, want unknown", got["addressMode"])
	}
}

func TestAddressModeJSONRejectsInvalid(t *testing.T) {
	var entry sourceengine.MangaEntry
	if err := json.Unmarshal([]byte(`{"url":"opaque-key","addressMode":"unsupported"}`), &entry); err == nil {
		t.Fatal("invalid addressMode was accepted")
	}
}
