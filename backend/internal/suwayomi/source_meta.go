package suwayomi

import "context"

// This file (source_meta.go) holds the per-source enable/disable convention: a
// multi-language extension installs one SOURCE per language (e.g. "Comick EN",
// "Comick RU", …), and Suwayomi has NO server-side "disabled source" concept —
// the server keeps serving a disabled source over GraphQL. Enable/disable is a
// CLIENT convention using a generic per-source metadata key, matching how
// Suwayomi-WebUI itself implements the toggle:
//   - Read:  SourceType.meta { key value } — the "isEnabled" key; ABSENT means
//     enabled (default true); the literal string "false" means disabled; any
//     other value (including "true") means enabled.
//   - Write: setSourceMeta(input:{meta:{sourceId,key:"isEnabled",value:"true"|
//     "false"}}) — backed by SourceMetaTable. Re-enabling sets "true"
//     EXPLICITLY rather than deleting the meta row (owner decision: match
//     Suwayomi-WebUI's own behavior).
//
// Shape re-confirmed at merge by the build-tagged TestShape9_SourceMeta.
//
// Tsundoku itself stores none of this — like settings.go/extensions.go it is a
// PURE proxy. The filtering behavior (hiding a disabled source from Discover/
// Search/Browse) lives in internal/imports, not here; this file only reads/
// writes the flag.

// sourceEnabledMetaKey is the meta key Suwayomi(-WebUI) uses for the
// enable/disable convention.
const sourceEnabledMetaKey = "isEnabled"

// gqlSourceMetaNode is one entry of a SourceType.meta list.
type gqlSourceMetaNode struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// sourceDisabledFromMeta resolves the DISABLED flag (the inverse of "enabled",
// see Source.Disabled's doc comment for why this is the zero-value-safe
// direction) from a source's meta list: absent key ⇒ false/enabled
// (Suwayomi's own default); value == "false" ⇒ true/disabled; any other
// value (including "true") ⇒ false/enabled.
func sourceDisabledFromMeta(meta []gqlSourceMetaNode) bool {
	for _, m := range meta {
		if m.Key == sourceEnabledMetaKey {
			return m.Value == "false"
		}
	}
	return false
}

// setSourceMetaMutation writes one source meta entry. Only clientMutationId is
// selected because SetSourceEnabled's caller re-reads via Sources/
// ExtensionSources for the authoritative state (§16 round-trip lives in the
// HTTP handler, mirroring updateExtensionMutation's discipline).
const setSourceMetaMutation = `
mutation SetSourceMeta($input: SetSourceMetaInput!) {
  setSourceMeta(input: $input) {
    clientMutationId
  }
}`

// SetSourceEnabled writes the isEnabled meta key for sourceID. enabled=false
// writes the literal string "false"; enabled=true writes "true" EXPLICITLY
// (never deletes the meta row, even to restore the "absent = enabled"
// default) so a re-enable is an unambiguous, idempotent, observable write.
func (c *httpClient) SetSourceEnabled(ctx context.Context, sourceID string, enabled bool) error {
	value := "false"
	if enabled {
		value = "true"
	}
	vars := map[string]any{
		"input": map[string]any{
			"meta": map[string]any{
				"sourceId": sourceID,
				"key":      sourceEnabledMetaKey,
				"value":    value,
			},
		},
	}
	return c.doGraphQL(ctx, setSourceMetaMutation, vars, nil)
}
