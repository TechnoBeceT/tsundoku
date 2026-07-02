package disk

import "strings"

// kaizokuProvenance resolves the origin provider/scanlator/importance for an
// orphan CBZ that may have been written by Kaizoku (which stores provider in the
// filename bracket and ComicInfo Publisher/Translator, with no importance).
// Preference order: Tsundoku's own ComicInfo extensions → the ComicInfo
// Publisher/Translator (Kaizoku.NET's authoritative, clean provenance) → the
// filename's first [Provider-Scanlator] bracket, used only as a last-resort
// fallback since Kaizoku.NET filenames carry filesystem-mangled characters.
// Importance defaults to 1 so any matched Suwayomi source (importance >= 2)
// outranks it.
func kaizokuProvenance(filename string, ci *ComicInfo) (provider, scanlator string, importance int) {
	var ciProvider, ciScanlator, ciPublisher, ciTranslator string
	importance = 1
	if ci != nil {
		ciProvider, ciScanlator = ci.Provider, ci.Scanlator
		ciPublisher, ciTranslator = ci.Publisher, ci.Translator
		if ci.Importance > 0 {
			importance = ci.Importance
		}
	}

	fProvider, fScanlator := providerFromFilename(filename)

	// Prefer Tsundoku's own extension fields, then the ComicInfo
	// Publisher/Translator (Kaizoku.NET's authoritative, clean provenance),
	// and only fall back to the filesystem-mangled filename bracket when
	// neither is present.
	provider = firstNonEmpty(ciProvider, ciPublisher, fProvider)
	scanlator = firstNonEmpty(ciScanlator, ciTranslator, fScanlator)

	// A scanlator that merely duplicates the provider carries no information
	// (some Kaizoku.NET sources set Publisher == Translator, e.g. "KaliScan.io").
	if scanlator == provider {
		scanlator = ""
	}
	return provider, scanlator, importance
}

// providerFromFilename extracts provider/scanlator from the FIRST bracket of a
// Kaizoku-style name: "[Provider-Scanlator][lang] Title ...". The provider is
// the text before the first '-'; the scanlator is the remainder (empty if none).
func providerFromFilename(filename string) (provider, scanlator string) {
	open := strings.IndexByte(filename, '[')
	if open < 0 {
		return "", ""
	}
	close := strings.IndexByte(filename[open:], ']')
	if close < 0 {
		return "", ""
	}
	token := filename[open+1 : open+close]
	if dash := strings.IndexByte(token, '-'); dash >= 0 {
		return strings.TrimSpace(token[:dash]), strings.TrimSpace(token[dash+1:])
	}
	return strings.TrimSpace(token), ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
