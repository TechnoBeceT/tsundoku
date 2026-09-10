package download

import (
	"context"
	"path/filepath"

	"github.com/google/uuid"
)

// ProviderChapterCacheInvalidator removes the disposable staging directory
// paired with a ProviderChapter's cached page links. A blank staging root is a
// no-op for deployments that do not configure disk staging.
type ProviderChapterCacheInvalidator struct {
	stagingRoot string
}

// NewProviderChapterCacheInvalidator returns an invalidator rooted at the same
// staging directory supplied to the page fetcher and download dispatcher.
func NewProviderChapterCacheInvalidator(stagingRoot string) *ProviderChapterCacheInvalidator {
	return &ProviderChapterCacheInvalidator{stagingRoot: stagingRoot}
}

// InvalidateProviderChapterCache removes only providerChapterID's disposable
// page-staging directory. Database page_links are cleared by the chapter
// reconcile only after this operation succeeds.
func (i *ProviderChapterCacheInvalidator) InvalidateProviderChapterCache(_ context.Context, providerChapterID uuid.UUID) error {
	if i == nil || i.stagingRoot == "" {
		return nil
	}
	return removeStagingDir(filepath.Join(i.stagingRoot, providerChapterID.String()))
}
