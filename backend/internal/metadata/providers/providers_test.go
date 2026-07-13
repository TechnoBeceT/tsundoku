package providers_test

import (
	"testing"

	"github.com/technobecet/tsundoku/internal/metadata/providers"
)

// TestNewRegistry_ReturnsFiveProvidersInDocumentedOrder pins the production
// provider set: exactly the five real providers, in the documented priority
// order (index 0 = primary), with strictly ascending Priority() values (the
// lower-number-wins convention — see metadata.Provider.Priority()).
func TestNewRegistry_ReturnsFiveProvidersInDocumentedOrder(t *testing.T) {
	reg := providers.NewRegistry(providers.Config{MALClientID: "test-client-id"})

	ps := reg.Providers()
	if len(ps) != 5 {
		t.Fatalf("want 5 providers, got %d", len(ps))
	}

	wantKeys := []string{"anilist", "mangadex", "mangaupdates", "mal", "kitsu"}
	for i, want := range wantKeys {
		if got := ps[i].Key(); got != want {
			t.Errorf("providers[%d].Key() = %q, want %q", i, got, want)
		}
	}

	// LOWER Priority() = higher rank: the documented order above must also be
	// strictly ascending by Priority().
	for i := 1; i < len(ps); i++ {
		if ps[i-1].Priority() >= ps[i].Priority() {
			t.Errorf("providers[%d].Priority() (%d) >= providers[%d].Priority() (%d), want strictly ascending",
				i-1, ps[i-1].Priority(), i, ps[i].Priority())
		}
	}
}
