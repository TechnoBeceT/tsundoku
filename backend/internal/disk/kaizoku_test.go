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
