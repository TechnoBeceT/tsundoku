package sourceconfiguration

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/network"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sourcethroughput"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

type catalogStub struct {
	sources []sourceengine.Source
	err     error
	calls   int
}

func (s *catalogStub) Sources(context.Context) ([]sourceengine.Source, error) {
	s.calls++
	return append([]sourceengine.Source(nil), s.sources...), s.err
}

type globalsStub struct {
	value globalSnapshot
	err   error
	calls int
}

func (s *globalsStub) Snapshot(context.Context) (globalSnapshot, error) {
	s.calls++
	return s.value, s.err
}

type throughputStub struct {
	value throughputSnapshot
	err   error
	calls int
}

func (s *throughputStub) Snapshot(context.Context) (throughputSnapshot, error) {
	s.calls++
	return s.value, s.err
}

type transportStub struct {
	value transportSnapshot
	err   error
	calls int
}

func (s *transportStub) Snapshot(context.Context) (transportSnapshot, error) {
	s.calls++
	return s.value, s.err
}

type routingStub struct {
	value routingSnapshot
	err   error
	calls int
}

func (s *routingStub) Snapshot(context.Context) (routingSnapshot, error) {
	s.calls++
	return s.value, s.err
}

type runtimeStub struct {
	value map[int64]sourcetransport.Intent
	err   error
	calls int
}

func (s *runtimeStub) Snapshot(context.Context) (map[int64]sourcetransport.Intent, error) {
	s.calls++
	return s.value, s.err
}

func boolPtr(v bool) *bool { return &v }

func intPtr(v int) *int { return &v }

func durationPtr(v time.Duration) *time.Duration { return &v }

func imageModePtr(v sourcetransport.ImageConnectionMode) *sourcetransport.ImageConnectionMode {
	return &v
}

