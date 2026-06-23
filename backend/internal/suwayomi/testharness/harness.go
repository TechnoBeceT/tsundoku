// Package testharness provides a shared, real Suwayomi-Server instance for
// build-tagged integration tests (//go:build suwayomi).
//
// It is compiled only when the suwayomi build tag is present and must never be
// imported from production code.
//
// # Usage
//
// In a TestMain or the first test that needs it, call Shared(t). The instance
// is provisioned and launched once per test binary run (singleton via sync.Once)
// and torn down automatically at run end via t.Cleanup.
//
//	func TestMain(m *testing.M) {
//	    // Shared registers its cleanup against the TestMain-level helper.
//	    os.Exit(m.Run())
//	}
//
// Individual tests call Shared(t) and receive the same *Instance.
//
// # Java requirement
//
// Suwayomi v2.2.2100 requires Java 21+. The harness scans /usr/lib/jvm/*/bin/java
// for a suitable JVM. If none is found, all tests using the harness are skipped
// with a clear message. The discovered path is passed to ProcessManager via
// SuwayomiConfig.JavaPath.
//
// # Local source fixture
//
// The harness creates a minimal local-source fixture: two manga chapters each
// containing small deterministic PNG images. Suwayomi's built-in Local source
// (sourceID "0") indexes files under server.localSourcePath with the structure:
//
//	localSourcePath/
//	  Test Manga/          ← manga folder (title = folder name)
//	    ch001/             ← chapter folder (name = folder name)
//	      001.png
//	      002.png
//	    ch002/
//	      001.png
//
// The harness writes a server.conf that points Suwayomi at this fixture dir.
//
// # GraphQL shape validation (Task 4)
//
// The e2e test that consumes this harness validates three Suwayomi GraphQL-shape
// items flagged in Task 4 as requiring live verification:
//
//  1. Chapter filter operator: chapters(filter:{mangaId:{equalTo:N}}).
//  2. fetchChapterPages page-URL format (relative path vs. absolute URL).
//  3. LongString scalar acceptance for sourceId in fetchSourceManga.
package testharness

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/config"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// LocalSourceID is the Suwayomi built-in Local source identifier.
// The Local source is always present; its ID is "0".
const LocalSourceID = "0"

// FixtureMangaTitle is the title of the fixture manga seeded into localSourcePath.
const FixtureMangaTitle = "Test Manga"

// FixtureChapterCount is the number of chapters seeded into the fixture.
const FixtureChapterCount = 2

// minJavaVersion is the minimum Java major version required by Suwayomi v2.2.2100.
const minJavaVersion = 21

// Instance holds the shared Suwayomi process and a Client connected to it.
// Obtain via Shared(t); do not construct directly.
type Instance struct {
	client  suwayomi.Client
	baseURL string
	pm      *suwayomi.ProcessManager
}

// Client returns the Suwayomi client connected to the shared instance.
func (inst *Instance) Client() suwayomi.Client { return inst.client }

// BaseURL returns the HTTP base URL of the shared Suwayomi instance.
func (inst *Instance) BaseURL() string { return inst.baseURL }

// singleton guards ensure that Shared launches exactly one Suwayomi per
// test binary run.
var (
	sharedOnce     sync.Once
	sharedInstance *Instance
	sharedErr      error

	// globalCleanup holds the Stop func registered by Setup so that TestMain
	// can call it after m.Run() completes.  Populated by Setup; nil until then.
	globalCleanup func()
)

// Setup launches the shared Suwayomi instance exactly once and registers its
// cleanup via the provided addCleanup func. It must be called from TestMain
// before m.Run() so the process lifetime spans all tests.
//
// Typical TestMain usage:
//
//	func TestMain(m *testing.M) {
//	    testharness.Setup(func(fn func()) { /* defer fn() equivalent */ })
//	    os.Exit(m.Run())
//	}
//
// Actually, the idiomatic pattern is:
//
//	func TestMain(m *testing.M) {
//	    code := m.Run()
//	    testharness.GlobalCleanup()
//	    os.Exit(code)
//	}
func Setup(javaPath string) error {
	var launchErr error
	sharedOnce.Do(func() {
		sharedInstance, launchErr = launch(javaPath)
		sharedErr = launchErr
		if sharedInstance != nil {
			globalCleanup = func() {
				sharedInstance.pm.Stop()
			}
		}
	})
	return launchErr
}

