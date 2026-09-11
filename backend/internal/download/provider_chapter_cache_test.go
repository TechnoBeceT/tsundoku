package download_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/download"
)

func TestProviderChapterCacheInvalidatorRemovesOnlyRequestedStagingDirectory(t *testing.T) {
	root := t.TempDir()
	targetID := uuid.New()
	siblingID := uuid.New()
	target := filepath.Join(root, targetID.String())
	sibling := filepath.Join(root, siblingID.String())
	for _, dir := range []string{target, sibling} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("create staging dir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "000001.jpg"), []byte("staged"), 0o600); err != nil {
			t.Fatalf("create staged page in %s: %v", dir, err)
		}
	}

	invalidator := download.NewProviderChapterCacheInvalidator(root)
	if err := invalidator.InvalidateProviderChapterCache(context.Background(), targetID); err != nil {
		t.Fatalf("InvalidateProviderChapterCache: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("target staging directory stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(sibling, "000001.jpg")); err != nil {
		t.Errorf("sibling staging page was changed: %v", err)
	}
}

func TestProviderChapterCacheInvalidatorBlankRootIsNoOp(t *testing.T) {
	invalidator := download.NewProviderChapterCacheInvalidator("")
	if err := invalidator.InvalidateProviderChapterCache(context.Background(), uuid.New()); err != nil {
		t.Fatalf("blank-root invalidation: %v", err)
	}
}
