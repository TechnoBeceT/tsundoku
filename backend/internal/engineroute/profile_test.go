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
	if got := engineroute.Derive(true, nil); len(got) != 0 {
		t.Fatalf("Derive(nil) = %v, want no profiles", got)
	}
	if got := engineroute.Derive(true, []engineroute.BindingInput{}); len(got) != 0 {
		t.Fatalf("Derive(empty) = %v, want no profiles", got)
	}
}

// TestDerive_DefaultEquivalentBindings proves a binding that is equivalent to the
// global default (no SOCKS override AND flare mode global/blank) contributes NO
// profile — its source stays on the default instance.
func TestDerive_DefaultEquivalentBindings(t *testing.T) {
	bindings := []engineroute.BindingInput{
		{SourceID: 1, FlareMode: engineroute.FlareModeGlobal, KCEFEnabled: true},
		{SourceID: 2, FlareMode: "", KCEFEnabled: true}, // blank normalizes to global
	}
	if got := engineroute.Derive(true, bindings); len(got) != 0 {
		t.Fatalf("Derive(default-equivalent) = %v, want no profiles", got)
	}
}

// TestDerive_KCEFDefaultEquivalence keeps legacy profile keys unchanged while
// separating sources whose effective embedded-browser capability differs from
// the entrypoint-managed default host.
func TestDerive_KCEFDefaultEquivalence(t *testing.T) {
	t.Parallel()

	defaultRoute := engineroute.BindingInput{SourceID: 1, FlareMode: engineroute.FlareModeGlobal, KCEFEnabled: true}
	requireProfileCount(t, "default-on equivalent", engineroute.Derive(true, []engineroute.BindingInput{defaultRoute}), 0)

	disabled := defaultRoute
	disabled.SourceID = 2
	disabled.KCEFEnabled = false
	got := engineroute.Derive(true, []engineroute.BindingInput{disabled, {SourceID: 3, FlareMode: engineroute.FlareModeGlobal, KCEFEnabled: false}})
	requireProfileCount(t, "default-on disabled", got, 1)
	if got[0].Key != "kcef=off" || got[0].KCEFEnabled || !reflect.DeepEqual(got[0].SourceIDs, []int64{2, 3}) {
		t.Fatalf("Derive(default-on disabled) = %+v, want grouped KCEF-off profile", got[0])
	}

	required := engineroute.BindingInput{SourceID: 4, FlareMode: engineroute.FlareModeGlobal, KCEFEnabled: true}
	got = engineroute.Derive(false, []engineroute.BindingInput{required})
	if got[0].Key != "kcef=on" || !got[0].KCEFEnabled {
		t.Fatalf("Derive(default-off enabled) = %+v, want KCEF-on profile", got)
	}

	requiredEndpoint := engineroute.Derive(true, []engineroute.BindingInput{{
		SourceID: 5, FlareMode: engineroute.FlareModeEndpoint,
		Flare: &engineroute.FlareEndpoint{ID: "flare"}, KCEFEnabled: true,
	}})
	requireProfileCount(t, "required endpoint", requiredEndpoint, 1)
	if requiredEndpoint[0].Key != "|endpoint|flare|kcef=on" {
		t.Fatalf("Derive(required endpoint) = %+v, want KCEF-on divergence key", requiredEndpoint)
	}
}

func requireProfileCount(t *testing.T, scenario string, profiles []engineroute.Profile, want int) {
	t.Helper()
	if len(profiles) != want {
		t.Fatalf("Derive(%s) yielded %d profiles, want %d", scenario, len(profiles), want)
	}
}

// TestDerive_AutoEndpointKeepsLegacyProfileKey proves an endpoint-routed source
// whose Auto policy resolves KCEF off keeps the key established before KCEF
// policy existed. It is already a managed network profile, so default-host
// equivalence must not rewrite its identity.
func TestDerive_AutoEndpointKeepsLegacyProfileKey(t *testing.T) {
	t.Parallel()

	got := engineroute.Derive(true, []engineroute.BindingInput{{
		SourceID:    9,
		FlareMode:   engineroute.FlareModeEndpoint,
		Flare:       &engineroute.FlareEndpoint{ID: "flare"},
		KCEFEnabled: false,
	}})
	if len(got) != 1 {
		t.Fatalf("Derive() yielded %d profiles, want 1", len(got))
	}
	if got[0].Key != "|endpoint|flare" || got[0].KCEFEnabled {
		t.Fatalf("Derive(Auto endpoint) = %+v, want legacy endpoint key with KCEF off", got[0])
	}
}

