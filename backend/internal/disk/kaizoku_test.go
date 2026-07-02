package disk

import "testing"

func TestKaizokuProvenance_TsundokuExtensionsWin(t *testing.T) {
	ci := &ComicInfo{Provider: "mangadex", Scanlator: "Team X", Importance: 7}
	p, s, imp := kaizokuProvenance("[whatever] anything.cbz", ci)
	if p != "mangadex" || s != "Team X" || imp != 7 {
		t.Fatalf("got %q/%q/%d, want mangadex/Team X/7", p, s, imp)
	}
}

func TestKaizokuProvenance_FilenameBracketFallback(t *testing.T) {
	ci := &ComicInfo{Publisher: "ignored-because-filename-wins", Translator: "ignored"}
	p, s, imp := kaizokuProvenance("[mangadex-Alpha Scans][en] My Series 12.5 (Finale).cbz", ci)
	if p != "mangadex" || s != "Alpha Scans" || imp != 1 {
		t.Fatalf("got %q/%q/%d, want mangadex/Alpha Scans/1", p, s, imp)
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