func TestEffectiveConfigurationResolvesPoliciesRoutingProxyProfileAndRuntime(t *testing.T) {
	const (
		inheritedID     int64 = 101
		exceptionID     int64 = 202
		blankEndpointID int64 = 303
		noneID          int64 = 404
		explicitOnID    int64 = 505
	)
	attempt := time.Date(2026, 8, 30, 12, 30, 0, 0, time.UTC)
	socksID := "00000000-0000-0000-0000-000000000001"
	flareID := "00000000-0000-0000-0000-000000000002"
	blankFlareID := "00000000-0000-0000-0000-000000000003"
	deps := dependencies{
		catalog: &catalogStub{sources: []sourceengine.Source{
			{ID: noneID, Name: "None", Lang: "ja"},
			{ID: exceptionID, Name: "Exception", Lang: "en"},
			{ID: inheritedID, Name: "Inherited", Lang: "en"},
			{ID: blankEndpointID, Name: "Blank", Lang: "tr"},
			{ID: explicitOnID, Name: "Explicit on", Lang: "en"},
		}},
		globals: &globalsStub{value: globalSnapshot{
			WarmupInterval:        15 * time.Minute,
			WarmupSlowThresholdMs: 1250,
			FailureThreshold:      4,
			SourceCooldown:        20 * time.Minute,
			PolitenessDelay:       750 * time.Millisecond,
			BypassEnabled:         true,
			BypassURL:             "http://global-bypass:8191",
			BypassSession:         "shared-session",
			ProxyEnabled:          true,
			ProxyURL:              "http://image-proxy:8080",
			ProxySourceIDs:        []int64{exceptionID},
		}},
		throughput: &throughputStub{value: throughputSnapshot{
			Defaults: sourcethroughput.Effective{DownloadConcurrency: 3, ImageRequestDelay: 500 * time.Millisecond},
			Overrides: map[int64]sourcethroughput.Override{
				exceptionID: {DownloadConcurrency: intPtr(7), ImageRequestDelay: durationPtr(0)},
			},
		}},
		transport: &transportStub{value: transportSnapshot{
			DefaultImageConnectionMode: sourcetransport.ImageConnectionFresh,
			Overrides: map[int64]sourcetransport.Override{
				exceptionID:  {ReuseBypassSession: boolPtr(false), ImageConnectionMode: imageModePtr(sourcetransport.ImageConnectionReuse)},
				explicitOnID: {ReuseBypassSession: boolPtr(true)},
			},
		}},
		routing: &routingStub{value: routingSnapshot{
			Resolved: map[int64]network.ResolvedBinding{
				exceptionID: {
					SourceID:  exceptionID,
					Socks:     &network.ResolvedSocks{ID: socksID},
					FlareMode: network.FlareModeEndpoint,
					Flare:     &network.ResolvedFlare{ID: flareID, Session: "endpoint-session"},
				},
				blankEndpointID: {
					SourceID:  blankEndpointID,
					FlareMode: network.FlareModeEndpoint,
					Flare:     &network.ResolvedFlare{ID: blankFlareID, Session: ""},
				},
				noneID: {SourceID: noneID, FlareMode: network.FlareModeNone},
				explicitOnID: {
					SourceID:  explicitOnID,
					FlareMode: network.FlareModeEndpoint,
					Flare:     &network.ResolvedFlare{ID: "whitespace", Session: "   "},
				},
			},
			Stored: map[int64]network.BindingDTO{
				exceptionID:     {SourceID: fmt.Sprint(exceptionID), SocksEndpointID: &socksID, FlareMode: network.FlareModeEndpoint, FlareEndpointID: &flareID},
				blankEndpointID: {SourceID: fmt.Sprint(blankEndpointID), FlareMode: network.FlareModeEndpoint, FlareEndpointID: &blankFlareID},
				noneID:          {SourceID: fmt.Sprint(noneID), FlareMode: network.FlareModeNone},
			},
			EndpointNames: map[string]string{socksID: "VPN SOCKS", flareID: "Bypass EU", blankFlareID: "Blank session"},
		}},
		runtime: &runtimeStub{value: map[int64]sourcetransport.Intent{
			exceptionID: {SourceID: exceptionID, DesiredRevision: 5, AppliedRevision: 4, LastApplyAttempt: &attempt, LastApplyError: "fixed diagnostic"},
		}},
	}
	svc := newService(deps)

	inherited, err := svc.Get(context.Background(), inheritedID)
	if err != nil {
		t.Fatalf("Get inherited: %v", err)
	}
	if got, want := inherited.DownloadConcurrency, (IntegerPolicyValue{Effective: 3, Inherited: true}); !reflect.DeepEqual(got, want) {
		t.Errorf("inherited concurrency = %+v, want %+v", got, want)
	}
	if got, want := inherited.ImageRequestDelay, (DurationPolicyValue{Effective: 500 * time.Millisecond, Inherited: true}); !reflect.DeepEqual(got, want) {
		t.Errorf("inherited image delay = %+v, want %+v", got, want)
	}
	if !inherited.ReuseBypassSession.Effective || inherited.ReuseBypassSession.Mode != sourcetransport.BypassSessionReusable || !inherited.ReuseBypassSession.Inherited {
		t.Errorf("inherited reusable session = %+v", inherited.ReuseBypassSession)
	}
	if inherited.ProfileKey != "" || inherited.Routing.SocksMode != SocksModeGlobal || inherited.Routing.BypassMode != network.FlareModeGlobal {
		t.Errorf("inherited routing/profile = %+v / %q", inherited.Routing, inherited.ProfileKey)
	}
	if inherited.Runtime.Status != RuntimeApplied || inherited.Runtime.DesiredRevision != 0 || inherited.Runtime.AppliedRevision != 0 {
		t.Errorf("zero runtime = %+v", inherited.Runtime)
	}

	exception, err := svc.Get(context.Background(), exceptionID)
	if err != nil {
		t.Fatalf("Get exception: %v", err)
	}
	if got, want := exception.DownloadConcurrency, (IntegerPolicyValue{Override: intPtr(7), Effective: 7}); !reflect.DeepEqual(got, want) {
		t.Errorf("explicit concurrency = %+v, want %+v", got, want)
	}
	if got, want := exception.ImageRequestDelay, (DurationPolicyValue{Override: durationPtr(0), Effective: 0}); !reflect.DeepEqual(got, want) {
		t.Errorf("explicit zero image delay = %+v, want %+v", got, want)
	}
	if exception.ReuseBypassSession.Override == nil || *exception.ReuseBypassSession.Override || exception.ReuseBypassSession.Effective || exception.ReuseBypassSession.Mode != sourcetransport.BypassSessionDisposable {
		t.Errorf("explicit false session = %+v", exception.ReuseBypassSession)
	}
	if got, want := exception.ImageConnectionMode.Effective, sourcetransport.ImageConnectionReuse; got != want || exception.ImageConnectionMode.Inherited {
		t.Errorf("explicit image mode = %+v", exception.ImageConnectionMode)
	}
	if got, want := exception.ImageProxy, (ImageProxyState{OptedIn: true, GatewayEnabled: true, GatewayConfigured: true, EffectiveAvailable: true}); got != want {
		t.Errorf("image proxy = %+v, want %+v", got, want)
	}
	if exception.Routing.SocksMode != SocksModeEndpoint || exception.Routing.Socks.EndpointID == nil || *exception.Routing.Socks.EndpointID != socksID || exception.Routing.Socks.Name == nil || *exception.Routing.Socks.Name != "VPN SOCKS" {
		t.Errorf("SOCKS routing = %+v", exception.Routing)
	}
	if exception.Routing.BypassMode != network.FlareModeEndpoint || exception.Routing.Bypass.Name == nil || *exception.Routing.Bypass.Name != "Bypass EU" || !exception.BypassEnabled {
		t.Errorf("bypass routing = %+v, enabled=%t", exception.Routing, exception.BypassEnabled)
	}
	wantProfile := socksID + "|endpoint|" + flareID + "|session=disposable"
	if exception.ProfileKey != wantProfile {
		t.Errorf("profile key = %q, want %q", exception.ProfileKey, wantProfile)
	}
	if exception.Runtime.Status != RuntimePending || exception.Runtime.LastApplyAttempt != &attempt || exception.Runtime.LastApplyError != "fixed diagnostic" {
		t.Errorf("runtime = %+v", exception.Runtime)
	}
	if got, want := exception.Protection, (ProtectionConfiguration{15 * time.Minute, 1250, 4, 20 * time.Minute, 750 * time.Millisecond}); got != want {
		t.Errorf("protection = %+v, want %+v", got, want)
	}

	blank, err := svc.Get(context.Background(), blankEndpointID)
	if err != nil {
		t.Fatalf("Get blank endpoint: %v", err)
	}
	if blank.ReuseBypassSession.Effective || blank.ReuseBypassSession.Mode != sourcetransport.BypassSessionDisposable {
		t.Errorf("blank endpoint session = %+v", blank.ReuseBypassSession)
	}
	none, err := svc.Get(context.Background(), noneID)
	if err != nil {
		t.Fatalf("Get none: %v", err)
	}
	if none.ReuseBypassSession.Effective || none.ReuseBypassSession.Mode != sourcetransport.BypassSessionDisabled || none.BypassEnabled {
		t.Errorf("none bypass = session %+v enabled=%t", none.ReuseBypassSession, none.BypassEnabled)
	}
	explicitOn, err := svc.Get(context.Background(), explicitOnID)
	if err != nil {
		t.Fatalf("Get explicit on: %v", err)
	}
	if explicitOn.ReuseBypassSession.Override == nil || !*explicitOn.ReuseBypassSession.Override || !explicitOn.ReuseBypassSession.Effective || explicitOn.ReuseBypassSession.Mode != sourcetransport.BypassSessionReusable {
		t.Errorf("explicit on whitespace session = %+v", explicitOn.ReuseBypassSession)
	}
}