// TestDerive_FlareNoneIsNonDefault proves that "none" (FlareSolverr explicitly
// OFF for a source) is a DISTINCT profile from the global default — it is not the
// same as "use whatever global is".
func TestDerive_FlareNoneIsNonDefault(t *testing.T) {
	got := engineroute.Derive(true, []engineroute.BindingInput{
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

	got := engineroute.Derive(true, []engineroute.BindingInput{
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

	got := engineroute.Derive(true, []engineroute.BindingInput{
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
	first := engineroute.Derive(true, in)
	second := engineroute.Derive(true, in)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Derive not deterministic:\n first=%+v\nsecond=%+v", first, second)
	}
	if first[0].Key >= first[1].Key {
		t.Fatalf("profiles not Key-ordered: %q then %q", first[0].Key, first[1].Key)
	}
}

// TestDerive_SessionPolicyMatrix proves disposable-session policy participates
// in profile identity exactly when FlareSolverr can be used. Inherit and On
// both keep the selected configured session, while Off requires an isolated
// profile that will push a blank session. Flare mode none ignores the policy
// because no bypass request can use a session there.
func TestDerive_SessionPolicyMatrix(t *testing.T) { //nolint:gocognit,cyclop // Table-driven routing matrix intentionally checks each independent result dimension.
	t.Parallel()

	policies := []struct {
		name    string
		disable bool
	}{
		{name: "inherit"},
		{name: "on"},
		{name: "off", disable: true},
	}
	modes := []string{
		engineroute.FlareModeGlobal,
		engineroute.FlareModeEndpoint,
		engineroute.FlareModeNone,
	}
	sessions := []struct {
		name  string
		value string
	}{
		{name: "blank"},
		{name: "nonblank", value: "shared-session"},
	}

	for _, policy := range policies {
		for _, mode := range modes {
			for _, session := range sessions {
				name := policy.name + "/" + mode + "/" + session.name
				t.Run(name, func(t *testing.T) {
					input := engineroute.BindingInput{
						SourceID:             41,
						FlareMode:            mode,
						DisableBypassSession: policy.disable,
						KCEFEnabled:          true,
					}
					if mode == engineroute.FlareModeEndpoint {
						input.Flare = &engineroute.FlareEndpoint{
							ID: "flare-endpoint", URL: "http://flare.test:8191",
							Session: session.value,
						}
					}

					got := engineroute.Derive(true, []engineroute.BindingInput{input})
					if mode == engineroute.FlareModeGlobal && !policy.disable {
						if len(got) != 0 {
							t.Fatalf("Derive() = %+v, want default-equivalent binding", got)
						}
						return
					}
					if len(got) != 1 {
						t.Fatalf("Derive() yielded %d profiles, want 1", len(got))
					}
					wantDisable := policy.disable && mode != engineroute.FlareModeNone
					if got[0].DisableBypassSession != wantDisable {
						t.Fatalf("profile DisableBypassSession = %v, want %v", got[0].DisableBypassSession, wantDisable)
					}
				})
			}
		}
	}
}

// TestDerive_EquivalentDisposableSessionsGroup proves two sources with the same
// stable endpoint identities and Off policy share one disposable-session
// instance. Mutable endpoint fields, including the configured session value,
// do not split the profile.
func TestDerive_EquivalentDisposableSessionsGroup(t *testing.T) {
	t.Parallel()

	got := engineroute.Derive(true, []engineroute.BindingInput{
		{
			SourceID: 8, FlareMode: engineroute.FlareModeEndpoint,
			Flare:                &engineroute.FlareEndpoint{ID: "flare", URL: "http://old", Session: "old"},
			DisableBypassSession: true,
		},
		{
			SourceID: 3, FlareMode: engineroute.FlareModeEndpoint,
			Flare:                &engineroute.FlareEndpoint{ID: "flare", URL: "http://new", Session: "new"},
			DisableBypassSession: true,
		},
	})

	if len(got) != 1 {
		t.Fatalf("Derive() yielded %d profiles, want 1 equivalent Off profile", len(got))
	}
	if !reflect.DeepEqual(got[0].SourceIDs, []int64{3, 8}) {
		t.Fatalf("profile sources = %v, want [3 8]", got[0].SourceIDs)
	}
	if !got[0].DisableBypassSession {
		t.Fatal("profile DisableBypassSession = false, want true")
	}
}

// TestDerive_SessionPolicyUsesStableIdentity proves changing mutable endpoint
// config does not churn a profile key, while changing Off to inherited policy
// changes the key because the effective session behavior changes.
func TestDerive_SessionPolicyUsesStableIdentity(t *testing.T) {
	t.Parallel()

	deriveKey := func(flare *engineroute.FlareEndpoint, disable bool) string {
		t.Helper()
		got := engineroute.Derive(true, []engineroute.BindingInput{{
			SourceID: 1, FlareMode: engineroute.FlareModeEndpoint, Flare: flare,
			DisableBypassSession: disable,
		}})
		if len(got) != 1 {
			t.Fatalf("Derive() yielded %d profiles, want 1", len(got))
		}
		return got[0].Key
	}

	oldKey := deriveKey(&engineroute.FlareEndpoint{ID: "flare", URL: "http://old", Session: "old", Timeout: 10}, true)
	newKey := deriveKey(&engineroute.FlareEndpoint{ID: "flare", URL: "http://new", Session: "new", Timeout: 20}, true)
	if oldKey != newKey {
		t.Fatalf("mutable endpoint edit changed profile key: %q != %q", oldKey, newKey)
	}
	if inheritedKey := deriveKey(&engineroute.FlareEndpoint{ID: "flare", URL: "http://new", Session: "new"}, false); inheritedKey == newKey {
		t.Fatalf("Off and inherited endpoint profiles share key %q", inheritedKey)
	}
}

// TestDerive_ClearingSessionOffRestoresDefaultEquivalence proves removing the
// Off override from an otherwise-global source removes its non-default profile.
func TestDerive_ClearingSessionOffRestoresDefaultEquivalence(t *testing.T) {
	t.Parallel()

	off := engineroute.Derive(true, []engineroute.BindingInput{{
		SourceID: 1, FlareMode: engineroute.FlareModeGlobal, DisableBypassSession: true,
	}})
	if len(off) != 1 {
		t.Fatalf("Off Derive() yielded %d profiles, want 1", len(off))
	}
	inherited := engineroute.Derive(true, []engineroute.BindingInput{{
		SourceID: 1, FlareMode: engineroute.FlareModeGlobal, KCEFEnabled: true,
	}})
	if len(inherited) != 0 {
		t.Fatalf("cleared Off Derive() = %+v, want default-equivalent binding", inherited)
	}
}