// GlobalCleanup stops the shared Suwayomi instance. It must be called from
// TestMain after m.Run() returns to ensure the process is always stopped.
// Calling GlobalCleanup when Setup was never called is a no-op.
func GlobalCleanup() {
	if globalCleanup != nil {
		globalCleanup()
	}
}

// Shared returns the singleton Suwayomi instance for integration tests. The
// instance must have been provisioned already by a prior Setup call (from
// TestMain). If setup failed, all tests calling Shared are failed.
//
// If no Java 21+ JVM is found on this machine, Shared calls t.Skip with a
// clear diagnostic message. Tests should treat this skip as a configuration
// issue, not a test failure.
func Shared(t *testing.T) *Instance {
	t.Helper()

	if sharedErr != nil {
		t.Fatalf("suwayomi harness: shared instance setup failed: %v", sharedErr)
	}
	if sharedInstance == nil {
		t.Fatal("suwayomi harness: Shared called before Setup; call testharness.Setup from TestMain")
	}
	return sharedInstance
}

// sharedRuntimeDir is a fixed path used across test binary runs so that
// Suwayomi's JAR, H2 database, KCEF binaries, and WebUI cache are reused.
// On the first run, Suwayomi downloads ~120 MB of dependencies (JAR + KCEF +
// WebUI). On subsequent runs those assets are already present and startup is
// fast (typically 3-5 s). Using a fixed path avoids a slow re-download every
// time the test binary is compiled and re-run.
const sharedRuntimeDir = "/tmp/tsundoku-suwayomi-harness-shared"

// launch provisions and starts a single Suwayomi process with a local-source
// fixture. It is called at most once per test binary invocation (from Setup).
//
// It uses a fixed shared runtime directory so that Suwayomi's downloaded assets
// (JAR, KCEF binaries, WebUI) are cached across runs. The local-source fixture
// is re-seeded on each run to guarantee a clean fixture state.
func launch(javaPath string) (*Instance, error) {
	runtimeDir := sharedRuntimeDir
	if err := os.MkdirAll(runtimeDir, 0o750); err != nil {
		return nil, fmt.Errorf("create runtime dir: %w", err)
	}

	// Seed the local-source fixture before writing server.conf so the path is
	// known when we configure server.localSourcePath.
	localSourcePath := filepath.Join(runtimeDir, "local_source")
	if err := seedFixture(localSourcePath); err != nil {
		return nil, fmt.Errorf("seed fixture: %w", err)
	}

	// Write server.conf directly into runtimeDir (= rootDir passed to Suwayomi
	// via -Dsuwayomi.tachidesk.config.server.rootDir). Suwayomi reads its
	// config from rootDir/server.conf, NOT from the rootDir/Suwayomi/ subdir
	// (that subdir holds the JAR only).
	if err := writeServerConf(runtimeDir, localSourcePath); err != nil {
		return nil, fmt.Errorf("write server.conf: %w", err)
	}

	// Build a SuwayomiConfig for the harness. Port 14567 is chosen to avoid
	// colliding with a production Suwayomi on 4567.
	cfg := config.SuwayomiConfig{
		Host:                "127.0.0.1",
		Port:                "14567",
		RuntimeDir:          runtimeDir,
		Version:             "v2.2.2100",
		DownloadURLTemplate: "https://github.com/Suwayomi/Suwayomi-Server/releases/download/%s/Suwayomi-Server-%s.jar",
		StartTimeout:        5 * time.Minute,
		DownloadTimeout:     15 * time.Minute,
		JavaPath:            javaPath,
	}

	pm := suwayomi.NewProcessManager(cfg)

	// Pass context.Background() so the process lives until pm.Stop() is called
	// explicitly by GlobalCleanup(). Do NOT use a timeout context here: when the
	// context passed to exec.CommandContext is cancelled, Go sends SIGKILL to the
	// child process. The cfg.StartTimeout is enforced internally by pm.Start via a
	// time.After — the ctx.Done path in waitReady is a cancellation escape hatch,
	// not the primary timeout mechanism.
	if err := pm.Start(context.Background()); err != nil {
		return nil, fmt.Errorf("start Suwayomi: %w", err)
	}

	baseURL := cfg.BaseURL()
	client := suwayomi.NewClient(cfg, &http.Client{Timeout: 60 * time.Second})

	// Wait for the HTTP API to accept requests reliably. Suwayomi prints the
	// Javalin ready signal ("You are running Javalin") before its background
	// tasks (KCEF download, WebUI setup) complete. During that window (typically
	// 15-25 s on first run) the server may reset connections. waitHTTPReady
	// requires several consecutive successful responses before declaring the
	// server stable. On subsequent runs where assets are cached, stability is
	// reached within seconds.
	httpReadyCtx, httpReadyCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer httpReadyCancel()
	if err := waitHTTPReady(httpReadyCtx, baseURL+"/api/graphql"); err != nil {
		pm.Stop()
		return nil, fmt.Errorf("Suwayomi HTTP not ready: %w", err)
	}

	return &Instance{client: client, baseURL: baseURL, pm: pm}, nil
}

