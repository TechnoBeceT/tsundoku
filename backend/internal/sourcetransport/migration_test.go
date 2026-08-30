package sourcetransport_test

import (
	"context"
	"database/sql"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/config"
	"github.com/technobecet/tsundoku/internal/database"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/network"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcethroughput"
	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

const (
	boundSourceID   = int64(101)
	globalSourceID  = int64(303)
	socksEndpointID = "00000000-0000-0000-0000-000000000101"
	flareEndpointID = "00000000-0000-0000-0000-000000000102"
)

type migrationBehavior struct {
	sessionName      string
	proxyIDs         []int64
	boundThroughput  sourcethroughput.Effective
	globalThroughput sourcethroughput.Effective
	routing          []network.ResolvedBinding
}

type migrationTransportDefaults struct {
	sessions enginetopo.SessionPolicyResolver
}

func (migrationTransportDefaults) ImageConnectionMode(context.Context) sourcetransport.ImageConnectionMode {
	return sourcetransport.ImageConnectionFresh
}

func (d migrationTransportDefaults) ResolveBypassSession(
	ctx context.Context,
	sourceID int64,
	override *bool,
) (bool, sourcetransport.BypassSessionMode, error) {
	return d.sessions.ResolveBypassSession(ctx, sourceID, override)
}

func TestMigrationPreservesExistingBehavior(t *testing.T) {
	cases := []struct {
		name            string
		globalSession   string
		wantGlobalReuse bool
		wantGlobalMode  sourcetransport.BypassSessionMode
	}{
		{
			name:           "blank configured session stays disposable",
			globalSession:  "",
			wantGlobalMode: sourcetransport.BypassSessionDisposable,
		},
		{
			name:            "nonblank configured session stays reusable",
			globalSession:   "global-session",
			wantGlobalReuse: true,
			wantGlobalMode:  sourcetransport.BypassSessionReusable,
		},
		{
			name:            "whitespace configured session stays reusable exactly",
			globalSession:   "   ",
			wantGlobalReuse: true,
			wantGlobalMode:  sourcetransport.BypassSessionReusable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			legacyClient, legacySQL, cfg := createLegacyFixture(t, tc.globalSession)

			for _, table := range []string{
				"global_runtime_intents",
				"source_runtime_intents",
				"source_transport_policies",
			} {
				assertTableAbsent(t, legacySQL, table)
			}

			before := captureMigrationBehavior(t, legacyClient)
			assertRepresentativeLegacyBehavior(t, before, tc.globalSession)
			if err := legacyClient.Close(); err != nil {
				t.Fatalf("close legacy fixture client: %v", err)
			}

			client, err := database.Open(ctx, cfg)
			if err != nil {
				t.Fatalf("database.Open upgraded fixture: %v", err)
			}
			t.Cleanup(func() { _ = client.Close() })

			after := captureMigrationBehavior(t, client)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("effective behavior changed across migration:\n before: %#v\n  after: %#v", before, after)
			}

			networkService := network.NewService(client)
			settingService := settings.NewService(client, migrationSettingDefaults())
			transportService := sourcetransport.NewService(client, migrationTransportDefaults{
				sessions: enginetopo.NewSessionPolicyResolver(networkService, settingService),
			}, nil)

			bound, err := transportService.Resolve(ctx, boundSourceID)
			if err != nil {
				t.Fatalf("Resolve bound source: %v", err)
			}
			if !bound.ReuseBypassSession || bound.BypassSessionMode != sourcetransport.BypassSessionReusable ||
				bound.ImageConnectionMode != sourcetransport.ImageConnectionFresh {
				t.Fatalf("bound source transport = %+v, want inherited endpoint session reusable with Fresh images", bound)
			}

			global, err := transportService.Resolve(ctx, globalSourceID)
			if err != nil {
				t.Fatalf("Resolve global source: %v", err)
			}
			if global.ReuseBypassSession != tc.wantGlobalReuse || global.BypassSessionMode != tc.wantGlobalMode ||
				global.ImageConnectionMode != sourcetransport.ImageConnectionFresh {
				t.Fatalf("global source transport = %+v, want reuse=%t mode=%q and Fresh images",
					global, tc.wantGlobalReuse, tc.wantGlobalMode)
			}

			if got := client.SourceTransportPolicy.Query().CountX(ctx); got != 0 {
				t.Fatalf("source transport policies after migration = %d, want zero", got)
			}
			if got := client.SourceRuntimeIntent.Query().CountX(ctx); got != 0 {
				t.Fatalf("source runtime intents after migration = %d, want zero", got)
			}
			if tc.globalSession == "   " {
				if got := settingService.FlareSolverrSessionName(ctx); got != tc.globalSession {
					t.Fatalf("post-migration session = %q, want byte-exact whitespace", got)
				}
				if _, err := client.SourceTransportPolicy.Create().SetSourceID(globalSourceID).
					SetReuseBypassSession(true).Save(ctx); err != nil {
					t.Fatalf("create explicit On policy: %v", err)
				}
				explicit, err := transportService.Resolve(ctx, globalSourceID)
				if err != nil {
					t.Fatalf("Resolve explicit On whitespace session: %v", err)
				}
				if !explicit.ReuseBypassSession || explicit.BypassSessionMode != sourcetransport.BypassSessionReusable {
					t.Fatalf("explicit On whitespace transport = %+v, want reusable", explicit)
				}
			}

			intents := client.GlobalRuntimeIntent.Query().AllX(ctx)
			if len(intents) != 1 || intents[0].Scope != "engine_config" ||
				intents[0].DesiredRevision != 1 || intents[0].AppliedRevision != 0 ||
				intents[0].LastApplyAttempt != nil || intents[0].LastApplyError != "" {
				t.Fatalf("global runtime intent after migration = %+v, want one pending revision for the pre-existing runtime settings", intents)
			}
		})
	}

	t.Run("global intent seed remains conditional", func(t *testing.T) {
		ctx := context.Background()
		legacyClient, legacySQL, cfg := createLegacySchema(t)
		if _, err := legacySQL.ExecContext(ctx,
			`INSERT INTO settings (id, key, value, updated_at) VALUES ($1, $2, $3, NOW())`,
			"00000000-0000-0000-0000-000000000021", settings.KeyStaleGraceDays, "14"); err != nil {
			t.Fatalf("seed non-runtime legacy setting: %v", err)
		}
		if err := legacyClient.Close(); err != nil {
			t.Fatalf("close non-runtime legacy fixture client: %v", err)
		}

		client, err := database.Open(ctx, cfg)
		if err != nil {
			t.Fatalf("database.Open non-runtime fixture: %v", err)
		}
		t.Cleanup(func() { _ = client.Close() })
		if got := client.GlobalRuntimeIntent.Query().CountX(ctx); got != 0 {
			t.Fatalf("global runtime intents without pre-existing runtime settings = %d, want zero", got)
		}
		if got := client.SourceTransportPolicy.Query().CountX(ctx); got != 0 {
			t.Fatalf("source transport policies in non-runtime control = %d, want zero", got)
		}
		if got := client.SourceRuntimeIntent.Query().CountX(ctx); got != 0 {
			t.Fatalf("source runtime intents in non-runtime control = %d, want zero", got)
		}
	})
}

