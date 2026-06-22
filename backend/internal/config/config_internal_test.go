// Package config — internal (white-box) tests for helpers that are not
// accessible from the black-box package config_test.
package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestIsNotExistNil confirms that isNotExist(nil) returns false so that
// the happy-path yaml load never falls into the "skip" branch.
func TestIsNotExistNil(t *testing.T) {
	if isNotExist(nil) {
		t.Fatal("isNotExist(nil) should be false")
	}
}

// TestIsNotExistUnknownError confirms that a generic error is not treated as
// a "file not found" so that real yaml parse errors surface correctly.
func TestIsNotExistUnknownError(t *testing.T) {
	err := errors.New("some other error")
	if isNotExist(err) {
		t.Fatal("isNotExist(non-fs-error) should be false")
	}
}

// TestIsNotExistFileMissing confirms that a real os.ErrNotExist-wrapped error
// is correctly identified so that a missing config.yaml is silently skipped.
// Uses fs.PathError (the concrete type os.Open returns) rather than
// string-matching so the test stays honest about what errors.Is checks.
func TestIsNotExistFileMissing(t *testing.T) {
	err := &fs.PathError{Op: "open", Path: "config.yaml", Err: os.ErrNotExist}
	if !isNotExist(err) {
		t.Fatal("isNotExist(fs.PathError wrapping os.ErrNotExist) should be true")
	}
}

// TestLoadRejectsMalformedYAML confirms that Load() returns an error when a
// config.yaml exists but contains invalid YAML — the non-not-exist yaml parse
// error path in Load() (line 128-130).
//
// This test changes the working directory so must not be run in parallel with
// other tests that depend on the cwd.
func TestLoadRejectsMalformedYAML(t *testing.T) {
	dir := t.TempDir()

	// Write a deliberately broken YAML file.
	bad := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(bad, []byte(":\tinvalid: yaml: {\n"), 0o600); err != nil {
		t.Fatalf("write bad yaml: %v", err)
	}

	// Switch cwd to the temp dir so Load() picks up the bad config.yaml.
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})

	// Required secrets — but we expect a parse error before validate() is reached.
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "pw")
	t.Setenv("TSUNDOKU_AUTH_SECRET", "supersecretpassword1234")

	_, err = Load()
	if err == nil {
		t.Fatal("expected error for malformed config.yaml, got nil")
	}
}