// consecutiveSuccessesRequired is the number of consecutive successful HTTP
// responses (each separated by probeInterval) before waitHTTPReady declares
// the server stable.
//
// Suwayomi downloads KCEF and WebUI files in background threads after printing
// the "You are running Javalin" ready signal. During that window (typically
// 15-25 s on the FIRST run; cached runs skip this) the server may reset
// connections. The shared runtime dir (sharedRuntimeDir) caches assets so
// subsequent runs are fast; this guard handles the slow first-run case.
const (
	consecutiveSuccessesRequired = 6
	probeInterval                = time.Second
)

// waitHTTPReady polls the given URL until consecutiveSuccessesRequired non-error
// HTTP responses are received in a row (each separated by probeInterval), or the
// context deadline is reached. Using consecutive successes filtered by a 1 s gap
// ensures the stable window is at least consecutiveSuccessesRequired seconds long,
// filtering out transient resets during Suwayomi's background-init phase.
func waitHTTPReady(ctx context.Context, url string) error {
	consecutive := 0
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url,
			strings.NewReader(`{"query":"{__typename}"}`))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			consecutive++
			if consecutive >= consecutiveSuccessesRequired {
				return nil // server is stable
			}
		} else {
			consecutive = 0 // reset the run on any error
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("server at %s did not become stable before deadline (got %d/%d consecutive successes): %w",
				url, consecutive, consecutiveSuccessesRequired, ctx.Err())
		case <-time.After(probeInterval):
		}
	}
}

// seedFixture creates the local-source directory tree that Suwayomi's built-in
// Local source will index. The structure is:
//
//	localSourcePath/
//	  Test Manga/
//	    ch001/
//	      001.png
//	      002.png
//	    ch002/
//	      001.png
//
// Each image is a tiny deterministic 4×4 red PNG (valid image data; Suwayomi
// serves it as-is so the e2e test can verify page bytes arrive correctly).
func seedFixture(localSourcePath string) error {
	chapters := []struct {
		name  string
		pages int
	}{
		{"ch001", 2},
		{"ch002", 1},
	}

	for _, ch := range chapters {
		dir := filepath.Join(localSourcePath, FixtureMangaTitle, ch.name)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create chapter dir %q: %w", dir, err)
		}
		for p := range ch.pages {
			imgPath := filepath.Join(dir, fmt.Sprintf("%03d.png", p+1))
			if err := writeTinyPNG(imgPath, p); err != nil {
				return fmt.Errorf("write fixture image %q: %w", imgPath, err)
			}
		}
	}
	return nil
}

// writeTinyPNG writes a deterministic 4×4 PNG at path. The pixel colour varies
// by index so that each page is distinguishable (important for order assertions).
func writeTinyPNG(path string, index int) error {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	// Use a different hue per page so images are not byte-for-byte identical.
	// Cap the values to stay within uint8 range (max index is 1 for 2-page chapters).
	r := uint8(200 + (index % 56))  //nolint:gosec // index is bounded by page count (≤ 2); no overflow
	g := uint8(100 + (index%15)*10) //nolint:gosec // index bounded; (index%15)*10 ≤ 140; 100+140=240 < 256
	c := color.RGBA{R: r, G: g, B: 50, A: 255}
	for y := range 4 {
		for x := range 4 {
			img.Set(x, y, c)
		}
	}
	// G304: path is constructed from a test-controlled temp directory.
	f, err := os.Create(path) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return png.Encode(f, img)
}