func captureMigrationBehavior(t *testing.T, client *ent.Client) migrationBehavior {
	t.Helper()
	ctx := context.Background()
	settingService := settings.NewService(client, migrationSettingDefaults())
	throughputService := sourcethroughput.NewService(client, settingService)
	routingService := network.NewService(client)

	boundThroughput, err := throughputService.Resolve(ctx, boundSourceID)
	if err != nil {
		t.Fatalf("resolve bound throughput: %v", err)
	}
	globalThroughput, err := throughputService.Resolve(ctx, globalSourceID)
	if err != nil {
		t.Fatalf("resolve inherited throughput: %v", err)
	}
	routing, err := routingService.RoutingSnapshot(ctx)
	if err != nil {
		t.Fatalf("capture routing snapshot: %v", err)
	}
	return migrationBehavior{
		sessionName:      settingService.FlareSolverrSessionName(ctx),
		proxyIDs:         settingService.ImpersonateSources(ctx),
		boundThroughput:  boundThroughput,
		globalThroughput: globalThroughput,
		routing:          routing,
	}
}

func assertRepresentativeLegacyBehavior(t *testing.T, got migrationBehavior, wantSession string) {
	t.Helper()
	if got.sessionName != wantSession {
		t.Fatalf("legacy configured session = %q, want %q", got.sessionName, wantSession)
	}
	if !slices.Equal(got.proxyIDs, []int64{boundSourceID, globalSourceID}) {
		t.Fatalf("legacy proxy membership = %v, want [%d %d]", got.proxyIDs, boundSourceID, globalSourceID)
	}
	if got.boundThroughput.DownloadConcurrency != 3 || got.boundThroughput.ImageRequestDelay != 250*time.Millisecond {
		t.Fatalf("legacy bound throughput = %+v, want concurrency 3 and delay 250ms", got.boundThroughput)
	}
	if got.globalThroughput.DownloadConcurrency != 6 || got.globalThroughput.ImageRequestDelay != 125*time.Millisecond {
		t.Fatalf("legacy inherited throughput = %+v, want concurrency 6 and delay 125ms", got.globalThroughput)
	}
	if len(got.routing) != 1 {
		t.Fatalf("legacy routing entries = %d, want 1", len(got.routing))
	}
	route := got.routing[0]
	if route.SourceID != boundSourceID || route.FlareMode != network.FlareModeEndpoint || route.Socks == nil || route.Flare == nil {
		t.Fatalf("legacy routing = %+v, want source %d with SOCKS and endpoint FlareSolverr", route, boundSourceID)
	}
	if route.Socks.ID != socksEndpointID || route.Socks.Host != "proxy.internal" || route.Socks.Port != 1081 ||
		route.Socks.Version != 5 || route.Socks.Username != "reader" || route.Socks.Password != "secret" {
		t.Fatalf("legacy SOCKS route = %+v, want exact stored endpoint", route.Socks)
	}
	if route.Flare.ID != flareEndpointID || route.Flare.URL != "http://flare.internal:8191" ||
		route.Flare.Session != "endpoint-session" || route.Flare.SessionTTL != 30 || route.Flare.Timeout != 90 ||
		!route.Flare.AsResponseFallback {
		t.Fatalf("legacy FlareSolverr route = %+v, want exact stored endpoint", route.Flare)
	}
}

