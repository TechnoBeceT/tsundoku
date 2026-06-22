// Package config — internal (white-box) tests for helpers that are not
// accessible from the black-box package config_test.
package config

import (
	"errors"
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
	err := errors.New("yaml: line 1: unexpected key")
	if isNotExist(err) {
		t.Fatal("isNotExist(non-fs-error) should be false")
	}
}

// TestIsNotExistFileMissing confirms that a "no such file" error is
// correctly identified so that a missing config.yaml is silently skipped.
func TestIsNotExistFileMissing(t *testing.T) {
	err := errors.New("open config.yaml: no such file or directory")
	if !isNotExist(err) {
		t.Fatal("isNotExist(no such file) should be true")
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

	// DB password is needed to pass validate(), but we expect a parse error
	// before validate() is reached.
	t.Setenv("TSUNDOKU_DATABASE_PASSWORD", "pw")

	_, err = Load()
	if err == nil {
		t.Fatal("expected error for malformed config.yaml, got nil")
	}
}
