package engineroute_test

import (
	"reflect"
	"testing"

	"github.com/technobecet/tsundoku/internal/engineroute"
)

// TestDerive_NoBindingsYieldsNoProfiles pins the zero-disruption invariant at the
// source: with no bindings, Derive returns no profiles, so the Router routes
// everything to the default instance — byte-for-byte today's single-instance
// behavior.
func TestDerive_NoBindingsYieldsNoProfiles(t *testing.T) {
	if got := engineroute.Derive(nil); len(got) != 0 {
		t.Fatalf("Derive(nil) = %v, want no profiles", got)
	}
	if got := engineroute.Derive([]engineroute.BindingInput{}); len(got) != 0 {
		t.Fatalf("Derive(empty) = %v, want no profiles", got)
	}
}

// TestDerive_DefaultEquivalentBindings proves a binding that is equivalent to the
// global default (no SOCKS override AND flare mode global/blank) contributes NO
// profile — its source stays on the default instance.
func TestDerive_DefaultEquivalentBindings(t *testing.T) {
	bindings := []engineroute.BindingInput{
		{SourceID: 1, FlareMode: engineroute.FlareModeGlobal},
		{SourceID: 2, FlareMode: ""}, // blank normalizes to global
	}
	if got := engineroute.Derive(bindings); len(got) != 0 {
		t.Fatalf("Derive(default-equivalent) = %v, want no profiles", got)
	}
}

// TestDerive_FlareNoneIsNonDefault proves that "none" (FlareSolverr explicitly
// OFF for a source) is a DISTINCT profile from the global default — it is not the
// same as "use whatever global is".
func TestDerive_FlareNoneIsNonDefault(t *testing.T) {
	got := engineroute.Derive([]engineroute.BindingInput{
		{SourceID: 7, FlareMode: engineroute.FlareModeNone},
	})
	if len(got) != 1 {
		t.Fatalf("Derive(flare=none) yielded %d profiles, want 1", len(got))
	}
	if got[0].FlareMode != engineroute.FlareModeNone {
		t.Fatalf("profile flare mode = %q, want %q", got[0].FlareMode, engineroute.FlareModeNone)
	}
	if !reflect.DeepEqual(got[0].SourceIDs, []int64{7}) {
		t.Fatalf("profile sources = %v, want [7]", got[0].SourceIDs)
	}
}

// TestDerive_GroupsSourcesBySameProfile proves two sources with the SAME socks
// endpoint + same flare config collapse into ONE profile (one instance serves
// both), while a different endpoint is a separate profile.
func TestDerive_GroupsSourcesBySameProfile(t *testing.T) {
	vpn := &engineroute.SocksEndpoint{ID: "vpn-uuid", Host: "10.0.0.1", Port: 1080, Version: 5}
	other := &engineroute.SocksEndpoint{ID: "other-uuid", Host: "10.0.0.2", Port: 1080, Version: 5}

	got := engineroute.Derive([]engineroute.BindingInput{
		{SourceID: 3, Socks: vpn, FlareMode: engineroute.FlareModeGlobal},
		{SourceID: 1, Socks: vpn, FlareMode: engineroute.FlareModeGlobal},
		{SourceID: 9, Socks: other, FlareMode: engineroute.FlareModeGlobal},
	})
	if len(got) != 2 {
		t.Fatalf("Derive yielded %d profiles, want 2", len(got))
	}
	// Profiles are Key-ordered and each SourceIDs is ascending — find the vpn one.
	var vpnProfile *engineroute.Profile
	for i := range got {
		if got[i].Socks != nil && got[i].Socks.ID == "vpn-uuid" {
			vpnProfile = &got[i]
		}
	}
	if vpnProfile == nil {
		t.Fatalf("no profile for the vpn endpoint in %+v", got)
	}
	if !reflect.DeepEqual(vpnProfile.SourceIDs, []int64{1, 3}) {
		t.Fatalf("vpn profile sources = %v, want [1 3] (grouped + sorted)", vpnProfile.SourceIDs)
	}
}

// TestDerive_EndpointFlareIsDistinctFromSocksOnly proves the SOCKS endpoint id
// AND the flare endpoint id both enter the profile key, so a socks-only binding
// and a socks+flare-endpoint binding on the same socks endpoint are two profiles.
func TestDerive_EndpointFlareIsDistinctProfile(t *testing.T) {
	vpn := &engineroute.SocksEndpoint{ID: "vpn", Host: "10.0.0.1", Port: 1080, Version: 5}
	fs := &engineroute.FlareEndpoint{ID: "fs", URL: "http://fs:8191"}

	got := engineroute.Derive([]engineroute.BindingInput{
		{SourceID: 1, Socks: vpn, FlareMode: engineroute.FlareModeGlobal},
		{SourceID: 2, Socks: vpn, FlareMode: engineroute.FlareModeEndpoint, Flare: fs},
	})
	if len(got) != 2 {
		t.Fatalf("Derive yielded %d profiles, want 2 (socks-only vs socks+flare-endpoint)", len(got))
	}
}

// TestDerive_Deterministic proves the same input always yields the same ordering
// (Key-ordered profiles, ascending source ids) — the property that makes a
// reconcile that changes nothing push nothing.
func TestDerive_Deterministic(t *testing.T) {
	a := &engineroute.SocksEndpoint{ID: "aaa", Host: "h", Port: 1, Version: 5}
	b := &engineroute.SocksEndpoint{ID: "bbb", Host: "h", Port: 2, Version: 5}
	in := []engineroute.BindingInput{
		{SourceID: 5, Socks: b, FlareMode: engineroute.FlareModeGlobal},
		{SourceID: 2, Socks: a, FlareMode: engineroute.FlareModeGlobal},
	}
	first := engineroute.Derive(in)
	second := engineroute.Derive(in)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Derive not deterministic:\n first=%+v\nsecond=%+v", first, second)
	}
	if first[0].Key >= first[1].Key {
		t.Fatalf("profiles not Key-ordered: %q then %q", first[0].Key, first[1].Key)
	}
}
