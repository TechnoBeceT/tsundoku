package enginetopo_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/enginetopo/apkcache"
	"github.com/technobecet/tsundoku/internal/ent"
	entharvestedextension "github.com/technobecet/tsundoku/internal/ent/harvestedextension"
	entharvestedrepo "github.com/technobecet/tsundoku/internal/ent/harvestedrepo"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// capturingWriter is a settings.SettingsStore-shaped test double that records
// the KeyValue batches it was asked to persist (ACCUMULATED across calls,
// because WriteThroughEngineConfig writes FlareSolverr and SOCKS as two
// separate batches), instead of touching a real DB.
//
// `owned` models the keys Tsundoku already has an explicit Settings row for:
// every key written via SetMany is added to it, so a re-write inside one test
// sees the previously-written keys as owned — WriteThroughEngineConfig is
// UNCONDITIONAL (unlike the retired boot-seed gap-fill), so this is here only
// to prove it overwrites already-owned keys, not to gate anything.
type capturingWriter struct {
	got   []settings.KeyValue
	calls int
	err   error
	errOn int             // when >0, fail SetMany only on the errOn-th call (1-based)
	owned map[string]bool // keys Tsundoku already owns (have an explicit row)
}

func (w *capturingWriter) SetMany(_ context.Context, updates []settings.KeyValue) error {
	w.calls++
	if w.err != nil && (w.errOn == 0 || w.errOn == w.calls) {
		return w.err
	}
	if w.owned == nil {
		w.owned = make(map[string]bool)
	}
	for _, kv := range updates {
		w.owned[kv.Key] = true
	}
	w.got = append(w.got, updates...)
	return nil
}

func (w *capturingWriter) value(key string) (string, bool) {
	for _, kv := range w.got {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return "", false
}

// TestOnExtensionInstalled proves the live write-through captures a just-installed
// extension into the durable store: the HarvestedExtension row exists and the
// .apk bytes are cached (via the SAME RecordInstalledExtension core the seed uses).
func TestOnExtensionInstalled(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	const repo = "https://repo.test/repo"
	apkBytes := []byte("APK-INSTALL")
	stub := &stubHTTP{routes: map[string]stubResp{
		"https://repo.test/repo/index.min.json": {status: 200, body: []byte(`[{"pkg":"pkg.one","apk":"one.apk","code":4}]`)},
		"https://repo.test/repo/apk/one.apk":    {status: 200, body: apkBytes},
	}}

	enginetopo.OnExtensionInstalled(ctx, db, cache, stub.get, installedExt("pkg.one", repo, 4, sourceengine.Source{ID: 7}))

	row := db.HarvestedExtension.Query().Where(entharvestedextension.PkgName("pkg.one")).OnlyX(ctx)
	assertCachedExtension(t, row, hexSHA(apkBytes), 4, []int64{7})
	assertCachedBytes(t, cache, "pkg.one", 4, apkBytes)
}

// TestOnExtensionUninstalled proves the write-through removes a just-uninstalled
// extension's row AND its cached apk, and that a SECOND call (already absent) is
// an idempotent no-op that touches nothing and never panics.
func TestOnExtensionUninstalled(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cache := apkcache.New(t.TempDir())

	const repo = "https://repo.test/repo"
	stub := &stubHTTP{routes: map[string]stubResp{
		"https://repo.test/repo/index.min.json": {status: 200, body: []byte(`[{"pkg":"pkg.one","apk":"one.apk","code":2}]`)},
		"https://repo.test/repo/apk/one.apk":    {status: 200, body: []byte("APK")},
	}}

	// Seed a real row + cached file via the capture path, then uninstall it.
	enginetopo.OnExtensionInstalled(ctx, db, cache, stub.get, installedExt("pkg.one", repo, 2, sourceengine.Source{ID: 1}))
	if !cache.Exists("pkg.one", 2) {
		t.Fatal("precondition: apk not cached before uninstall")
	}

	enginetopo.OnExtensionUninstalled(ctx, db, cache, "pkg.one")

	if ok, _ := db.HarvestedExtension.Query().Where(entharvestedextension.PkgName("pkg.one")).Exist(ctx); ok {
		t.Error("HarvestedExtension row still present after uninstall, want deleted")
	}
	if cache.Exists("pkg.one", 2) {
		t.Error("cached apk still present after uninstall, want removed")
	}

	// A second uninstall of the now-absent extension must be a clean no-op.
	enginetopo.OnExtensionUninstalled(ctx, db, cache, "pkg.one")
}

// TestOnReposSet proves the repo write-through is a FULL REPLACE: pre-seeded
// {A,B} becomes exactly {B,C} — A (no longer present) is deleted and C (new) is
// added.
func TestOnReposSet(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	const a = "https://a.test/repo"
	const b = "https://b.test/repo"
	const c = "https://c.test/repo"

	// Pre-seed {A,B}.
	enginetopo.OnReposSet(ctx, db, []string{a, b})
	assertRepoSet(t, db, []string{a, b})

	// Replace with {B,C}: A must be pruned, C added, B kept.
	enginetopo.OnReposSet(ctx, db, []string{b, c})
	assertRepoSet(t, db, []string{b, c})
}

// TestOnReposSet_EmptyClearsAll proves an empty replacement list clears every row.
func TestOnReposSet_EmptyClearsAll(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	enginetopo.OnReposSet(ctx, db, []string{"https://a.test/repo", "https://b.test/repo"})
	enginetopo.OnReposSet(ctx, db, nil)
	assertRepoSet(t, db, nil)
}

// assertRepoSet fails unless the HarvestedRepo table holds exactly want (order-
// independent).
func assertRepoSet(t *testing.T, db *ent.Client, want []string) {
	t.Helper()
	rows, err := db.HarvestedRepo.Query().Select(entharvestedrepo.FieldURL).Strings(context.Background())
	if err != nil {
		t.Fatalf("query repos: %v", err)
	}
	got := make(map[string]bool, len(rows))
	for _, u := range rows {
		got[u] = true
	}
	if len(got) != len(want) {
		t.Fatalf("repo set = %v, want %v", rows, want)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("repo %q missing from set %v, want present", w, rows)
		}
	}
}

