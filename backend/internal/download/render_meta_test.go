package download_test

import (
	"testing"

	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
)

// TestBuildRenderMetaProviderLabel verifies buildRenderMeta populates the
// RenderMeta.ProviderLabel (filename display name) from SeriesProvider.ProviderName
// when set, and falls back to the source ID (SeriesProvider.Provider) when the name
// is empty. Provider (the ID) is always carried through unchanged for the sidecar +
// ComicInfo.
func TestBuildRenderMetaProviderLabel(t *testing.T) {
	t.Parallel()

	const sourceID = "7537715367149829912"

	cases := []struct {
		name         string
		providerName string
		wantLabel    string
	}{
		{
			name:         "provider name resolves the label",
			providerName: "Comick",
			wantLabel:    "Comick",
		},
		{
			name:         "empty provider name falls back to the ID",
			providerName: "",
			wantLabel:    sourceID,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ch := &ent.Chapter{}
			ch.Edges.Series = &ent.Series{Title: "Tacit"}
			pc := &ent.ProviderChapter{ChapterKey: "39"}
			sp := &ent.SeriesProvider{
				Provider:     sourceID,
				ProviderName: tc.providerName,
			}

			meta := download.BuildRenderMeta(ch, pc, sp, nil)

			if meta.ProviderLabel != tc.wantLabel {
				t.Errorf("ProviderLabel = %q, want %q", meta.ProviderLabel, tc.wantLabel)
			}
			// Provider (the ID) is always the raw source ID, never the name.
			if meta.Provider != sourceID {
				t.Errorf("Provider = %q, want %q (must stay the ID)", meta.Provider, sourceID)
			}
		})
	}
}