func TestImageProxyAvailabilityRequiresOptInEnabledAndConfiguredGateway(t *testing.T) {
	for _, tc := range []struct {
		name      string
		optedIn   bool
		enabled   bool
		url       string
		available bool
	}{
		{name: "available", optedIn: true, enabled: true, url: "http://proxy", available: true},
		{name: "not opted in", enabled: true, url: "http://proxy"},
		{name: "gateway disabled", optedIn: true, url: "http://proxy"},
		{name: "gateway not configured", optedIn: true, enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ids := []int64(nil)
			if tc.optedIn {
				ids = []int64{1}
			}
			got := compose(sourceengine.Source{ID: 1}, storesSnapshot{
				globals:    globalSnapshot{ProxySourceIDs: ids, ProxyEnabled: tc.enabled, ProxyURL: tc.url},
				throughput: throughputSnapshot{Overrides: map[int64]sourcethroughput.Override{}},
				transport:  transportSnapshot{Overrides: map[int64]sourcetransport.Override{}},
				routing:    routingSnapshot{Resolved: map[int64]network.ResolvedBinding{}},
				runtime:    map[int64]sourcetransport.Intent{},
			}, "").ImageProxy
			if got.OptedIn != tc.optedIn || got.GatewayEnabled != tc.enabled || got.GatewayConfigured != (tc.url != "") || got.EffectiveAvailable != tc.available {
				t.Fatalf("proxy state = %+v", got)
			}
		})
	}
}

