package disk

import "testing"

func TestKaizokuProvenance_TsundokuExtensionsWin(t *testing.T) {
	ci := &ComicInfo{Provider: "mangadex", Scanlator: "Team X", Importance: 7}
	p, s, imp := kaizokuProvenance("[whatever] anything.cbz", ci)
	if p != "mangadex" || s != "Team X" || imp != 7 {
		t.Fatalf("got %q/%q/%d, want mangadex/Team X/7", p, s, imp)
	}
}

func TestKaizokuProvenance_ComicInfoPublisherWinsOverFilename(t *testing.T) {
	// filename bracket has no dash, so providerFromFilename yields a
	// (different) provider with no scanlator at all — ComicInfo Publisher
	// must still win over it.
	ci := &ComicInfo{Publisher: "Asura Scans", Translator: ""}
	p, s, imp := kaizokuProvenance("[mangled][en] X 1.cbz", ci)
	if p != "Asura Scans" || s != "" || imp != 1 {
		t.Fatalf("got %q/%q/%d, want Asura Scans//1", p, s, imp)
	}
}

func TestKaizokuProvenance_ComicInfoPublisherTranslatorFallback(t *testing.T) {
	ci := &ComicInfo{Publisher: "weebcentral", Translator: "Beta Group"}
	p, s, imp := kaizokuProvenance("no-bracket-here.cbz", ci)
	if p != "weebcentral" || s != "Beta Group" || imp != 1 {
		t.Fatalf("got %q/%q/%d, want weebcentral/Beta Group/1", p, s, imp)
	}
}

func TestKaizokuProvenance_ProviderNoScanlator(t *testing.T) {
	p, s, imp := kaizokuProvenance("[mangadex][en] Title 1.cbz", nil)
	if p != "mangadex" || s != "" || imp != 1 {
		t.Fatalf("got %q/%q/%d, want mangadex//1", p, s, imp)
	}
}

func TestKaizokuProvenance_NoSignalAtAll(t *testing.T) {
	p, s, imp := kaizokuProvenance("plain.cbz", nil)
	if p != "" || s != "" || imp != 1 {
		t.Fatalf("got %q/%q/%d, want //1", p, s, imp)
	}
}

func TestKaizokuProvenance_RealDataComixOfficial(t *testing.T) {
	ci := &ComicInfo{Publisher: "Comix", Translator: "Official?"}
	p, s, imp := kaizokuProvenance("[Comix-Official][en] 4 Cut Hero 156.5.cbz", ci)
	if p != "Comix" || s != "Official?" || imp != 1 {
		t.Fatalf("got %q/%q/%d, want Comix/Official?/1", p, s, imp)
	}
}

func TestKaizokuProvenance_DuplicateTranslatorDropped(t *testing.T) {
	ci := &ComicInfo{Publisher: "KaliScan.io", Translator: "KaliScan.io"}
	p, s, imp := kaizokuProvenance("[KaliScan.io][en] X 1.cbz", ci)
	if p != "KaliScan.io" || s != "" || imp != 1 {
		t.Fatalf("got %q/%q/%d, want KaliScan.io//1", p, s, imp)
	}
}

func TestKaizokuProvenance_FilenameOnlyFallbackNoComicInfo(t *testing.T) {
	p, s, imp := kaizokuProvenance("[Comix-Official][en] X 1.cbz", nil)
	if p != "Comix" || s != "Official" || imp != 1 {
		t.Fatalf("got %q/%q/%d, want Comix/Official/1", p, s, imp)
	}
}

// TestFilenameRoundTrip proves the Task 5 write/read agreement: whatever
// GenerateCBZFilename writes into the "[Provider-Scanlator]" bracket,
// kaizokuProvenance must parse back to the SAME (provider, scanlator) — the
// lossless disk↔DB round-trip a total-DB-loss Reconcile depends on.
func TestFilenameRoundTrip(t *testing.T) {
	cases := []struct {
		name          string
		provider      string
		scanlator     string
		wantProvider  string
		wantScanlator string
	}{
		{
			name:          "provider and scanlator",
			provider:      "mangadex",
			scanlator:     "dynasty",
			wantProvider:  "mangadex",
			wantScanlator: "dynasty",
		},
		{
			name:          "no scanlator",
			provider:      "mangadex",
			scanlator:     "",
			wantProvider:  "mangadex",
			wantScanlator: "",
		},
		{
			name:          "scanlator equals provider — dropped by GenerateCBZFilename",
			provider:      "mangadex",
			scanlator:     "mangadex",
			wantProvider:  "mangadex",
			wantScanlator: "",
		},
		{
			name:          "provider with hyphen sanitized to underscore",
			provider:      "manga-plus",
			scanlator:     "team-x",
			wantProvider:  "manga_plus",
			wantScanlator: "team-x",
		},
		{
			name:          "scanlator with brackets sanitized",
			provider:      "comix",
			scanlator:     "[Best] Group",
			wantProvider:  "comix",
			wantScanlator: "(Best) Group",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta := RenderMeta{
				Provider:    tc.provider,
				Scanlator:   tc.scanlator,
				Language:    "en",
				SeriesTitle: "Round Trip Test",
				Number:      ptrFloat(1),
				MaxChapter:  ptrFloat(10),
			}
			filename := GenerateCBZFilename(meta)

			gotProvider, gotScanlator, _ := kaizokuProvenance(filename, nil)
			if gotProvider != tc.wantProvider || gotScanlator != tc.wantScanlator {
				t.Fatalf("round-trip(%q) = %q/%q, want %q/%q",
					filename, gotProvider, gotScanlator, tc.wantProvider, tc.wantScanlator)
			}
		})
	}
}

func ptrFloat(f float64) *float64 { return &f }