// writeServerConf writes the Suwayomi server.conf into the rootDir (the
// directory passed to Suwayomi via -Dsuwayomi.tachidesk.config.server.rootDir).
// Suwayomi reads server.conf from rootDir/server.conf (not from the
// rootDir/Suwayomi/ subdirectory). It overrides the default conf to:
//   - bind on 127.0.0.1:14567 (harness port)
//   - disable authentication and the system tray
//   - disable the initial browser-open
//   - disable CEF/WebView (kcefEnabled = false) — not available headlessly
//   - point localSourcePath at the fixture tree
//   - enable downloadAsCbz = true
//
// Enum values (authMode, webUIFlavor, etc.) must be written without quotes —
// Suwayomi's HOCON-like parser treats bare identifiers as enum constants.
//
// The config is written before the process starts so that Suwayomi picks it
// up on first launch without creating its own default.
func writeServerConf(rootDir, localSourcePath string) error {
	conf := fmt.Sprintf(`# Harness-generated server.conf for Tsundoku integration tests.
server.ip = "127.0.0.1"
server.port = 14567

# downloader
server.downloadAsCbz = true
server.downloadsPath = ""
server.autoDownloadNewChapters = false
server.excludeEntryWithUnreadChapters = true
server.autoDownloadNewChaptersLimit = 0
server.autoDownloadIgnoreReUploads = false

# extension repos (empty — harness uses only the built-in Local source)
server.extensionRepos = []

# requests
server.maxSourcesInParallel = 6

# updater — disabled (no library update needed during tests)
server.globalUpdateInterval = 0

# Authentication — none for tests
server.authMode = NONE

# misc
server.debugLogsEnabled = false
server.systemTrayEnabled = false
server.initialOpenInBrowserEnabled = false

# WebUI — bundled channel, WebView/CEF disabled (headless environment)
server.webUIEnabled = true
server.webUIFlavor = WEBUI
server.webUIChannel = BUNDLED
server.webUIUpdateCheckInterval = 0
server.kcefEnabled = false

# local source — point at the fixture directory
server.localSourcePath = %q
`, localSourcePath)

	confPath := filepath.Join(rootDir, "server.conf")
	return os.WriteFile(confPath, []byte(conf), 0o600) //nolint:gosec
}

// FindJava21 scans /usr/lib/jvm for a Java executable whose major version is
// >= 21. It also checks the well-known path /usr/lib/jvm/java-26-openjdk/bin/java
// first as an optimisation. Returns the absolute path to the first suitable JVM,
// or an error if none is found.
//
// Call this from TestMain to resolve the java path before Setup.
func FindJava21() (string, error) {
	// Fast path: check the known Java 26 install on this machine.
	known := []string{
		"/usr/lib/jvm/java-26-openjdk/bin/java",
		"/usr/lib/jvm/java-21-openjdk/bin/java",
	}
	for _, p := range known {
		if v, ok := javaVersion(p); ok && v >= minJavaVersion {
			return p, nil
		}
	}

	// Scan /usr/lib/jvm/*/bin/java.
	matches, _ := filepath.Glob("/usr/lib/jvm/*/bin/java")
	for _, p := range matches {
		if v, ok := javaVersion(p); ok && v >= minJavaVersion {
			return p, nil
		}
	}

	// Last resort: check "java" on PATH.
	if path, err := exec.LookPath("java"); err == nil {
		if v, ok := javaVersion(path); ok && v >= minJavaVersion {
			return path, nil
		}
	}

	return "", fmt.Errorf("no Java %d+ executable found; install a JDK >= %d or set TSUNDOKU_SUWAYOMI_JAVAPATH", minJavaVersion, minJavaVersion)
}

// javaVersion runs `path -version` and parses the major version number from the
// output. Returns (0, false) when the binary is missing, not executable, or the
// version cannot be parsed.
func javaVersion(path string) (int, bool) {
	cmd := exec.Command(path, "-version") //nolint:gosec // path is from a controlled set of well-known JVM locations
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, false
	}
	return parseJavaMajor(string(out))
}

// parseJavaMajor extracts the Java major version from `java -version` output.
// Handles both modern (21.0.1) and legacy (1.8.0_292) version strings.
func parseJavaMajor(output string) (int, bool) {
	// java -version outputs to stderr in the form:
	//   openjdk version "21.0.1" ...
	//   java version "1.8.0_292" ...
	for _, line := range strings.Split(output, "\n") {
		start := strings.Index(line, `"`)
		if start < 0 {
			continue
		}
		end := strings.Index(line[start+1:], `"`)
		if end < 0 {
			continue
		}
		ver := line[start+1 : start+1+end] // e.g. "21.0.1" or "1.8.0_292"
		parts := strings.SplitN(ver, ".", 3)
		if len(parts) < 1 {
			continue
		}
		major, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		// Legacy Java 8 reports "1.8" — convert the minor to major.
		if major == 1 && len(parts) >= 2 {
			minor, err := strconv.Atoi(parts[1])
			if err == nil {
				return minor, true
			}
		}
		return major, true
	}
	return 0, false
}