// TestWriteThroughEngineConfig_UnconditionalWithSocks proves the config
// write-through is an UNCONDITIONAL capture (a plain SetMany, NOT gap-fill): even
// keys Tsundoku already owns are overwritten, both FlareSolverr and SOCKS keys are
// written when SOCKS is on, and the SOCKS username/password are NEVER written (not
// tunables — mirrors the seed's omission).
func TestWriteThroughEngineConfig_UnconditionalWithSocks(t *testing.T) {
	ctx := context.Background()
	// Mark every FlareSolverr key as already-owned: a gap-fill would skip them, so
	// seeing them written proves the write-through is unconditional.
	w := &capturingWriter{owned: map[string]bool{
		settings.KeyFlareSolverrEnabled:          true,
		settings.KeyFlareSolverrURL:              true,
		settings.KeyFlareSolverrTimeout:          true,
		settings.KeyFlareSolverrSessionName:      true,
		settings.KeyFlareSolverrSessionTTL:       true,
		settings.KeyFlareSolverrResponseFallback: true,
	}}

	const secretUser = "proxyuser"
	const secretPass = "proxypass"
	live := suwayomi.SuwayomiSettings{
		FlareSolverrEnabled: true,
		FlareSolverrURL:     "http://flaresolverr.internal:8191",
		FlareSolverrTimeout: 90,
		SocksProxyEnabled:   true,
		SocksProxyVersion:   5,
		SocksProxyHost:      "socks.internal",
		SocksProxyPort:      "1080",
		SocksProxyUsername:  secretUser,
		SocksProxyPassword:  secretPass,
	}

	enginetopo.WriteThroughEngineConfig(ctx, w, live)

	// FlareSolverr + SOCKS keys were all captured despite being "owned".
	for _, tc := range []struct{ key, want string }{
		{settings.KeyFlareSolverrURL, "http://flaresolverr.internal:8191"},
		{settings.KeyFlareSolverrEnabled, "true"},
		{settings.KeyEngineSocksEnabled, "true"},
		{settings.KeyEngineSocksHost, "socks.internal"},
		{settings.KeyEngineSocksPort, "1080"},
		{settings.KeyEngineSocksVersion, "5"},
	} {
		got, ok := w.value(tc.key)
		if !ok {
			t.Errorf("key %q not written, want captured unconditionally", tc.key)
			continue
		}
		if got != tc.want {
			t.Errorf("key %q = %q, want %q", tc.key, got, tc.want)
		}
	}

	// The SOCKS credentials must never appear in ANY written value.
	for _, kv := range w.got {
		if kv.Value == secretUser || kv.Value == secretPass {
			t.Errorf("SOCKS credential leaked into settings write under key %q", kv.Key)
		}
	}
}

// TestWriteThroughEngineConfig_SocksOffSkipsSocks proves that when SOCKS is off
// (disabled or blank port) only the FlareSolverr batch is written — the SOCKS keys
// are skipped entirely (nothing configured to capture).
func TestWriteThroughEngineConfig_SocksOffSkipsSocks(t *testing.T) {
	ctx := context.Background()
	w := &capturingWriter{}

	enginetopo.WriteThroughEngineConfig(ctx, w, suwayomi.SuwayomiSettings{
		FlareSolverrEnabled: true,
		FlareSolverrURL:     "http://fs.example:8191",
		SocksProxyEnabled:   false, // stock Suwayomi: SOCKS off
		SocksProxyPort:      "",
	})

	if _, ok := w.value(settings.KeyFlareSolverrURL); !ok {
		t.Error("FlareSolverr URL not captured, want written")
	}
	for _, k := range []string{
		settings.KeyEngineSocksEnabled,
		settings.KeyEngineSocksHost,
		settings.KeyEngineSocksPort,
		settings.KeyEngineSocksVersion,
	} {
		if _, ok := w.value(k); ok {
			t.Errorf("SOCKS key %q written, want skipped (SOCKS off)", k)
		}
	}
}