func migrationSettingDefaults() settings.Defaults {
	return settings.Defaults{
		DownloadConcurrency:      2,
		SourcesImageRequestDelay: 50 * time.Millisecond,
	}
}

func createLegacyFixture(t *testing.T, globalSession string) (*ent.Client, *sql.DB, config.DatabaseConfig) {
	t.Helper()
	ctx := context.Background()
	client, db, cfg := createLegacySchema(t)

	settingsRows := []struct{ id, key, value string }{
		{"00000000-0000-0000-0000-000000000001", settings.KeyFlareSolverrSessionName, globalSession},
		{"00000000-0000-0000-0000-000000000002", settings.KeyDownloadConcurrency, "6"},
		{"00000000-0000-0000-0000-000000000003", settings.KeySourcesImageRequestDelay, "125ms"},
		{"00000000-0000-0000-0000-000000000004", settings.KeyImpersonateEnabled, "true"},
		{"00000000-0000-0000-0000-000000000005", settings.KeyImpersonateURL, "http://impersonate.internal:8788"},
		{"00000000-0000-0000-0000-000000000006", settings.KeyImpersonateSources, "101,303"},
	}
	for _, row := range settingsRows {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO settings (id, key, value, updated_at) VALUES ($1, $2, $3, NOW())`,
			row.id, row.key, row.value); err != nil {
			_ = db.Close()
			t.Fatalf("seed legacy setting %s: %v", row.key, err)
		}
	}
	sourceRows := []struct {
		name  string
		query string
		args  []any
	}{
		{
			name: "throughput override",
			query: `INSERT INTO source_throughput_policies
				(id, source_id, download_concurrency, image_request_delay_ms, created_at, updated_at)
				VALUES ('00000000-0000-0000-0000-000000000011', $1, 3, 250, NOW(), NOW())`,
			args: []any{boundSourceID},
		},
		{
			name: "network endpoints",
			query: `INSERT INTO network_endpoints
				(id, name, kind, enabled, host, port, socks_version, username, password,
				 url, session, session_ttl, timeout, as_response_fallback, created_at, updated_at)
				VALUES
				($1, 'migration socks', 'socks', TRUE, 'proxy.internal', 1081, 5, 'reader', 'secret',
				 '', '', 0, 60, TRUE, NOW(), NOW()),
				($2, 'migration flare', 'flaresolverr', TRUE, '', 0, 5, '', '',
				 'http://flare.internal:8191', 'endpoint-session', 30, 90, TRUE, NOW(), NOW())`,
			args: []any{socksEndpointID, flareEndpointID},
		},
		{
			name: "network binding",
			query: `INSERT INTO source_network_bindings
				(id, source_id, socks_endpoint_id, flare_mode, flare_endpoint_id, created_at, updated_at)
				VALUES ('00000000-0000-0000-0000-000000000012', $1, $2, 'endpoint', $3, NOW(), NOW())`,
			args: []any{boundSourceID, socksEndpointID, flareEndpointID},
		},
	}
	for _, row := range sourceRows {
		if _, err := db.ExecContext(ctx, row.query, row.args...); err != nil {
			_ = db.Close()
			t.Fatalf("seed legacy %s: %v", row.name, err)
		}
	}

	return client, db, cfg
}

func createLegacySchema(t *testing.T) (*ent.Client, *sql.DB, config.DatabaseConfig) {
	t.Helper()
	client, db, cfg := testdb.NewUnmigrated(t)
	if _, err := db.ExecContext(context.Background(), legacySchema); err != nil {
		t.Fatalf("create legacy fixture schema: %v", err)
	}
	return client, db, cfg
}

func assertTableAbsent(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	var found sql.NullString
	if err := db.QueryRowContext(context.Background(), `SELECT to_regclass($1)`, table).Scan(&found); err != nil {
		t.Fatalf("inspect pre-migration table %s: %v", table, err)
	}
	if found.Valid {
		t.Fatalf("pre-migration table %s unexpectedly exists as %s", table, found.String)
	}
}

const legacySchema = `
CREATE TABLE settings (
	id uuid PRIMARY KEY,
	key varchar NOT NULL UNIQUE,
	value varchar NOT NULL DEFAULT '',
	updated_at timestamptz NOT NULL
);
CREATE TABLE source_throughput_policies (
	id uuid PRIMARY KEY,
	source_id bigint NOT NULL UNIQUE,
	download_concurrency bigint NULL,
	image_request_delay_ms bigint NULL,
	created_at timestamptz NOT NULL,
	updated_at timestamptz NOT NULL
);
CREATE TABLE network_endpoints (
	id uuid PRIMARY KEY,
	name varchar NOT NULL,
	kind varchar NOT NULL,
	enabled boolean NOT NULL DEFAULT TRUE,
	host varchar NOT NULL DEFAULT '',
	port bigint NOT NULL DEFAULT 0,
	socks_version bigint NOT NULL DEFAULT 5,
	username varchar NOT NULL DEFAULT '',
	password varchar NOT NULL DEFAULT '',
	url varchar NOT NULL DEFAULT '',
	session varchar NOT NULL DEFAULT '',
	session_ttl bigint NOT NULL DEFAULT 0,
	timeout bigint NOT NULL DEFAULT 60,
	as_response_fallback boolean NOT NULL DEFAULT TRUE,
	created_at timestamptz NOT NULL,
	updated_at timestamptz NOT NULL
);
CREATE TABLE source_network_bindings (
	id uuid PRIMARY KEY,
	source_id bigint NOT NULL UNIQUE,
	socks_endpoint_id uuid NULL,
	flare_mode varchar NOT NULL DEFAULT 'global',
	flare_endpoint_id uuid NULL,
	created_at timestamptz NOT NULL,
	updated_at timestamptz NOT NULL
);
`