func TestExceptionsUsesExactFieldLevelCount(t *testing.T) {
	socksID := "socks"
	flareID := "flare"
	deps := dependencies{
		catalog: &catalogStub{sources: []sourceengine.Source{{ID: 4, Name: "global only"}, {ID: 3, Name: "none"}, {ID: 1, Name: "one"}, {ID: 2, Name: "two"}}},
		globals: &globalsStub{value: globalSnapshot{BypassEnabled: true, BypassURL: "http://bypass", ProxyEnabled: true, ProxyURL: "http://proxy", ProxySourceIDs: []int64{2}}},
		throughput: &throughputStub{value: throughputSnapshot{Defaults: sourcethroughput.Effective{}, Overrides: map[int64]sourcethroughput.Override{
			1: {DownloadConcurrency: intPtr(0)},
			2: {DownloadConcurrency: intPtr(5), ImageRequestDelay: durationPtr(0)},
		}}},
		transport: &transportStub{value: transportSnapshot{DefaultImageConnectionMode: sourcetransport.ImageConnectionFresh, Overrides: map[int64]sourcetransport.Override{
			2: {ReuseBypassSession: boolPtr(false), ImageConnectionMode: imageModePtr(sourcetransport.ImageConnectionFresh)},
		}}},
		routing: &routingStub{value: routingSnapshot{
			Resolved: map[int64]network.ResolvedBinding{2: {SourceID: 2, Socks: &network.ResolvedSocks{ID: socksID}, FlareMode: network.FlareModeEndpoint, Flare: &network.ResolvedFlare{ID: flareID}}},
			Stored: map[int64]network.BindingDTO{
				2: {SourceID: "2", SocksEndpointID: &socksID, FlareMode: network.FlareModeEndpoint, FlareEndpointID: &flareID},
				3: {SourceID: "3", FlareMode: network.FlareModeNone},
			},
		}},
		runtime: &runtimeStub{value: map[int64]sourcetransport.Intent{}},
	}

	got, err := newService(deps).Exceptions(context.Background())
	if err != nil {
		t.Fatalf("Exceptions: %v", err)
	}
	want := []Summary{
		{Source: SourceIdentity{SourceID: 1, Name: "one"}, ExceptionCount: 1, Runtime: RuntimeStatus{Status: RuntimeApplied}},
		{Source: SourceIdentity{SourceID: 2, Name: "two"}, ExceptionCount: 7, Runtime: RuntimeStatus{Status: RuntimeApplied}},
		{Source: SourceIdentity{SourceID: 3, Name: "none"}, ExceptionCount: 1, Runtime: RuntimeStatus{Status: RuntimeApplied}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("summaries = %#v, want %#v", got, want)
	}
}

func TestExceptionsLoadsEachStoreOnceForManySources(t *testing.T) {
	catalog := &catalogStub{}
	for id := int64(1); id <= 100; id++ {
		catalog.sources = append(catalog.sources, sourceengine.Source{ID: id, Name: fmt.Sprintf("source-%03d", id)})
	}
	globals := &globalsStub{}
	throughput := &throughputStub{value: throughputSnapshot{Overrides: map[int64]sourcethroughput.Override{}}}
	transport := &transportStub{value: transportSnapshot{DefaultImageConnectionMode: sourcetransport.ImageConnectionFresh, Overrides: map[int64]sourcetransport.Override{}}}
	routing := &routingStub{value: routingSnapshot{Resolved: map[int64]network.ResolvedBinding{}, Stored: map[int64]network.BindingDTO{}}}
	runtime := &runtimeStub{value: map[int64]sourcetransport.Intent{}}

	got, err := newService(dependencies{catalog: catalog, globals: globals, throughput: throughput, transport: transport, routing: routing, runtime: runtime}).Exceptions(context.Background())
	if err != nil {
		t.Fatalf("Exceptions: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Exceptions len = %d, want 0", len(got))
	}
	if catalog.calls != 1 || globals.calls != 1 || throughput.calls != 1 || transport.calls != 1 || routing.calls != 1 || runtime.calls != 1 {
		t.Fatalf("bounded calls: catalog=%d globals=%d throughput=%d transport=%d routing=%d runtime=%d; want each exactly 1", catalog.calls, globals.calls, throughput.calls, transport.calls, routing.calls, runtime.calls)
	}
}

func TestGetClassifiesCatalogFailuresAndMissingSource(t *testing.T) {
	raw := errors.New("dial tcp secret-host")
	svc := newService(dependencies{catalog: &catalogStub{err: raw}})
	if _, err := svc.Get(context.Background(), 1); !errors.Is(err, ErrCatalogUnavailable) || !errors.Is(err, raw) {
		t.Fatalf("catalog error = %v, want classified raw cause", err)
	}

	svc = newService(dependencies{catalog: &catalogStub{sources: []sourceengine.Source{{ID: 2}}}})
	if _, err := svc.Get(context.Background(), 1); !errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("missing error = %v, want ErrSourceNotFound", err)
	}
}
